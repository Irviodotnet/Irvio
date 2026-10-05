// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/awg"
	"hydra.local/hydra/internal/connsupervisor"
	"hydra.local/hydra/internal/exitselector"
	"hydra.local/hydra/internal/iceconnect"
	"hydra.local/hydra/internal/icesession"
	"hydra.local/hydra/internal/icesignal"
	"hydra.local/hydra/internal/rendezvous"
	"hydra.local/hydra/internal/sessioncontrol"
	"hydra.local/hydra/internal/sessionestablish"
	"hydra.local/hydra/internal/transportgen"
	"hydra.local/hydra/internal/tunnel"
	"hydra.local/hydra/internal/tunnellease"
	"hydra.local/hydra/internal/tunnelsignal"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

// BrokeredClientConnector exchanges signed signaling, then delegates direct ICE/AWG/lease lifetime.
type BrokeredClientConnector struct {
	config BrokeredClientConnectorConfig
	next   atomic.Uint64
}

type BrokeredClientConnectorConfig struct {
	LocalPeer        peer.ID
	IdentityPrivate  crypto.PrivKey
	LocalAWGKeyEpoch uint64
	SignedDescriptor func() []byte
	Generation       GenerationSource
	Provider         *rendezvous.CandidateProvider
	Bindings         BindingResolver
	Installer        SessionInstaller
	Validation       icesession.ValidationConfig
	SignalConfig     icesignal.Config
	Obfuscation      awg.ObfuscationConfig
	RenewalLead      time.Duration
	RenewalRetry     time.Duration
	RequestTimeout   time.Duration
	TUNName          string
	Logger           *slog.Logger
}

type m12Maintenance struct {
	mu        sync.Mutex
	client    *sessioncontrol.ReconnectingClient
	bindings  BindingResolver
	rotator   SessionAWGKeyRotator
	remote    peer.ID
	remoteKey awg.Key
	keyEpoch  uint64
	onRotated func(uint64)
	logger    *slog.Logger
	closed    bool
}

