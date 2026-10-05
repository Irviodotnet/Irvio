// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/pion/stun/v3"

	"hydra.local/hydra/internal/socketmux"
	"hydra.local/hydra/internal/stunprobe"
)

// BindingExchanger performs transaction-aware STUN Binding and must honor cancellation.
type BindingExchanger interface {
	Exchange(context.Context, stunprobe.BindingRequest) (stunprobe.BindingResponse, error)
}

// Profiler runs bounded RFC 5780 diagnostics without owning its exchanger.
type Profiler struct {
	exchanger BindingExchanger
	config    Config
	context   context.Context
	cancel    context.CancelFunc

	mu        sync.Mutex
	closed    bool
	active    map[socketmux.AddressFamily]bool
	closeOnce sync.Once
	wg        sync.WaitGroup
	stats     counters
	paceMu    sync.Mutex
	nextStart time.Time
}

// New creates a profiler over an existing STUN Binding exchanger.
func New(exchanger BindingExchanger, config Config) (*Profiler, error) {
	if exchanger == nil {
		return nil, fmt.Errorf("create NAT behavior profiler: nil Binding exchanger")
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("validate NAT behavior profiler config: %w", err)
	}
	profilerContext, cancel := context.WithCancel(context.Background())
	return &Profiler{
		exchanger: exchanger,
		config:    config,
		context:   profilerContext,
		cancel:    cancel,
		active:    make(map[socketmux.AddressFamily]bool, 2),
	}, nil
}

// Profile determines mapping and filtering behavior for one address family.
func (p *Profiler) Profile(ctx context.Context, primaryServer netip.AddrPort) (Profile, error) {
	if ctx == nil {
		return Profile{}, fmt.Errorf("profile NAT behavior: nil context")
	}
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if err := validUnicastEndpoint(primaryServer); err != nil {
		return Profile{}, err
	}
	family := endpointFamily(primaryServer)
	profileContext, release, err := p.begin(ctx, family)
	if err != nil {
		return Profile{}, err
	}
	defer release()

	p.stats.profilesStarted.Add(1)
	started := time.Now()
	profile := Profile{
		Family: family, PrimaryServer: primaryServer, Translation: TranslationUnknown,
		Mapping: MappingUnknown, Filtering: FilteringUnknown, StartedAt: started,
		Observations: make([]Observation, 0, p.config.MaxObservations),
	}
	baseline, exchangeErr := p.exchange(profileContext, &profile, StepBaseline, regularRequest(primaryServer))
	if exchangeErr != nil {
		if lifecycleErr := p.lifecycleError(ctx, profileContext); lifecycleErr != nil {
			profile.Reason = reasonForError(lifecycleErr, p.isClosed())
			p.finish(&profile, ProfileInconclusive)
			p.stats.profilesCanceled.Add(1)
			return profile, lifecycleErr
		}
		if errors.Is(exchangeErr, stunprobe.ErrTimeout) {
			profile.Reason = ReasonTimeout
			p.finish(&profile, ProfileConnectivityUnconfirmed)
			return profile, nil
		}
		if exchangerUnavailable(exchangeErr) {
			profile.Reason = ReasonInvalidResponse
			p.finish(&profile, ProfileInconclusive)
			return profile, fmt.Errorf("profile RFC5780 baseline: %w", exchangeErr)
		}
		profile.Reason = protocolReason(exchangeErr)
		p.finish(&profile, ProfileServerUnsupported)
		return profile, nil
	}
	if err := validateMappedPair(baseline, true); err != nil {
		profile.LocalEndpoint = baseline.LocalEndpoint
		profile.InitialMappedEndpoint = baseline.MappedEndpoint
		if errors.Is(err, ErrServerUnsupported) {
			profile.Reason = ReasonServerUnsupported
			p.finish(&profile, ProfileServerUnsupported)
		} else {
			profile.Reason = ReasonInvalidResponse
			p.finish(&profile, ProfileInconclusive)
		}
		return profile, nil
	}
	topology, topologyErr := topologyFromBaseline(primaryServer, baseline)
	if topologyErr != nil {
		profile.LocalEndpoint = baseline.LocalEndpoint
		profile.InitialMappedEndpoint = baseline.MappedEndpoint
		if errors.Is(topologyErr, ErrServerUnsupported) {
			profile.Reason = ReasonServerUnsupported
			p.finish(&profile, ProfileServerUnsupported)
		} else {
			profile.Reason = ReasonInvalidResponse
			p.finish(&profile, ProfileInconclusive)
		}
		return profile, nil
	}
	profile.Topology = topology
	profile.LocalEndpoint = baseline.LocalEndpoint
	profile.InitialMappedEndpoint = baseline.MappedEndpoint
	profile.Translation = translationState(baseline.LocalEndpoint, baseline.MappedEndpoint)

	filtering, inferred, filteringReason, filteringErr := p.profileFiltering(profileContext, &profile, topology)
	if filteringErr != nil {
		profile.Reason = reasonForError(filteringErr, p.isClosed())
		p.finish(&profile, ProfileInconclusive)
		if isCancellation(filteringErr) {
			p.stats.profilesCanceled.Add(1)
		}
		return profile, filteringErr
	}
	profile.Filtering = filtering
	profile.FilteringInferredFromTimeout = inferred

	mappingReason := ReasonNone
	if profile.Translation == TranslationAbsent {
		profile.Mapping = MappingEndpointIndependent
	} else {
		profile.Mapping, mappingReason, exchangeErr = p.profileMapping(profileContext, &profile, topology, baseline.MappedEndpoint)
		if exchangeErr != nil {
			profile.Reason = reasonForError(exchangeErr, p.isClosed())
			p.finish(&profile, ProfileInconclusive)
			if isCancellation(exchangeErr) {
				p.stats.profilesCanceled.Add(1)
			}
			return profile, exchangeErr
		}
	}

	profile.Reason = filteringReason
	if profile.Reason == ReasonNone {
		profile.Reason = mappingReason
	}
	p.finish(&profile, finalStatus(profile.Mapping, profile.Filtering, profile.Reason))
	return profile, nil
}

