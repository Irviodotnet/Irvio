// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"

	"hydra.local/hydra/internal/awg"
	"hydra.local/hydra/internal/connsupervisor"
	"hydra.local/hydra/internal/exitselector"
	"hydra.local/hydra/internal/iceconnect"
	"hydra.local/hydra/internal/icesession"
	"hydra.local/hydra/internal/icesignal"
	"hydra.local/hydra/internal/transportgen"
	"hydra.local/hydra/internal/tunnellease"
	"hydra.local/hydra/internal/tunnelsignal"
)

type GenerationSource func() *transportgen.Generation

type InstallRequest struct {
	LocalPeer         peer.ID
	RemotePeer        peer.ID
	Transport         *transportgen.Generation
	ICE               *iceconnect.ConnectedSession
	SelectedEndpoint  awg.SelectedEndpoint
	RemoteAWGKey      awg.Key
	RemoteAWGKeyEpoch uint64
	Lease             tunnellease.Lease
	TunnelControl     []netip.Prefix
	BeforeUserRoutes  func(context.Context) (io.Closer, error)
}

type SessionInstaller interface {
	Install(context.Context, InstallRequest) (io.Closer, error)
}

// SessionPromoter publishes routing and AWG only after Selector accepts the lifecycle generation.
type SessionPromoter interface {
	Promote(context.Context) error
}

// SessionAWGKeyRotator keeps old routes through COMMIT and transfers them only after a fresh handshake.
type SessionAWGKeyRotator interface {
	StageRemoteAWGKey(awg.Key, awg.Key, peer.ID) error
	CommitRemoteAWGKey(context.Context, awg.Key, awg.Key, peer.ID) error
}

type SessionInstallerFunc func(context.Context, InstallRequest) (io.Closer, error)

func (f SessionInstallerFunc) Install(ctx context.Context, request InstallRequest) (io.Closer, error) {
	return f(ctx, request)
}

type ClientConnectorConfig struct {
	LocalPeer      peer.ID
	Generation     GenerationSource
	Signaling      *icesignal.Client
	Leases         *tunnelsignal.Client
	Bindings       BindingResolver
	Installer      SessionInstaller
	Validation     icesession.ValidationConfig
	RenewalLead    time.Duration
	RenewalRetry   time.Duration
	RequestTimeout time.Duration
	Logger         *slog.Logger
}

// ClientConnector owns the bounded M5b-M7 chain and keeps credentials and resources private.
type ClientConnector struct {
	config ClientConnectorConfig
	next   atomic.Uint64
}

type renewalClient struct{ client *tunnelsignal.Client }

func (c renewalClient) Renew(ctx context.Context, lease tunnellease.Lease) (tunnellease.Lease, error) {
	return c.client.Renew(ctx, lease)
}

func (c renewalClient) Release(ctx context.Context, lease tunnellease.Lease) error {
	err := c.client.Release(ctx, lease)
	if tunnelsignal.IsTerminalOwnershipError(err) {
		return nil
	}
	return err
}