func (m *m12Maintenance) configureRotation(bindings BindingResolver, rotator SessionAWGKeyRotator, remote peer.ID, key awg.Key, epoch uint64) error {
	if bindings.Registry == nil || rotator == nil || remote == "" || key == (awg.Key{}) || epoch == 0 {
		return ErrInvalidConfig
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.rotator != nil {
		return ErrGenerationStale
	}
	m.bindings, m.rotator, m.remote, m.remoteKey, m.keyEpoch = bindings, rotator, remote, key, epoch
	return nil
}

func (m *m12Maintenance) observeRotation(callback func(uint64)) {
	m.mu.Lock()
	m.onRotated = callback
	m.mu.Unlock()
}

func (m *m12Maintenance) install(client *sessioncontrol.ReconnectingClient) error {
	if client == nil {
		return ErrInvalidConfig
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.client != nil {
		_ = client.Close()
		return ErrGenerationStale
	}
	m.client = client
	return nil
}

func (m *m12Maintenance) Renew(ctx context.Context, lease tunnellease.Lease) (tunnellease.Lease, error) {
	m.mu.Lock()
	client := m.client
	closed := m.closed
	m.mu.Unlock()
	if closed || client == nil {
		return tunnellease.Lease{}, sessioncontrol.ErrClosed
	}
	renewed, err := client.Renew(ctx, lease)
	if err != nil {
		return tunnellease.Lease{}, err
	}
	proposal := client.PendingRotation()
	if proposal.KeyEpoch == 0 {
		return renewed, nil
	}
	if m.logger != nil {
		m.logger.Info("AWG_DIRECT_ROTATION_CLIENT_PROPOSAL", "peer_id", m.remote, "key_epoch", proposal.KeyEpoch)
	}
	m.mu.Lock()
	bindings, rotator, remote := m.bindings, m.rotator, m.remote
	oldKey, oldEpoch := m.remoteKey, m.keyEpoch
	m.mu.Unlock()
	if rotator == nil || proposal.KeyEpoch <= oldEpoch {
		m.logRotationFailure(proposal.KeyEpoch, sessioncontrol.ErrStale)
		return tunnellease.Lease{}, sessioncontrol.ErrStale
	}
	resolved, err := bindings.ResolveSigned(proposal.SignedBinding, remote)
	if err != nil || resolved.Binding.KeyEpoch != proposal.KeyEpoch {
		m.logRotationFailure(proposal.KeyEpoch, err)
		return tunnellease.Lease{}, errors.Join(err, sessioncontrol.ErrStale)
	}
	if err := bindings.AuthorizePromotion(resolved); err != nil {
		m.logRotationFailure(proposal.KeyEpoch, err)
		return tunnellease.Lease{}, err
	}
	if m.logger != nil {
		m.logger.Info("AWG_DIRECT_ROTATION_CLIENT_STARTED", "peer_id", remote, "key_epoch", proposal.KeyEpoch)
	}
	rotated, err := client.Rotate(ctx, renewed, proposal, func() error {
		return rotator.StageRemoteAWGKey(oldKey, resolved.Key, remote)
	}, func() error {
		applyErr := rotator.CommitRemoteAWGKey(ctx, oldKey, resolved.Key, remote)
		if m.logger != nil {
			m.logger.Info("AWG_DIRECT_ROTATION_CLIENT_HANDSHAKE", "peer_id", remote, "key_epoch", proposal.KeyEpoch, "error", applyErr)
		}
		return applyErr
	})
	if err != nil {
		m.logRotationFailure(proposal.KeyEpoch, err)
		return tunnellease.Lease{}, err
	}
	m.mu.Lock()
	if proposal.KeyEpoch > m.keyEpoch {
		m.remoteKey, m.keyEpoch = resolved.Key, proposal.KeyEpoch
	}
	callback := m.onRotated
	m.mu.Unlock()
	if callback != nil {
		callback(proposal.KeyEpoch)
	}
	if m.logger != nil {
		m.logger.Info("AWG_DIRECT_ROTATION_CLIENT_COMMITTED", "peer_id", remote, "key_epoch", proposal.KeyEpoch)
	}
	return rotated, nil
}

func (m *m12Maintenance) logRotationFailure(epoch uint64, err error) {
	if m.logger != nil {
		m.logger.Warn("AWG_DIRECT_ROTATION_CLIENT_FAILED", "peer_id", m.remote, "key_epoch", epoch, "reason", err)
	}
}

func (m *m12Maintenance) Release(ctx context.Context, lease tunnellease.Lease) error {
	m.mu.Lock()
	client := m.client
	closed := m.closed
	m.mu.Unlock()
	if closed || client == nil {
		return nil
	}
	return client.Release(ctx, lease)
}

func (m *m12Maintenance) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	client := m.client
	m.client = nil
	m.mu.Unlock()
	if client != nil {
		return client.Close()
	}
	return nil
}

func NewBrokeredClientConnector(config BrokeredClientConnectorConfig) (*BrokeredClientConnector, error) {
	if config.TUNName == "" {
		config.TUNName = tunnel.DefaultName
	}
	if config.LocalPeer == "" || config.IdentityPrivate == nil || config.LocalAWGKeyEpoch == 0 ||
		config.SignedDescriptor == nil || config.Generation == nil || config.Provider == nil ||
		config.Bindings.Registry == nil || config.Installer == nil {
		return nil, ErrInvalidConfig
	}
	signer, err := peer.IDFromPrivateKey(config.IdentityPrivate)
	if err != nil || signer != config.LocalPeer {
		return nil, ErrInvalidConfig
	}
	if config.Validation == (icesession.ValidationConfig{}) {
		config.Validation = icesession.DefaultValidationConfig()
	}
	if err := config.Validation.Validate(); err != nil {
		return nil, err
	}
	if config.SignalConfig == (icesignal.Config{}) {
		config.SignalConfig = icesignal.DefaultConfig()
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
	return &BrokeredClientConnector{config: config}, nil
}

func (c *BrokeredClientConnector) Connect(ctx context.Context, candidate exitselector.Candidate) (result exitselector.ConnectResult, resultErr error) {
	defer func() {
		if resultErr != nil {
			c.config.Logger.Warn("BROKERED_EXIT_CONNECTION_ATTEMPT_FAILED", "peer_id", candidate.PeerID,
				"outcome", result.Outcome, "reason", resultErr)
		}
	}()
	if ctx == nil || candidate.PeerID == "" || candidate.PeerID == c.config.LocalPeer ||
		candidate.Transport != exitselector.CandidateTransportBrokered {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, ErrInvalidConfig
	}
	generation := c.config.Generation()
	if generation == nil || generation.Lifecycle() != transportgen.LifecycleReady {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeUnavailable}, ErrGenerationStale
	}
	resolved, err := c.config.Provider.Resolve(candidate)
	if err != nil {
		return exitselector.ConnectResult{Outcome: m12ConnectOutcome(err)}, err
	}
	descriptor, err := sessionestablish.BindingFromSignedAnnouncement(
		resolved.Descriptor.SignedEnvelope, candidate.PeerID, time.Now().UTC(),
	)
	if err != nil || descriptor.ID != candidate.DescriptorID ||
		descriptor.Incarnation != candidate.DescriptorIncarnation || descriptor.Sequence != candidate.DescriptorSequence {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, errors.Join(err, ErrGenerationStale)
	}
	binding, err := c.config.Bindings.VerifyLocator(resolved.Descriptor.SignedEnvelope, candidate.PeerID)
	if err != nil || binding.Binding.KeyEpoch != descriptor.AWGKeyEpoch {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, err
	}
	localSigned := c.config.SignedDescriptor()
	if local, verifyErr := sessionestablish.BindingFromSignedAnnouncement(localSigned, c.config.LocalPeer, time.Now().UTC()); verifyErr != nil || local.AWGKeyEpoch != c.config.LocalAWGKeyEpoch {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, errors.Join(verifyErr, ErrGenerationStale)
	}
	profile, err := awg.ObfuscationProfileHash(c.config.Obfuscation)
	if err != nil {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeIncompatible}, err
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
	carrier, err := c.config.Provider.BrokerCarrier(candidate)
	if err != nil {
		return exitselector.ConnectResult{Outcome: m12ConnectOutcome(err)}, err
	}
	runtimeGeneration := c.next.Add(1)
	initiator, err := sessionestablish.NewInitiator(sessionestablish.InitiatorOptions{
		Local: c.config.LocalPeer, PrivateKey: c.config.IdentityPrivate,
		LocalAWGKeyEpoch: c.config.LocalAWGKeyEpoch, SignedAnnouncement: localSigned,
		NetworkGeneration: uint64(generation.ID()), RuntimeGeneration: runtimeGeneration,
		AWGProfileHash: profile, Policy: sessionestablish.DefaultPolicy(),
	})
	if err != nil {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, err
	}
	var offer icesignal.Offer
	established, err := initiator.Establish(ctx, carrier, candidate.PeerID, candidate.Country, descriptor,
		sessionestablish.OfferFactoryFunc(func(_ context.Context, ready sessionestablish.ReadyContext) (*irviov1.IceOffer, error) {
			if ready.ExitNetworkGeneration == 0 || c.config.Generation() != generation {
				return nil, ErrGenerationStale
			}
			description, validateErr := icesession.ValidateDescription(prepared.LocalDescription(), c.config.Validation)
			if validateErr != nil {
				return nil, validateErr
			}
			sessionID, sessionErr := icesession.NewSessionID()
			if sessionErr != nil {
				return nil, sessionErr
			}
			now := time.Now().UTC().Truncate(time.Second)
			offer = icesignal.Offer{
				SessionID: sessionID, Initiator: c.config.LocalPeer, Responder: candidate.PeerID,
				Purpose: icesession.PurposeExit, Role: icesession.RoleControlling,
				Description: description, CreatedAt: now, ExpiresAt: now.Add(c.config.SignalConfig.DescriptionTTL),
			}
			return icesignal.OfferToProto(offer), nil
		}), true, true)
	if err != nil {
		return exitselector.ConnectResult{Outcome: m12ConnectOutcome(err)}, err
	}
	readySigned := established.Ready.Ready.Payload.GetReady().SignedAnnouncement
	binding, err = c.config.Bindings.ResolveSigned(readySigned, candidate.PeerID)
	if err != nil || binding.Binding.KeyEpoch != established.Ready.ExitAWGKeyEpoch ||
		binding.Key != awg.Key(established.Ready.ExitAWGPublicKey) {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, errors.Join(err, ErrGenerationStale)
	}
	answer, err := icesignal.AnswerFromProto(established.Body.IceAnswer, c.config.Validation)
	if err != nil || icesignal.ValidateAnswer(answer, offer, candidate.PeerID, c.config.LocalPeer, time.Now().UTC(), c.config.SignalConfig) != nil {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, errors.Join(err, icesignal.ErrInvalidMessage)
	}
	if c.config.Generation() != generation || uint64(generation.ID()) != established.Ready.Prepare.Payload.GetPrepare().NetworkGeneration {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeUnavailable}, ErrGenerationStale
	}
	if err := prepared.BindConnectivity(icesession.ConnectivityBinding{
		SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: offer.Responder,
		LocalPeer: c.config.LocalPeer, RemotePeer: candidate.PeerID, Purpose: offer.Purpose,
		LocalRole: offer.Role, RemoteRole: answer.Role,
	}); err != nil {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	attempt, err := iceconnect.New(prepared, iceconnect.Binding{
		SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: offer.Responder,
		LocalPeer: c.config.LocalPeer, RemotePeer: candidate.PeerID, Purpose: offer.Purpose,
		LocalRole: offer.Role, RemoteRole: answer.Role,
	}, iceconnect.RemoteState{
		SessionID: answer.SessionID, PeerID: candidate.PeerID, Role: answer.Role, Description: answer.Description,
	}, iceConnectConfig(c.config.Validation))
	if err != nil {
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	if err := attempt.LoadRemoteCandidates(ctx); err != nil {
		_ = attempt.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	c.config.Logger.Info("BROKERED_ICE_CONNECT_STARTED", "peer_id", candidate.PeerID,
		"local_candidates", m12CandidateEndpoints(offer.Description.Candidates),
		"remote_candidates", m12CandidateEndpoints(answer.Description.Candidates))
	connected, err := attempt.Connect(ctx)
	if err != nil {
		c.config.Logger.Warn("BROKERED_ICE_CONNECT_FAILED", "peer_id", candidate.PeerID, "reason", err)
		_ = attempt.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeUnavailable}, err
	}
	c.config.Logger.Info("BROKERED_ICE_CONNECTED", "peer_id", candidate.PeerID,
		"local_endpoint", connected.Result().LocalEndpoint, "remote_endpoint", connected.Result().RemoteEndpoint)
	keepPrepared = true
	sessionGeneration := connsupervisor.SessionGeneration(runtimeGeneration)
	endpoint, err := awg.SelectedEndpointFromICE(connected.Result(), generation.ID(), sessionGeneration, candidate.PeerID)
	if err != nil {
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	lease, err := tunnelsignal.LeaseFromProto(established.Body.ProvisionalGrant, candidate.PeerID, c.config.LocalPeer, time.Now().UTC())
	if err != nil || lease.Session != m12LeaseTokenFor(established.Ready.Attempt, established.Ready.OwnerToken, c.config.LocalPeer) {
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, errors.Join(err, sessioncontrol.ErrStale)
	}
	controlAddress, controlRoutes, err := m12ControlAddress(established.Body.ExitControlAddresses, established.Body.ExitControlPort, lease)
	if err != nil || !time.Unix(established.Body.ActivationDeadlineUnix, 0).After(time.Now().UTC()) {
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, errors.Join(err, sessioncontrol.ErrStale)
	}
	maintenance := &m12Maintenance{logger: c.config.Logger}
	c.config.Logger.Info("BROKERED_DATA_PLANE_INSTALL_STARTED", "peer_id", candidate.PeerID)
	resource, err := c.config.Installer.Install(ctx, InstallRequest{
		LocalPeer: c.config.LocalPeer, RemotePeer: candidate.PeerID,
		Transport: generation, ICE: connected, SelectedEndpoint: endpoint,
		RemoteAWGKey: binding.Key, RemoteAWGKeyEpoch: binding.Binding.KeyEpoch, Lease: lease,
		TunnelControl: controlRoutes,
		BeforeUserRoutes: func(bindContext context.Context) (io.Closer, error) {
			c.config.Logger.Info("TUNNEL_SESSION_CONTROL_BIND_STARTED", "peer_id", candidate.PeerID)
			deadline := time.Unix(established.Body.ActivationDeadlineUnix, 0)
			if current, ok := bindContext.Deadline(); !ok || deadline.Before(current) {
				var cancel context.CancelFunc
				bindContext, cancel = context.WithDeadline(bindContext, deadline)
				defer cancel()
			}
			client, dialErr := sessioncontrol.DialReconnecting(bindContext, sessioncontrol.ReconnectingConfig{
				Address: controlAddress, PrivateKey: c.config.IdentityPrivate, ExpectedExit: candidate.PeerID,
				Lease: lease, Dial: sessioncontrol.TunnelDialer(c.config.TUNName), Timeout: c.config.RequestTimeout,
			})
			if dialErr != nil {
				muxStats := generation.Mux().Stats()
				c.config.Logger.Warn("TUNNEL_SESSION_CONTROL_BIND_FAILED", "peer_id", candidate.PeerID, "reason", dialErr,
					"mux_received", muxStats.IPv4.ReceivedPackets+muxStats.IPv6.ReceivedPackets,
					"mux_control", muxStats.ControlEnqueued, "mux_data", muxStats.DataEnqueued,
					"mux_classifier_drop", muxStats.ClassifierDrops, "mux_data_overflow", muxStats.DataOverflowDrops)
				return nil, dialErr
			}
			if dialErr = maintenance.install(client); dialErr != nil {
				return nil, dialErr
			}
			c.config.Logger.Info("TUNNEL_SESSION_CONTROL_BOUND", "peer_id", candidate.PeerID)
			return maintenance, nil
		},
	})
	if err != nil || resource == nil {
		c.config.Logger.Warn("BROKERED_DATA_PLANE_INSTALL_FAILED", "peer_id", candidate.PeerID, "reason", err)
		_ = maintenance.Close()
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, errors.Join(err, ErrInvalidConfig)
	}
	rotator, ok := resource.(SessionAWGKeyRotator)
	if !ok {
		_ = resource.Close()
		_ = maintenance.Close()
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, ErrInvalidConfig
	}
	if err := maintenance.configureRotation(c.config.Bindings, rotator, candidate.PeerID, binding.Key, binding.Binding.KeyEpoch); err != nil {
		_ = resource.Close()
		_ = maintenance.Close()
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeFailed}, err
	}
	c.config.Logger.Info("BROKERED_DATA_PLANE_INSTALLED", "peer_id", candidate.PeerID)
	if err := c.config.Bindings.AuthorizePromotion(binding); err != nil || c.config.Generation() != generation {
		_ = resource.Close()
		_ = connected.Close()
		return exitselector.ConnectResult{Outcome: exitselector.OutcomeAuthFailed}, errors.Join(err, ErrGenerationStale)
	}
	renewalConfig := tunnellease.DefaultRenewerConfig(maintenance, lease)
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
	connection := newRuntimeConnection(
		connected, attempt.ConnectionStateChanges(), resource, renewer,
		c.config.Logger, candidate.PeerID,
		func() error {
			if c.config.Generation() != generation {
				return ErrGenerationStale
			}
			return c.config.Bindings.AuthorizePromotion(binding)
		},
		exitselector.ConnectionDiagnostics{
			NetworkGeneration: generation.ID(), SessionGeneration: uint64(sessionGeneration),
			TunnelLeaseID: lease.ID.String(), TunnelLeaseValidUntil: lease.ValidUntil,
			AWGKeyEpoch: binding.Binding.KeyEpoch, SelectedEndpoint: endpoint.Address,
		},
	)
	connection.promotionTimeout = c.config.RequestTimeout
	maintenance.observeRotation(connection.updateAWGKeyEpoch)
	return exitselector.ConnectResult{Outcome: exitselector.OutcomeConnected, Connection: connection}, nil
}

func m12CandidateEndpoints(candidates []icesession.Candidate) []string {
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, netip.AddrPortFrom(candidate.Address, candidate.Port).String())
	}
	return result
}