func (p *Profiler) profileFiltering(ctx context.Context, profile *Profile, topology Topology) (FilteringBehavior, bool, ObservationReason, error) {
	testII := stunprobe.BindingRequest{
		Destination: topology.Primary, ExpectedSuccessSource: topology.AlternateIPAlternatePort,
		AllowedErrorSources: []netip.AddrPort{topology.Primary}, ChangeIP: true, ChangePort: true,
	}
	response, err := p.exchange(ctx, profile, StepFilteringChangeIPPort, testII)
	if err == nil {
		if err := validateChangedSourceResponse(response, topology.AlternateIPAlternatePort); err != nil {
			return FilteringUnknown, false, ReasonInvalidResponse, nil
		}
		return FilteringEndpointIndependent, false, ReasonNone, nil
	}
	if lifecycleErr := p.lifecycleError(ctx, ctx); lifecycleErr != nil {
		return FilteringUnknown, false, ReasonNone, lifecycleErr
	}
	if isUnknownAttribute(err) {
		return FilteringUnknown, false, ReasonServerUnsupported, nil
	}
	if exchangerUnavailable(err) {
		return FilteringUnknown, false, ReasonNone, err
	}
	if !errors.Is(err, stunprobe.ErrTimeout) {
		return FilteringUnknown, false, protocolReason(err), nil
	}
	if _, livenessErr := p.exchange(ctx, profile, StepFilteringLivenessAfterChangeIPPort, regularRequest(topology.Primary)); livenessErr != nil {
		if lifecycleErr := p.lifecycleError(ctx, ctx); lifecycleErr != nil {
			return FilteringUnknown, false, ReasonNone, lifecycleErr
		}
		return FilteringUnknown, false, ReasonLivenessFailed, nil
	}

	testIII := stunprobe.BindingRequest{
		Destination: topology.Primary, ExpectedSuccessSource: topology.PrimaryIPAlternatePort,
		AllowedErrorSources: []netip.AddrPort{topology.Primary}, ChangePort: true,
	}
	response, err = p.exchange(ctx, profile, StepFilteringChangePort, testIII)
	if err == nil {
		if err := validateChangedSourceResponse(response, topology.PrimaryIPAlternatePort); err != nil {
			return FilteringUnknown, false, ReasonInvalidResponse, nil
		}
		return FilteringAddressDependent, true, ReasonNone, nil
	}
	if lifecycleErr := p.lifecycleError(ctx, ctx); lifecycleErr != nil {
		return FilteringUnknown, false, ReasonNone, lifecycleErr
	}
	if isUnknownAttribute(err) {
		return FilteringUnknown, false, ReasonServerUnsupported, nil
	}
	if exchangerUnavailable(err) {
		return FilteringUnknown, false, ReasonNone, err
	}
	if !errors.Is(err, stunprobe.ErrTimeout) {
		return FilteringUnknown, false, protocolReason(err), nil
	}
	if _, livenessErr := p.exchange(ctx, profile, StepFilteringFinalLiveness, regularRequest(topology.Primary)); livenessErr != nil {
		if lifecycleErr := p.lifecycleError(ctx, ctx); lifecycleErr != nil {
			return FilteringUnknown, false, ReasonNone, lifecycleErr
		}
		return FilteringUnknown, false, ReasonLivenessFailed, nil
	}
	return FilteringAddressAndPortDependent, true, ReasonNone, nil
}

