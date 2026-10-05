// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type operationKind uint8

const (
	operationNone operationKind = iota
	operationInitialSelection
	operationRefresh
	operationExhaustedSelection
	operationConnectInitial
	operationConnectRecovery
	operationConnectReserve
	operationConnectCold
)

type operation struct {
	kind      operationKind
	token     uint64
	cancel    context.CancelFunc
	candidate *CandidateEvaluation
	started   time.Time
	budget    time.Duration
}

type operationResult struct {
	kind       operationKind
	token      uint64
	selection  Selection
	candidate  *CandidateEvaluation
	connection Connection
	outcome    ConnectOutcome
	err        error
	duration   time.Duration
	event      EventType
}

type connectorResponse struct {
	result ConnectResult
	err    error
}

type commandKind uint8

const (
	commandUnderlay commandKind = iota + 1
	commandActiveEvent
	commandRefresh
	commandCountry
)

type command struct {
	kind     commandKind
	underlay UnderlayState
	event    ConnectionEventType
	peer     peer.ID
	token    uint64
	country  CountryIntent
	reply    chan error
}

// Selector owns one CountryIntent and at most one current ACTIVE connection.
type Selector struct {
	config Config

	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	commands chan command
	results  chan operationResult
	events   chan Event

	stateMu   sync.RWMutex
	snapshot  Snapshot
	started   bool
	closed    bool
	closeOnce sync.Once
	closeErr  error
	metrics   *metrics
}

func New(config Config) (*Selector, error) {
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("create Exit selector: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Selector{
		config: config, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		commands: make(chan command, 64), results: make(chan operationResult, 8),
		events: make(chan Event, config.EventCapacity), metrics: newMetrics(config.SampleCapacity),
		snapshot: Snapshot{State: StateIdle, Country: config.Country, Underlay: config.InitialUnderlay, ProviderAvailable: true, RecoveryRemaining: config.ActiveRecoveryBudget},
	}, nil
}

// Start begins selection. ProtectedMode must already be active.
func (s *Selector) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.config.Protected() {
		return ErrProtectedModeOff
	}
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return ErrClosed
	}
	if s.started {
		s.stateMu.Unlock()
		return ErrAlreadyStarted
	}
	s.started = true
	s.stateMu.Unlock()
	stop := context.AfterFunc(ctx, s.cancel)
	go func() {
		defer stop()
		s.run()
	}()
	return nil
}

func (s *Selector) UpdateUnderlay(state UnderlayState) error {
	if state.NetworkGeneration == 0 {
		return ErrInvalidConfig
	}
	return s.send(command{kind: commandUnderlay, underlay: state})
}

func (s *Selector) ReportActive(event ConnectionEventType) error {
	if event != ConnectionDisconnected && event != ConnectionRecovered && event != ConnectionFailed && event != ConnectionUpdated {
		return ErrInvalidConfig
	}
	snapshot := s.Current()
	if snapshot.Active == nil {
		return fmt.Errorf("report ACTIVE event: no ACTIVE Exit")
	}
	return s.send(command{kind: commandActiveEvent, event: event, peer: snapshot.Active.Candidate.PeerID, token: snapshot.LifecycleGeneration})
}

func (s *Selector) RefreshNow() error { return s.send(command{kind: commandRefresh}) }

func (s *Selector) SetCountry(country CountryIntent) error {
	normalized, err := normalizeCountry(country)
	if err != nil {
		return err
	}
	return s.send(command{kind: commandCountry, country: normalized})
}

func (s *Selector) send(value command) error {
	s.stateMu.RLock()
	started, closed := s.started, s.closed
	s.stateMu.RUnlock()
	if closed {
		return ErrClosed
	}
	if !started {
		return ErrNotStarted
	}
	value.reply = make(chan error, 1)
	select {
	case s.commands <- value:
	case <-s.done:
		return ErrClosed
	}
	select {
	case err := <-value.reply:
		return err
	case <-s.done:
		return ErrClosed
	}
}

func (s *Selector) Current() Snapshot {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	result := s.snapshot
	result.Active = cloneEvaluation(result.Active)
	result.Reserve = cloneEvaluation(result.Reserve)
	result.Cold = cloneEvaluation(result.Cold)
	return result
}