func m12ControlAddress(values [][]byte, port uint32, lease tunnellease.Lease) (string, []netip.Prefix, error) {
	if port == 0 || port > 65535 || len(values) == 0 || len(values) > 4 {
		return "", nil, ErrInvalidConfig
	}
	routes := make([]netip.Prefix, 0, len(values))
	addresses := make([]netip.Addr, 0, len(values))
	seen := make(map[netip.Addr]struct{}, len(values))
	for _, raw := range values {
		address, ok := netip.AddrFromSlice(raw)
		if !ok || !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.Is4In6() {
			return "", nil, ErrInvalidConfig
		}
		if _, duplicate := seen[address]; duplicate {
			continue
		}
		if address.Is4() && !lease.IPv4.IsValid() || address.Is6() && !lease.IPv6.IsValid() {
			continue
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address)
		routes = append(routes, netip.PrefixFrom(address, address.BitLen()))
	}
	if len(addresses) == 0 {
		return "", nil, ErrInvalidConfig
	}
	return net.JoinHostPort(addresses[0].String(), fmt.Sprint(port)), routes, nil
}

func m12ConnectOutcome(err error) exitselector.ConnectOutcome {
	switch {
	case errors.Is(err, sessionestablish.ErrCapacity), errors.Is(err, rendezvous.ErrBusy):
		return exitselector.OutcomeBusy
	case errors.Is(err, rendezvous.ErrRouteFailed), errors.Is(err, rendezvous.ErrStale):
		return exitselector.OutcomeRouteFailed
	case errors.Is(err, sessionestablish.ErrWrongPeer), errors.Is(err, sessionestablish.ErrSignature),
		errors.Is(err, sessionestablish.ErrConflict), errors.Is(err, sessionestablish.ErrChain):
		return exitselector.OutcomeAuthFailed
	case errors.Is(err, sessionestablish.ErrInvalid):
		return exitselector.OutcomeIncompatible
	default:
		return exitselector.OutcomeUnavailable
	}
}

var _ exitselector.Connector = (*BrokeredClientConnector)(nil)
var _ tunnellease.RenewalClient = (*m12Maintenance)(nil)