func (p *Profiler) profileMapping(ctx context.Context, profile *Profile, topology Topology, initial netip.AddrPort) (MappingBehavior, ObservationReason, error) {
	second, err := p.exchange(ctx, profile, StepMappingAlternateIPPrimaryPort, regularRequest(topology.AlternateIPPrimaryPort))
	if err != nil {
		if lifecycleErr := p.lifecycleError(ctx, ctx); lifecycleErr != nil {
			return MappingUnknown, ReasonNone, lifecycleErr
		}
		if exchangerUnavailable(err) {
			return MappingUnknown, ReasonNone, err
		}
		return MappingUnknown, protocolReason(err), nil
	}
	if err := validateMappedPair(second, false); err != nil {
		return MappingUnknown, ReasonInvalidResponse, nil
	}
	if second.MappedEndpoint == initial {
		return MappingEndpointIndependent, ReasonNone, nil
	}
	third, err := p.exchange(ctx, profile, StepMappingAlternateIPAlternatePort, regularRequest(topology.AlternateIPAlternatePort))
	if err != nil {
		if lifecycleErr := p.lifecycleError(ctx, ctx); lifecycleErr != nil {
			return MappingUnknown, ReasonNone, lifecycleErr
		}
		if exchangerUnavailable(err) {
			return MappingUnknown, ReasonNone, err
		}
		return MappingUnknown, protocolReason(err), nil
	}
	if err := validateMappedPair(third, false); err != nil {
		return MappingUnknown, ReasonInvalidResponse, nil
	}
	if third.MappedEndpoint == second.MappedEndpoint {
		return MappingAddressDependent, ReasonNone, nil
	}
	return MappingAddressAndPortDependent, ReasonNone, nil
}

func (p *Profiler) exchange(ctx context.Context, profile *Profile, step TestStep, request stunprobe.BindingRequest) (stunprobe.BindingResponse, error) {
	if err := p.waitForPacing(ctx); err != nil {
		p.record(profile, step, request, stunprobe.BindingResponse{}, err)
		return stunprobe.BindingResponse{}, err
	}
	p.stats.exchanges.Add(1)
	response, err := p.exchanger.Exchange(ctx, request)
	if errors.Is(err, stunprobe.ErrTimeout) {
		p.stats.exchangeTimeouts.Add(1)
	} else if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		p.stats.exchangeProtocolErrors.Add(1)
	}
	p.record(profile, step, request, response, err)
	return response, err
}