func (s *Selector) Events() <-chan Event { return s.events }
func (s *Selector) Stats() Stats         { return s.metrics.snapshot() }

func (s *Selector) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() { s.cancel() })
	s.stateMu.RLock()
	started := s.started
	s.stateMu.RUnlock()
	if started {
		<-s.done
	} else {
		s.stateMu.Lock()
		s.closed = true
		s.snapshot.State = StateClosed
		s.snapshot.Closed = true
		s.stateMu.Unlock()
	}
	return s.closeErr
}

func (s *Selector) run() {
	defer close(s.done)
	defer close(s.events)
	country := s.config.Country
	underlay := s.config.InitialUnderlay
	state := StateIdle
	lifecycle := uint64(1)
	providerAvailable := true
	recoveryRemaining := s.config.ActiveRecoveryBudget
	suppress := newSuppressions(s.config.SuppressionLimit)
	routeSuppress := newRouteSuppressions(s.config.SuppressionLimit)
	var active, reserve, cold *CandidateEvaluation
	var failedActive *CandidateEvaluation
	var activeConnection Connection
	var activeVersion uint64
	var activeWatchCancel context.CancelFunc
	var current operation
	var refreshTimer Timer
	var refreshC <-chan time.Time
	var resume operation

	update := func(next State) {
		state = next
		diagnostics := ConnectionDiagnostics{}
		if connection, ok := activeConnection.(DiagnosableConnection); ok {
			diagnostics = connection.Diagnostics()
		}
		s.stateMu.Lock()
		s.snapshot = Snapshot{
			State: state, Country: country, Underlay: underlay,
			LifecycleGeneration: lifecycle, Active: cloneEvaluation(active), Reserve: cloneEvaluation(reserve), Cold: cloneEvaluation(cold),
			ProviderAvailable: providerAvailable, RecoveryRemaining: recoveryRemaining, Connection: diagnostics, Closed: next == StateClosed,
		}
		s.stateMu.Unlock()
	}
	publish := func(kind EventType, candidate *CandidateEvaluation, outcome ConnectOutcome, duration time.Duration, failures ...error) {
		var id peer.ID
		if candidate != nil {
			id = candidate.Candidate.PeerID
		}
		var failure error
		if len(failures) != 0 {
			failure = failures[0]
		}
		event := Event{Type: kind, State: state, Country: country, PeerID: id, NetworkGeneration: underlay.NetworkGeneration, LifecycleGeneration: lifecycle, Outcome: outcome, Duration: duration, At: s.config.Clock.Now().UTC(), Err: failure}
		select {
		case s.events <- event:
		default:
			select {
			case <-s.events:
			default:
			}
			select {
			case s.events <- event:
			default:
			}
		}
	}
	stopRefresh := func() {
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
		refreshTimer, refreshC = nil, nil
	}
	cancelOperation := func(save bool) {
		if current.cancel == nil {
			return
		}
		if save {
			resume = current
			resume.cancel = nil
		}
		current.cancel()
		current = operation{}
		lifecycle++
	}
	chargeRecovery := func() {
		if current.kind != operationConnectRecovery {
			return
		}
		spent := s.config.Clock.Now().Sub(current.started)
		if spent <= 0 {
			return
		}
		if spent >= recoveryRemaining {
			recoveryRemaining = 0
		} else {
			recoveryRemaining -= spent
		}
	}
	scheduleRefresh := func(delay time.Duration) {
		stopRefresh()
		if delay <= 0 {
			delay = s.config.ProviderRetry
		}
		refreshTimer = s.config.Clock.NewTimer(delay)
		refreshC = refreshTimer.C()
	}
	randomRefresh := func() time.Duration {
		delay, err := JitteredRefresh(s.config.RefreshMinimum, s.config.RefreshMaximum, s.config.Random)
		if err != nil {
			return s.config.RefreshMinimum
		}
		return delay
	}
	providerExclusions := func(includeActive bool) []peer.ID {
		if includeActive && active != nil {
			return []peer.ID{active.Candidate.PeerID}
		}
		return nil
	}
	launchSelection := func(kind operationKind) {
		if !underlay.Usable() || current.cancel != nil || !s.config.Protected() {
			return
		}
		ctx, cancel := context.WithCancel(s.ctx)
		token := lifecycle
		current = operation{kind: kind, token: token, cancel: cancel, started: s.config.Clock.Now()}
		if kind == operationRefresh {
			publish(EventBackgroundRefreshStarted, active, OutcomeUnknown, 0)
		} else if kind == operationInitialSelection {
			update(StateSelecting)
			publish(EventInitialSelectionStarted, nil, OutcomeUnknown, 0)
		} else {
			update(StateRefreshingAfterExhaustion)
			publish(EventFreshBatchRequested, nil, OutcomeUnknown, 0)
			s.metrics.mutate(func(stats *Stats) { stats.FreshBatchRequests++ })
		}
		// Apply path suppression after acquisition; never exclude a peer whose new NetworkEpoch could recover it.
		request := CandidateRequest{Country: country, Limit: MaxCandidateBatch, ExcludePeerIDs: providerExclusions(active != nil)}
		excludedPeers := make(map[peer.ID]struct{}, 1)
		if active != nil {
			excludedPeers[active.Candidate.PeerID] = struct{}{}
		}
		evaluationConfig := EvaluationConfig{
			LocalPeer: s.config.LocalPeer, Country: country, Now: s.config.Clock.Now(), MinimumBandwidthMbps: s.config.MinimumBandwidthMbps,
			ProbeConcurrency: s.config.ProbeConcurrency, ProbeTimeout: s.config.ProbeTimeout, LatencyTolerance: s.config.LatencyTolerance,
			Suppressed: suppress.epochs(s.config.Clock.Now()), SuppressedRoutes: routeSuppress.epochs(s.config.Clock.Now()), Excluded: excludedPeers,
		}
		s.metrics.providerRequest()
		go func() {
			batch, err := s.config.Provider.GetCandidates(ctx, request)
			if err != nil {
				s.sendResult(operationResult{kind: kind, token: token, err: err})
				return
			}
			s.metrics.batch(len(batch.Candidates))
			selection, err := evaluateBatch(ctx, batch, s.config.Probe, evaluationConfig, evaluationHooks{
				started: func(candidate Candidate) {
					s.metrics.probeStart()
					s.sendResult(operationResult{kind: kind, token: token, candidate: &CandidateEvaluation{Candidate: candidate}, event: EventCandidateProbeStarted})
				},
				completed: func(candidate Candidate, result ProbeResult) {
					s.metrics.probeDone(result.Latency)
					s.sendResult(operationResult{kind: kind, token: token, candidate: &CandidateEvaluation{Candidate: candidate, Probe: result}, event: EventCandidateProbeCompleted})
				},
			})
			s.sendResult(operationResult{kind: kind, token: token, selection: selection, err: err})
		}()
	}
	launchConnect := func(kind operationKind, candidate *CandidateEvaluation, budget time.Duration) {
		if candidate == nil || !underlay.Usable() || current.cancel != nil || !s.config.Protected() {
			return
		}
		ctx, cancel := context.WithCancel(s.ctx)
		token := lifecycle
		started := s.config.Clock.Now()
		current = operation{kind: kind, token: token, cancel: cancel, candidate: cloneEvaluation(candidate), started: started, budget: budget}
		s.metrics.connectStart()
		switch kind {
		case operationConnectInitial:
			update(StateConnectingInitial)
		case operationConnectRecovery:
			update(StateSamePeerRecovery)
			publish(EventSamePeerRecoveryStarted, candidate, OutcomeUnknown, 0)
		case operationConnectReserve:
			update(StateConnectingReserve)
			publish(EventReserveConnectStarted, candidate, OutcomeUnknown, 0)
		case operationConnectCold:
			update(StateConnectingCold)
			publish(EventColdConnectStarted, candidate, OutcomeUnknown, 0)
		}
		if validateCandidate(candidate.Candidate, country, s.config.LocalPeer, s.config.Clock.Now()) != nil {
			s.sendResult(operationResult{kind: kind, token: token, candidate: cloneEvaluation(candidate), outcome: OutcomeUnavailable, err: ErrInvalidCandidate})
			return
		}
		go s.runConnect(ctx, cancel, token, kind, candidate, budget, started)
	}
	watchActive := func(connection Connection, candidate *CandidateEvaluation, token uint64) {
		if activeWatchCancel != nil {
			activeWatchCancel()
		}
		watchContext, cancel := context.WithCancel(s.ctx)
		activeWatchCancel = cancel
		go func() {
			for {
				select {
				case <-watchContext.Done():
					return
				case event, ok := <-connection.Events():
					if !ok {
						event.Type = ConnectionFailed
					}
					value := command{kind: commandActiveEvent, event: event.Type, peer: candidate.Candidate.PeerID, token: token}
					select {
					case s.commands <- value:
					case <-watchContext.Done():
					}
					if !ok {
						return
					}
				}
			}
		}()
	}
	startFallback := func() {
		if reserve != nil {
			candidate := reserve
			reserve = nil
			launchConnect(operationConnectReserve, candidate, s.config.FastConnectTimeout)
			return
		}
		if cold != nil {
			candidate := cold
			cold = nil
			launchConnect(operationConnectCold, candidate, s.config.FastConnectTimeout)
			return
		}
		publish(EventCandidateSetExhausted, nil, OutcomeUnknown, 0)
		launchSelection(operationExhaustedSelection)
	}
	enterLocalPromotionFailure := func(result operationResult) {
		if activeWatchCancel != nil {
			activeWatchCancel()
			activeWatchCancel = nil
		}
		if activeConnection != nil {
			_ = activeConnection.Close()
		}
		if active != nil {
			failedActive = cloneEvaluation(active)
		}
		activeConnection, active, reserve, cold = nil, nil, nil, nil
		recoveryRemaining = s.config.ActiveRecoveryBudget
		update(StateLocalPromotionFailed)
		publish(EventLocalPromotionFailed, result.candidate, result.outcome, result.duration, result.err)
		scheduleRefresh(s.config.LocalRetry)
	}

	if underlay.Usable() {
		launchSelection(operationInitialSelection)
	} else {
		update(StateWaitingForUnderlay)
	}

	for {
		select {
		case <-s.ctx.Done():
			update(StateClosing)
			stopRefresh()
			cancelOperation(false)
			if activeWatchCancel != nil {
				activeWatchCancel()
			}
			if activeConnection != nil {
				s.closeErr = activeConnection.Close()
			}
			activeConnection, active, reserve, cold, failedActive = nil, nil, nil, nil, nil
			update(StateClosed)
			publish(EventClosed, nil, OutcomeUnknown, 0)
			s.stateMu.Lock()
			s.closed = true
			s.stateMu.Unlock()
			return

		case value := <-s.commands:
			err := func() error {
				switch value.kind {
				case commandUnderlay:
					if value.underlay.NetworkGeneration < underlay.NetworkGeneration {
						return nil
					}
					wasUsable := underlay.Usable()
					generationChanged := value.underlay.NetworkGeneration > underlay.NetworkGeneration
					underlay = value.underlay
					if !underlay.Usable() {
						chargeRecovery()
						cancelOperation(true)
						stopRefresh()
						update(StateWaitingForUnderlay)
						if wasUsable {
							publish(EventUnderlayLost, active, OutcomeUnknown, 0)
							s.metrics.mutate(func(stats *Stats) { stats.UnderlayPauses++ })
						}
						return nil
					}
					if !wasUsable {
						publish(EventUnderlayRestored, active, OutcomeUnknown, 0)
					}
					if generationChanged {
						chargeRecovery()
						stopRefresh()
						suppress.clear()
						routeSuppress.clear()
						if current.cancel != nil {
							cancelOperation(false)
						} else {
							lifecycle++
						}
					}
					if state == StateLocalPromotionFailed && !generationChanged {
						return nil
					}
					if active != nil {
						if generationChanged || state == StateWaitingForUnderlay {
							resume = operation{}
							launchConnect(operationConnectRecovery, active, recoveryRemaining)
						} else {
							update(StateConnected)
						}
					} else if resume.candidate != nil && (resume.kind == operationConnectInitial || resume.kind == operationConnectReserve || resume.kind == operationConnectCold) {
						candidate, kind := resume.candidate, resume.kind
						resume = operation{}
						launchConnect(kind, candidate, s.config.FastConnectTimeout)
					} else {
						resume = operation{}
						launchSelection(operationInitialSelection)
					}

				case commandActiveEvent:
					if active == nil || value.peer != active.Candidate.PeerID {
						return nil
					}
					if value.reply == nil && value.token != activeVersion || value.reply != nil && value.token != lifecycle {
						return nil
					}
					switch value.event {
					case ConnectionRecovered:
						if state == StateSamePeerRecovery && activeConnection != nil {
							cancelOperation(false)
							recoveryRemaining = s.config.ActiveRecoveryBudget
							update(StateConnected)
							publish(EventSamePeerRecovered, active, OutcomeConnected, 0)
							scheduleRefresh(randomRefresh())
						}
					case ConnectionUpdated:
						update(state)
						publish(EventConnectionUpdated, active, OutcomeConnected, 0)
					case ConnectionDisconnected, ConnectionFailed:
						publish(EventActiveDisconnected, active, OutcomeUnavailable, 0)
						if !underlay.Usable() {
							update(StateWaitingForUnderlay)
							return nil
						}
						if state != StateSamePeerRecovery {
							stopRefresh()
							if current.kind == operationRefresh {
								cancelOperation(false)
							}
							recoveryRemaining = s.config.ActiveRecoveryBudget
							launchConnect(operationConnectRecovery, active, recoveryRemaining)
						}
					}

				case commandRefresh:
					if !underlay.Usable() {
						return ErrUnderlayUnavailable
					}
					if state == StateLocalPromotionFailed {
						return nil
					}
					if current.cancel == nil {
						if active != nil {
							launchSelection(operationRefresh)
						} else {
							launchSelection(operationExhaustedSelection)
						}
					}

				case commandCountry:
					if value.country == country {
						return nil
					}
					cancelOperation(false)
					stopRefresh()
					if activeWatchCancel != nil {
						activeWatchCancel()
					}
					if activeConnection != nil {
						_ = activeConnection.Close()
					}
					activeConnection, active, reserve, cold, failedActive = nil, nil, nil, nil, nil
					country = value.country
					suppress.clear()
					routeSuppress.clear()
					lifecycle++
					publish(EventCountryIntentChanged, nil, OutcomeUnknown, 0)
					if underlay.Usable() {
						launchSelection(operationInitialSelection)
					} else {
						update(StateWaitingForUnderlay)
					}
				}
				return nil
			}()
			if value.reply != nil {
				value.reply <- err
			}

		case result := <-s.results:
			if result.event != 0 {
				if result.token == lifecycle && result.kind == current.kind {
					publish(result.event, result.candidate, OutcomeUnknown, 0)
				}
				continue
			}
			if result.token != lifecycle || result.kind != current.kind {
				if result.connection != nil {
					_ = result.connection.Close()
				}
				s.metrics.mutate(func(stats *Stats) { stats.StaleResultsDiscarded++ })
				continue
			}
			completedOperation := current
			current = operation{}
			if result.kind == operationInitialSelection || result.kind == operationRefresh || result.kind == operationExhaustedSelection {
				if result.err != nil {
					providerAvailable = false
					s.metrics.providerFailure()
					if active != nil {
						update(StateConnected)
						publish(EventProviderUnavailable, active, OutcomeFailed, 0)
						scheduleRefresh(randomRefresh())
					} else {
						update(StateNoExit)
						publish(EventProviderUnavailable, active, OutcomeFailed, 0)
						publish(EventNoExitAvailable, nil, OutcomeUnavailable, 0)
						scheduleRefresh(s.config.ProviderRetry)
					}
					continue
				}
				if !providerAvailable {
					publish(EventProviderRecovered, active, OutcomeConnected, 0)
				}
				providerAvailable = true
				publish(EventCandidateBatchReceived, result.selection.Active, OutcomeUnknown, 0)
				if result.kind == operationRefresh && active != nil {
					reserve = cloneEvaluation(result.selection.Active)
					cold = cloneEvaluation(result.selection.Reserve)
					update(StateConnected)
					publish(EventBackgroundRefreshCompleted, reserve, OutcomeUnknown, 0)
					scheduleRefresh(randomRefresh())
					continue
				}
				if result.selection.Active == nil {
					update(StateNoExit)
					publish(EventNoExitAvailable, nil, OutcomeUnavailable, 0)
					scheduleRefresh(s.config.ProviderRetry)
					continue
				}
				reserve, cold = cloneEvaluation(result.selection.Reserve), cloneEvaluation(result.selection.Cold)
				launchConnect(operationConnectInitial, result.selection.Active, s.config.FastConnectTimeout)
				continue
			}

			if result.outcome == OutcomeConnected && result.connection == nil {
				result.outcome = OutcomeFailed
				result.err = ErrInvalidCandidate
			}
			if result.outcome == OutcomeConnected && result.connection != nil {
				if promotable, ok := result.connection.(PromotableConnection); ok {
					// Bound establishment and promotion separately so broker/ICE cannot consume the AWG/BIND budget.
					if completedOperation.budget <= 0 {
						result.outcome = OutcomeUnavailable
						result.err = context.DeadlineExceeded
					} else {
						promotionTimeout := s.config.FastConnectTimeout
						if timed, ok := result.connection.(PromotionTimeoutConnection); ok {
							promotionTimeout = timed.PromotionTimeout()
						}
						if promotionTimeout <= 0 || promotionTimeout > 30*time.Second {
							result.outcome = OutcomeFailed
							result.err = ErrInvalidCandidate
						} else {
							promotionContext, cancel := context.WithTimeout(s.ctx, promotionTimeout)
							err := promotable.Promote(promotionContext)
							cancel()
							if err != nil {
								result.outcome = classifyOutcome(ConnectResult{}, err)
								result.err = err
							}
						}
					}
				}
			}
			s.metrics.connectDone(result.outcome, result.duration)
			if result.outcome == OutcomeConnected && result.connection != nil {
				oldConnection, oldActive := activeConnection, active
				if oldActive == nil {
					oldActive = failedActive
				}
				activeConnection = result.connection
				active = cloneEvaluation(result.candidate)
				failedActive = nil
				recoveryRemaining = s.config.ActiveRecoveryBudget
				lifecycle++
				activeVersion++
				update(StateConnected)
				watchActive(activeConnection, active, activeVersion)
				publish(EventActivePromoted, active, result.outcome, result.duration)
				s.metrics.mutate(func(stats *Stats) {
					if result.kind == operationConnectRecovery {
						stats.SamePeerRecoveries++
					}
					if oldActive != nil && oldActive.Candidate.PeerID != active.Candidate.PeerID {
						stats.Failovers++
					}
				})
				if result.kind == operationConnectRecovery {
					publish(EventSamePeerRecovered, active, result.outcome, result.duration)
				} else if oldActive != nil && oldActive.Candidate.PeerID != active.Candidate.PeerID {
					publish(EventExitFailoverCompleted, active, result.outcome, result.duration)
				}
				if oldConnection != nil && oldConnection != activeConnection {
					_ = oldConnection.Close()
				}
				if result.kind == operationConnectReserve || result.kind == operationConnectCold || reserve == nil || cold == nil {
					launchSelection(operationRefresh)
				} else {
					scheduleRefresh(randomRefresh())
				}
				continue
			}
			if result.connection != nil {
				_ = result.connection.Close()
			}
			if result.outcome == OutcomeLocalPromotionFailed {
				enterLocalPromotionFailure(result)
				continue
			}
			if result.kind == operationConnectRecovery {
				publish(EventSamePeerRecoveryExpired, active, result.outcome, result.duration)
				if active != nil && result.outcome != OutcomeBusy {
					if result.outcome == OutcomeRouteFailed {
						routeSuppress.add(active.Candidate, s.config.Clock.Now().Add(s.config.AvoidDuration))
					} else {
						suppress.add(active.Candidate, s.config.Clock.Now().Add(s.config.AvoidDuration))
					}
				}
				if activeWatchCancel != nil {
					activeWatchCancel()
				}
				if activeConnection != nil {
					_ = activeConnection.Close()
				}
				failedActive = cloneEvaluation(active)
				activeConnection, active = nil, nil
				startFallback()
				continue
			}
			if result.outcome != OutcomeBusy && result.candidate != nil {
				if result.outcome == OutcomeRouteFailed {
					routeSuppress.add(result.candidate.Candidate, s.config.Clock.Now().Add(s.config.AvoidDuration))
				} else {
					suppress.add(result.candidate.Candidate, s.config.Clock.Now().Add(s.config.AvoidDuration))
				}
			}
			switch result.kind {
			case operationConnectReserve:
				if result.outcome == OutcomeBusy {
					publish(EventReserveBusy, result.candidate, result.outcome, result.duration)
				} else {
					publish(EventReserveUnavailable, result.candidate, result.outcome, result.duration)
				}
				startFallback()
			case operationConnectCold, operationConnectInitial:
				startFallback()
			}

		case <-refreshC:
			previousState := state
			refreshTimer, refreshC = nil, nil
			if !underlay.Usable() {
				update(StateWaitingForUnderlay)
				continue
			}
			if current.cancel == nil {
				if previousState == StateLocalPromotionFailed {
					launchSelection(operationInitialSelection)
				} else if active != nil {
					launchSelection(operationRefresh)
				} else {
					launchSelection(operationExhaustedSelection)
				}
			}
		}
	}
}