func NewClientConnector(config ClientConnectorConfig) (*ClientConnector, error) {
	if config.LocalPeer == "" || config.Generation == nil || config.Signaling == nil ||
		config.Leases == nil || config.Bindings.Directory == nil ||
		config.Bindings.Registry == nil || config.Installer == nil {
		return nil, ErrInvalidConfig
	}
	if config.Validation == (icesession.ValidationConfig{}) {
		config.Validation = icesession.DefaultValidationConfig()
	}
	if err := config.Validation.Validate(); err != nil {
		return nil, err
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 5 * time.Second
	}
	if config.RenewalLead == 0 {
		config.RenewalLead = 20 * time.Second
	}
	if config.RenewalRetry == 0 {
		config.RenewalRetry = 2 * time.Second
	}
	if config.RequestTimeout <= 0 || config.RenewalLead <= 0 || config.RenewalRetry <= 0 {
		return nil, ErrInvalidConfig
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &ClientConnector{config: config}, nil
}

func (c *ClientConnector) Connect(ctx context.Context, candidate exitselector.Candidate) (result exitselector.ConnectResult, resultErr error) {
	defer func() {
		if resultErr != nil {
			c.config.Logger.Warn("EXIT_CONNECTION_ATTEMPT_FAILED", "peer_id", candidate.PeerID,
				"outcome", result.Outcome, "reason", resultErr)
		}
	}()
	if ctx == nil || candidate.PeerID == "" || candidate.PeerID == c.config.LocalPeer {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, ErrInvalidConfig
	}
	generation := c.config.Generation()
	if generation == nil || generation.Lifecycle() != transportgen.LifecycleReady {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeUnavailable}, fmt.Errorf("current transport unavailable")
	}
	sessionGeneration := connsupervisor.SessionGeneration(c.next.Add(1))
	binding, err := c.config.Bindings.Resolve(ctx, candidate.PeerID)
	if err != nil {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, err
	}
	prepared, err := generation.NewPreparedSession(ctx)
	if err != nil {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeUnavailable}, err
	}
	keepPrepared := false
	defer func() {
		if !keepPrepared {
			_ = prepared.Close()
		}
	}()
	exchange, err := c.config.Signaling.Exchange(ctx, candidate.PeerID, prepared)
	if err != nil {
		return exitselector.ConnectResult{Outcome: signalOutcome(err)}, err
	}
	attempt, err := iceconnect.New(prepared, iceconnect.Binding{
		SessionID: exchange.SessionID, Initiator: exchange.Initiator, Responder: exchange.Responder,
		LocalPeer: c.config.LocalPeer, RemotePeer: candidate.PeerID, Purpose: exchange.Purpose,
		LocalRole: exchange.LocalRole, RemoteRole: exchange.RemoteRole,
	}, iceconnect.RemoteState{
		SessionID: exchange.SessionID, PeerID: candidate.PeerID,
		Role: exchange.RemoteRole, Description: exchange.RemoteDescription,
	}, iceConnectConfig(c.config.Validation))
	if err != nil {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	if err := attempt.LoadRemoteCandidates(ctx); err != nil {
		_ = attempt.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	connected, err := attempt.Connect(ctx)
	if err != nil {
		_ = attempt.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeUnavailable}, err
	}
	keepPrepared = true
	endpoint, err := awg.SelectedEndpointFromICE(
		connected.Result(), generation.ID(), sessionGeneration, candidate.PeerID,
	)
	if err != nil {
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	if err := c.config.Bindings.AuthorizePromotion(binding); err != nil {
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, err
	}
	requestContext, cancel := context.WithTimeout(ctx, c.config.RequestTimeout)
	var leaseToken tunnellease.SessionToken
	copy(leaseToken[:], exchange.SessionID[:])
	lease, err := c.config.Leases.AcquireForSession(requestContext, candidate.PeerID, leaseToken)
	cancel()
	if err != nil {
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: signalOutcome(err)}, err
	}
	releaseLease := true
	defer func() {
		if releaseLease {
			releaseContext, releaseCancel := context.WithTimeout(context.Background(), c.config.RequestTimeout)
			_ = c.config.Leases.Release(releaseContext, lease)
			releaseCancel()
		}
	}()
	resource, err := c.config.Installer.Install(ctx, InstallRequest{
		LocalPeer: c.config.LocalPeer, RemotePeer: candidate.PeerID,
		Transport: generation, ICE: connected, SelectedEndpoint: endpoint,
		RemoteAWGKey: binding.Key, RemoteAWGKeyEpoch: binding.Binding.KeyEpoch, Lease: lease,
	})
	if err != nil {
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	if resource == nil {
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, ErrInvalidConfig
	}
	if err := c.config.Bindings.AuthorizePromotion(binding); err != nil {
		_ = resource.Close()
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, err
	}
	renewalConfig := tunnellease.DefaultRenewerConfig(renewalClient{client: c.config.Leases}, lease)
	renewalConfig.LeadTime = c.config.RenewalLead
	renewalConfig.RetryDelay = c.config.RenewalRetry
	renewalConfig.RequestTimeout = c.config.RequestTimeout
	renewalConfig.ReleaseOnClose = true
	renewer, err := tunnellease.NewRenewer(renewalConfig)
	if err != nil {
		_ = resource.Close()
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	if err := renewer.Start(context.Background()); err != nil {
		_ = resource.Close()
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	releaseLease = false
	connection := newRuntimeConnection(
		connected, attempt.ConnectionStateChanges(), resource, renewer,
		c.config.Logger, candidate.PeerID,
		func() error { return c.config.Bindings.AuthorizePromotion(binding) },
		exitselector.ConnectionDiagnostics{
			NetworkGeneration: generation.ID(), SessionGeneration: uint64(sessionGeneration),
			TunnelLeaseID: lease.ID.String(), TunnelLeaseValidUntil: lease.ValidUntil,
			AWGKeyEpoch: binding.Binding.KeyEpoch, SelectedEndpoint: endpoint.Address,
		},
	)
	connection.promotionTimeout = c.config.RequestTimeout
	return exitselector.ConnectResult{Outcome: exitselector.OutcomeConnected, Connection: connection}, nil
}

func iceConnectConfig(validation icesession.ValidationConfig) iceconnect.Config {
	config := iceconnect.DefaultConfig()
	config.Validation = validation
	return config
}

func signalOutcome(err error) exitselector.ConnectOutcome {
	var remote *icesignal.RemoteError
	if errors.As(err, &remote) {
		switch remote.Code {
		case icesignal.ErrorBusy:
			return exitselector.OutcomeBusy
		case icesignal.ErrorUnauthorized, icesignal.ErrorWrongPeer:
			return exitselector.OutcomeAuthFailed
		case icesignal.ErrorUnsupportedVersion:
			return exitselector.OutcomeIncompatible
		default:
			return exitselector.OutcomeUnavailable
		}
	}
	return exitselector.OutcomeUnavailable
}

type runtimeConnection struct {
	ice              *iceconnect.ConnectedSession
	resource         io.Closer
	renewer          *tunnellease.Renewer
	logger           *slog.Logger
	remotePeer       peer.ID
	promotionTimeout time.Duration
	events           chan exitselector.ConnectionEvent
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
	closeOnce        sync.Once
	closeErr         error
	promoteOnce      sync.Once
	promoteErr       error
	authorize        func() error
	diagnosticsMu    sync.RWMutex
	diagnostics      exitselector.ConnectionDiagnostics
}

func newRuntimeConnection(
	connected *iceconnect.ConnectedSession,
	states <-chan ice.ConnectionState,
	resource io.Closer,
	renewer *tunnellease.Renewer,
	logger *slog.Logger,
	remotePeer peer.ID,
	authorize func() error,
	diagnostics exitselector.ConnectionDiagnostics,
) *runtimeConnection {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	connection := &runtimeConnection{
		ice: connected, resource: resource, renewer: renewer, logger: logger, remotePeer: remotePeer,
		events: make(chan exitselector.ConnectionEvent, 8),
		ctx:    ctx, cancel: cancel, done: make(chan struct{}), authorize: authorize, diagnostics: diagnostics,
	}
	go connection.watch(states)
	return connection
}

func (c *runtimeConnection) PromotionTimeout() time.Duration {
	if c == nil {
		return 0
	}
	return c.promotionTimeout
}

func (c *runtimeConnection) Events() <-chan exitselector.ConnectionEvent { return c.events }

func (c *runtimeConnection) Diagnostics() exitselector.ConnectionDiagnostics {
	c.diagnosticsMu.RLock()
	result := c.diagnostics
	c.diagnosticsMu.RUnlock()
	if c.renewer != nil {
		if lease, _ := c.renewer.Current(); !lease.ValidUntil.IsZero() {
			result.TunnelLeaseID = lease.ID.String()
			result.TunnelLeaseValidUntil = lease.ValidUntil
		}
	}
	return result
}

func (c *runtimeConnection) updateAWGKeyEpoch(epoch uint64) {
	if epoch == 0 {
		return
	}
	c.diagnosticsMu.Lock()
	if epoch > c.diagnostics.AWGKeyEpoch {
		c.diagnostics.AWGKeyEpoch = epoch
	}
	c.diagnosticsMu.Unlock()
}

func (c *runtimeConnection) Promote(ctx context.Context) error {
	if c == nil || ctx == nil {
		return ErrInvalidConfig
	}
	c.promoteOnce.Do(func() {
		if c.authorize != nil {
			if err := c.authorize(); err != nil {
				c.promoteErr = &exitselector.OutcomeError{Outcome: exitselector.OutcomeAuthFailed, Err: err}
			}
		}
		if c.promoteErr == nil {
			if promoter, ok := c.resource.(SessionPromoter); ok {
				if err := promoter.Promote(ctx); err != nil {
					outcome := exitselector.OutcomeFailed
					var classified *exitselector.OutcomeError
					if errors.As(err, &classified) && classified.Outcome != exitselector.OutcomeUnknown {
						outcome = classified.Outcome
						c.promoteErr = err
					} else {
						c.promoteErr = &exitselector.OutcomeError{Outcome: outcome, Err: err}
					}
					diagnostics := c.Diagnostics()
					failureDomain := "path_or_auth"
					if outcome == exitselector.OutcomeLocalPromotionFailed {
						failureDomain = "local"
					}
					c.logger.Error("CLIENT_DATA_PLANE_PROMOTION_FAILED",
						"component", "runtime", "operation", "session_promote", "failure_domain", failureDomain,
						"outcome", outcome,
						"peer_id", c.remotePeer, "session_generation", diagnostics.SessionGeneration,
						"transport_generation", diagnostics.NetworkGeneration, "selected_endpoint", diagnostics.SelectedEndpoint,
						"reason", err)
				}
			}
		}
	})
	return c.promoteErr
}

func localPromotionFailure(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &exitselector.OutcomeError{
		Outcome: exitselector.OutcomeLocalPromotionFailed,
		Err:     fmt.Errorf("local promotion operation %s: %w", operation, err),
	}
}

func (c *runtimeConnection) watch(states <-chan ice.ConnectionState) {
	defer close(c.done)
	defer close(c.events)
	renewals := c.renewer.Events()
	for {
		select {
		case <-c.ctx.Done():
			return
		case state, ok := <-states:
			if !ok {
				c.publish(exitselector.ConnectionEvent{Type: exitselector.ConnectionFailed, At: time.Now().UTC()})
				return
			}
			switch state {
			case ice.ConnectionStateDisconnected:
				c.publish(exitselector.ConnectionEvent{Type: exitselector.ConnectionDisconnected, At: time.Now().UTC()})
			case ice.ConnectionStateConnected:
				c.publish(exitselector.ConnectionEvent{Type: exitselector.ConnectionRecovered, At: time.Now().UTC()})
			case ice.ConnectionStateFailed, ice.ConnectionStateClosed:
				c.publish(exitselector.ConnectionEvent{Type: exitselector.ConnectionFailed, At: time.Now().UTC()})
			}
		case renewal, ok := <-renewals:
			if !ok {
				renewals = nil
				continue
			}
			if renewal.Type == tunnellease.RenewalExpired {
				c.publish(exitselector.ConnectionEvent{Type: exitselector.ConnectionFailed, At: renewal.At})
			} else if renewal.Type == tunnellease.RenewalSucceeded {
				c.publish(exitselector.ConnectionEvent{Type: exitselector.ConnectionUpdated, At: renewal.At})
			}
		}
	}
}

func (c *runtimeConnection) publish(event exitselector.ConnectionEvent) {
	select {
	case c.events <- event:
	default:
		select {
		case <-c.events:
		default:
		}
		select {
		case c.events <- event:
		default:
		}
	}
}

func (c *runtimeConnection) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.cancel()
		<-c.done
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		// Release the remote lease before closing AWG; failed RELEASE must not prevent fail-closed local teardown.
		var releaseErr error
		if c.renewer != nil {
			releaseErr = c.renewer.Close(ctx)
		}
		resourceErr := c.resource.Close()
		var iceErr error
		if c.ice != nil {
			iceErr = c.ice.Close()
		}
		cancel()
		if releaseErr != nil {
			c.logger.Warn("TUNNEL_RELEASE_UNCONFIRMED", "error_class", "direct_control_unavailable")
		}
		// Only local resource failures keep ProtectedMode engaged.
		c.closeErr = errors.Join(resourceErr, iceErr)
	})
	return c.closeErr
}

var _ exitselector.Connector = (*ClientConnector)(nil)
var _ exitselector.Connection = (*runtimeConnection)(nil)
var _ exitselector.PromotableConnection = (*runtimeConnection)(nil)
var _ exitselector.DiagnosableConnection = (*runtimeConnection)(nil)