func (p *Profiler) record(profile *Profile, step TestStep, request stunprobe.BindingRequest, response stunprobe.BindingResponse, err error) {
	if len(profile.Observations) >= p.config.MaxObservations {
		return
	}
	observation := Observation{
		Step: step, RequestDestination: request.Destination, ExpectedResponseSource: request.ExpectedSuccessSource,
		ActualResponseSource: response.ResponseSource, MappedEndpoint: response.MappedEndpoint,
		Attempts: response.Attempts, RoundTrip: response.RoundTrip,
	}
	switch {
	case err == nil:
		observation.Outcome = OutcomeSuccess
	case errors.Is(err, stunprobe.ErrTimeout):
		observation.Outcome, observation.Reason = OutcomeTimeout, ReasonTimeout
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		observation.Outcome, observation.Reason = OutcomeCanceled, ReasonContextCanceled
	case errors.Is(err, ErrClosed):
		observation.Outcome, observation.Reason = OutcomeClosed, ReasonProfilerClosed
	default:
		observation.Outcome, observation.Reason = OutcomeProtocolError, protocolReason(err)
	}
	profile.Observations = append(profile.Observations, observation)
}

func (p *Profiler) begin(ctx context.Context, family socketmux.AddressFamily) (context.Context, func(), error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, nil, ErrClosed
	}
	if p.active[family] {
		p.stats.profilesRejectedInProgress.Add(1)
		p.mu.Unlock()
		return nil, nil, ErrProbeInProgress
	}
	p.active[family] = true
	p.wg.Add(1)
	p.mu.Unlock()

	profileContext, cancel := context.WithCancel(ctx)
	stopProfilerCancel := context.AfterFunc(p.context, cancel)
	release := func() {
		stopProfilerCancel()
		cancel()
		p.mu.Lock()
		delete(p.active, family)
		p.mu.Unlock()
		p.wg.Done()
	}
	return profileContext, release, nil
}

func (p *Profiler) lifecycleError(caller, operation context.Context) error {
	if p.isClosed() {
		return ErrClosed
	}
	if err := caller.Err(); err != nil {
		return err
	}
	return operation.Err()
}

func (p *Profiler) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *Profiler) finish(profile *Profile, status ProfileStatus) {
	profile.Status = status
	profile.CompletedAt = time.Now()
	profile.Duration = profile.CompletedAt.Sub(profile.StartedAt)
	profile.Observations = append([]Observation(nil), profile.Observations...)
	switch status {
	case ProfileComplete:
		p.stats.profilesComplete.Add(1)
	case ProfilePartial:
		p.stats.profilesPartial.Add(1)
	case ProfileServerUnsupported:
		p.stats.profilesServerUnsupported.Add(1)
	case ProfileConnectivityUnconfirmed:
		p.stats.profilesConnectivityUnconfirmed.Add(1)
	case ProfileInconclusive:
		p.stats.profilesInconclusive.Add(1)
	}
}

// Stats returns a race-safe profiler snapshot.
func (p *Profiler) Stats() Stats {
	return Stats{
		ProfilesStarted: p.stats.profilesStarted.Load(), ProfilesComplete: p.stats.profilesComplete.Load(),
		ProfilesPartial: p.stats.profilesPartial.Load(), ProfilesServerUnsupported: p.stats.profilesServerUnsupported.Load(),
		ProfilesConnectivityUnconfirmed: p.stats.profilesConnectivityUnconfirmed.Load(),
		ProfilesInconclusive:            p.stats.profilesInconclusive.Load(),
		ProfilesRejectedInProgress:      p.stats.profilesRejectedInProgress.Load(), ProfilesCanceled: p.stats.profilesCanceled.Load(),
		Exchanges: p.stats.exchanges.Load(), ExchangeTimeouts: p.stats.exchangeTimeouts.Load(),
		ExchangeProtocolErrors: p.stats.exchangeProtocolErrors.Load(),
	}
}