func (s *Selector) sendResult(result operationResult) {
	select {
	case s.results <- result:
	case <-s.ctx.Done():
		if result.connection != nil {
			_ = result.connection.Close()
		}
	}
}

func (s *Selector) runConnect(ctx context.Context, cancel context.CancelFunc, token uint64, kind operationKind, candidate *CandidateEvaluation, budget time.Duration, started time.Time) {
	defer cancel()
	timer := s.config.Clock.NewTimer(budget)
	defer timer.Stop()
	responses := make(chan connectorResponse, 1)
	go func() {
		connectContext := ctx
		connectCancel := func() {}
		if budget > 0 {
			connectContext, connectCancel = context.WithTimeout(ctx, budget)
		}
		defer connectCancel()
		result, err := s.config.Connector.Connect(connectContext, candidate.Candidate)
		responses <- connectorResponse{result: result, err: err}
	}()
	select {
	case received := <-responses:
		duration := s.config.Clock.Now().Sub(started)
		s.sendResult(operationResult{kind: kind, token: token, candidate: cloneEvaluation(candidate), connection: received.result.Connection, outcome: classifyOutcome(received.result, received.err), err: received.err, duration: duration})
	case <-timer.C():
		cancel()
		duration := s.config.Clock.Now().Sub(started)
		s.sendResult(operationResult{kind: kind, token: token, candidate: cloneEvaluation(candidate), outcome: OutcomeUnavailable, err: context.DeadlineExceeded, duration: duration})
		go s.forwardLateConnectResult(responses, token, kind, candidate, started)
	case <-ctx.Done():
		duration := s.config.Clock.Now().Sub(started)
		s.sendResult(operationResult{kind: kind, token: token, candidate: cloneEvaluation(candidate), outcome: OutcomeUnavailable, err: ctx.Err(), duration: duration})
		go s.forwardLateConnectResult(responses, token, kind, candidate, started)
	}
}

func (s *Selector) forwardLateConnectResult(responses <-chan connectorResponse, token uint64, kind operationKind, candidate *CandidateEvaluation, started time.Time) {
	received := <-responses
	s.sendResult(operationResult{
		kind: kind, token: token, candidate: cloneEvaluation(candidate), connection: received.result.Connection,
		outcome: classifyOutcome(received.result, received.err), err: received.err,
		duration: s.config.Clock.Now().Sub(started),
	})
}