// Close cancels active profiles without closing the exchanger or its transport.
func (p *Profiler) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.cancel()
		p.mu.Unlock()
	})
	p.wg.Wait()
	return nil
}

func (p *Profiler) waitForPacing(ctx context.Context) error {
	if p.config.MinimumTransactionInterval == 0 {
		return nil
	}
	p.paceMu.Lock()
	now := time.Now()
	scheduled := now
	if p.nextStart.After(now) {
		scheduled = p.nextStart
	}
	p.nextStart = scheduled.Add(p.config.MinimumTransactionInterval)
	p.paceMu.Unlock()
	delay := scheduled.Sub(now)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func regularRequest(server netip.AddrPort) stunprobe.BindingRequest {
	return stunprobe.BindingRequest{
		Destination: server, ExpectedSuccessSource: server, AllowedErrorSources: []netip.AddrPort{server},
	}
}

func validateMappedPair(response stunprobe.BindingResponse, requireMapped bool) error {
	if err := validUnicastEndpoint(response.MappedEndpoint); err != nil || endpointFamily(response.MappedEndpoint) != response.Family {
		return ErrMappedAddressMismatch
	}
	if !response.MappedAddress.Present {
		if requireMapped {
			return ErrServerUnsupported
		}
		return nil
	}
	if response.MappedAddress.AddrPort != response.MappedEndpoint {
		return ErrMappedAddressMismatch
	}
	return nil
}

func exchangerUnavailable(err error) bool {
	return errors.Is(err, stunprobe.ErrClosed) || errors.Is(err, stunprobe.ErrTransportClosed)
}

func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrClosed)
}

func validateChangedSourceResponse(response stunprobe.BindingResponse, expected netip.AddrPort) error {
	if response.ResponseSource != expected || !response.ResponseOrigin.Present || response.ResponseOrigin.AddrPort != response.ResponseSource {
		return ErrInvalidTopology
	}
	if response.Family != endpointFamily(expected) {
		return ErrInvalidTopology
	}
	return nil
}

func translationState(local, mapped netip.AddrPort) TranslationState {
	if !local.IsValid() || local.Port() == 0 || local.Addr().IsUnspecified() || local.Addr().Is4In6() {
		return TranslationUnknown
	}
	if local == mapped {
		return TranslationAbsent
	}
	return TranslationPresent
}

func isUnknownAttribute(err error) bool {
	var bindingErr *stunprobe.BindingError
	return errors.As(err, &bindingErr) && bindingErr.Code == stun.CodeUnknownAttribute
}

func protocolReason(err error) ObservationReason {
	if errors.Is(err, stunprobe.ErrTimeout) {
		return ReasonTimeout
	}
	if isUnknownAttribute(err) || errors.Is(err, ErrServerUnsupported) {
		return ReasonServerUnsupported
	}
	return ReasonInvalidResponse
}

func reasonForError(err error, closed bool) ObservationReason {
	if closed || errors.Is(err, ErrClosed) {
		return ReasonProfilerClosed
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ReasonContextCanceled
	}
	return protocolReason(err)
}

func finalStatus(mapping MappingBehavior, filtering FilteringBehavior, reason ObservationReason) ProfileStatus {
	if mapping != MappingUnknown && filtering != FilteringUnknown {
		return ProfileComplete
	}
	if mapping != MappingUnknown || filtering != FilteringUnknown {
		return ProfilePartial
	}
	if reason == ReasonServerUnsupported {
		return ProfileServerUnsupported
	}
	return ProfileInconclusive
}
