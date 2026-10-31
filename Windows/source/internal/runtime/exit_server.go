// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/exitaccess"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/sessioncontrol"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/tunnelsignal"
)

// exitServerPlane keeps protocol authority independent from the operating system's packet backend.
type exitServerPlane interface {
	tunnellease.ResourceActivator
	tunnellease.ResourceRenewer
	tunnellease.ResourceRevoker
	SessionInstaller
	NetworkReconciler
	io.Closer
	registerPending(*pendingExitSession) error
	expirePending(time.Time)
	discardPending(tunnellease.SessionToken, peer.ID) error
	rotationTarget() (tunnellease.Lease, error)
	applyLocalAWGRotation(context.Context, tunnellease.Lease, awg.Key) error
}

type exitPlaneFactory func(context.Context, ExitServerConfig) (exitServerPlane, error)

type ExitServerConfig struct {
	Host               host.Host
	LocalPeer          peer.ID
	PrivateKey         awg.Key
	Transports         TransportLifecycle
	Bindings           BindingResolver
	Authorizer         icesignal.Authorizer
	PrivateAccess      *privateaccess.Policy
	Validation         icesession.ValidationConfig
	SignalConfig       icesignal.Config
	TUNName            string
	TUNAddresses       []netip.Prefix
	WANName            string
	IPv4Pool           netip.Prefix
	IPv6Pool           netip.Prefix
	Management         []netip.Prefix
	Bypass             []netip.Prefix
	LeaseTTL           time.Duration
	LeaseSweep         time.Duration
	RequestTimeout     time.Duration
	Obfuscation        awg.ObfuscationConfig
	MaxPending         int
	Logger             *slog.Logger
	M12Enabled         bool
	IdentityPrivate    crypto.PrivKey
	SignedDescriptor   func() []byte
	SessionControlPort uint16
	Platform           ClientDataPlanePlatform
}

type ExitServer struct {
	config       ExitServerConfig
	plane        exitServerPlane
	lifecycle    *tunnellease.Lifecycle
	iceService   *icesignal.Service
	leaseService *tunnelsignal.Service
	m12          *m12ExitServices

	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	next       atomic.Uint64
	workers    sync.WaitGroup
	slots      chan struct{}
	rotationMu sync.Mutex
	rotation   *exitAWGRotation
	closeMu    sync.Mutex
}

type exitAWGRotation struct {
	privateKey awg.Key
	publicKey  awg.Key
	epoch      uint64
	signed     []byte
	digest     [sha256.Size]byte
	client     peer.ID
	leaseID    tunnellease.ID
	session    tunnellease.SessionToken
	done       chan error
	err        error
	committed  bool
}

type pendingExitSession struct {
	token     tunnellease.SessionToken
	remote    peer.ID
	key       awg.Key
	keyEpoch  uint64
	endpoint  awg.SelectedEndpoint
	ice       *iceconnect.ConnectedSession
	attempt   *iceconnect.Attempt
	createdAt time.Time
}

// A non-nil server returned with an error owns unfinished cleanup and must only be closed.
func newExitServer(ctx context.Context, config ExitServerConfig, factory exitPlaneFactory) (result *ExitServer, resultErr error) {
	if config.PrivateAccess != nil && !config.M12Enabled {
		return nil, ErrInvalidConfig
	}
	if factory == nil || ctx == nil || config.Host == nil || config.LocalPeer == "" || config.Host.ID() != config.LocalPeer ||
		config.PrivateKey == (awg.Key{}) || config.Transports == nil || config.Bindings.Directory == nil ||
		config.Bindings.Registry == nil || config.Authorizer == nil || config.WANName == "" ||
		len(config.TUNAddresses) == 0 || !config.IPv4Pool.IsValid() && !config.IPv6Pool.IsValid() {
		return nil, ErrInvalidConfig
	}
	if config.TUNName == "" {
		config.TUNName = tunnel.DefaultName
	}
	if config.LeaseTTL == 0 {
		config.LeaseTTL = tunnellease.DefaultTTL
	}
	if config.LeaseSweep == 0 {
		config.LeaseSweep = time.Second
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 5 * time.Second
	}
	if config.MaxPending == 0 {
		config.MaxPending = tunnellease.DefaultMaxActive
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Validation == (icesession.ValidationConfig{}) {
		config.Validation = icesession.DefaultValidationConfig()
	}
	if err := config.Validation.Validate(); err != nil {
		return nil, err
	}
	config.SignalConfig.Validation = config.Validation
	config.SignalConfig.Logger = config.Logger
	serverContext, cancel := context.WithCancel(context.Background())
	server := &ExitServer{config: config, ctx: serverContext, cancel: cancel, slots: make(chan struct{}, config.MaxPending)}
	defer func() {
		if resultErr != nil {
			cancel()
			cleanupContext, stop := context.WithTimeout(context.Background(), config.RequestTimeout)
			defer stop()
			if cleanupErr := server.closeResources(cleanupContext); cleanupErr != nil {
				result, resultErr = server, errors.Join(resultErr, cleanupErr)
			}
		}
	}()
	plane, err := factory(ctx, config)
	server.plane = plane
	if err != nil {
		return nil, err
	}
	if plane == nil {
		return nil, ErrInvalidConfig
	}

	allocatorConfig := tunnellease.DefaultConfig(config.LocalPeer)
	allocatorConfig.IPv4Pool, allocatorConfig.IPv6Pool = config.IPv4Pool, config.IPv6Pool
	allocatorConfig.TTL = config.LeaseTTL
	allocatorConfig.MaxActive = config.MaxPending
	allocatorConfig.Reserved = exitReservedAddresses(config.TUNAddresses, config.IPv4Pool, config.IPv6Pool)
	allocator, err := tunnellease.New(allocatorConfig)
	if err != nil {
		return nil, err
	}
	lifecycleConfig := tunnellease.DefaultLifecycleConfig(allocator, plane)
	lifecycleConfig.Activator = plane
	lifecycleConfig.Renewer = plane
	lifecycleConfig.SweepInterval = config.LeaseSweep
	lifecycleConfig.RevocationTimeout = config.RequestTimeout
	lifecycleConfig.MaxPending = config.MaxPending
	if config.PrivateAccess != nil {
		lifecycleConfig.Admission = config.PrivateAccess
		lifecycleConfig.RequireSessionToken = true
	}
	server.lifecycle, err = tunnellease.NewLifecycle(lifecycleConfig)
	if err != nil {
		_ = allocator.Close()
		return nil, err
	}
	server.leaseService, err = tunnelsignal.NewLifecycleService(config.Host, server.lifecycle, config.RequestTimeout)
	if err != nil {
		return nil, err
	}
	if config.M12Enabled {
		server.m12, err = newM12ExitServices(ctx, server)
		if err != nil {
			return nil, err
		}
	}
	legacyAuthorizer := config.Authorizer
	if config.PrivateAccess != nil {
		legacyAuthorizer = icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return exitaccess.ErrDenied })
	}
	server.iceService, err = icesignal.NewService(
		config.Host, config.LocalPeer,
		icesignal.SessionFactoryFunc(func(sessionContext context.Context, _ peer.ID, _ icesession.Description) (*icesession.PreparedSession, error) {
			generation := config.Transports.Current()
			if generation == nil {
				return nil, fmt.Errorf("Exit transport unavailable")
			}
			return generation.NewPreparedSession(sessionContext)
		}),
		legacyAuthorizer, config.SignalConfig,
	)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, cancel)
	server.done = make(chan struct{})
	server.workers.Add(1)
	go func() {
		defer stop()
		defer server.workers.Done()
		server.sweepPending()
	}()
	go server.run(server.iceService)
	return server, nil
}

func (s *ExitServer) run(service *icesignal.Service) {
	defer close(s.done)
	defer s.workers.Wait()
	for {
		accepted, err := service.NextAccepted(s.ctx)
		if err != nil {
			if s.ctx.Err() != nil || errors.Is(err, icesignal.ErrClosed) {
				return
			}
			s.config.Logger.Warn("HYDRA_RUNTIME_ICE_ACCEPT", "reason", err)
			continue
		}
		s.rotationMu.Lock()
		if s.rotation != nil && !s.rotation.committed {
			s.rotationMu.Unlock()
			_ = accepted.Local.Close()
			continue
		}
		select {
		case s.slots <- struct{}{}:
			s.workers.Add(1)
			go func() {
				defer s.workers.Done()
				defer func() { <-s.slots }()
				s.prepareAccepted(accepted)
			}()
		default:
			_ = accepted.Local.Close()
		}
		s.rotationMu.Unlock()
	}
}

func (s *ExitServer) sweepPending() {
	interval := s.config.RequestTimeout
	if interval > time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			s.plane.expirePending(now.UTC().Add(-2 * s.config.RequestTimeout))
		}
	}
}

func (s *ExitServer) prepareAccepted(accepted icesignal.AcceptedSession) {
	attempt, err := iceconnect.New(accepted.Local, iceconnect.Binding{
		SessionID: accepted.SessionID, Initiator: accepted.Initiator, Responder: accepted.Responder,
		LocalPeer: s.config.LocalPeer, RemotePeer: accepted.RemotePeer, Purpose: accepted.Purpose,
		LocalRole: accepted.LocalRole, RemoteRole: accepted.RemoteRole,
	}, iceconnect.RemoteState{
		SessionID: accepted.SessionID, PeerID: accepted.RemotePeer,
		Role: accepted.RemoteRole, Description: accepted.RemoteDescription,
	}, iceConnectConfig(s.config.Validation))
	if err != nil {
		_ = accepted.Local.Close()
		return
	}
	if err := attempt.LoadRemoteCandidates(s.ctx); err != nil {
		_ = attempt.Close()
		return
	}
	connected, err := attempt.Connect(s.ctx)
	if err != nil {
		_ = attempt.Close()
		return
	}
	binding, err := s.config.Bindings.Resolve(s.ctx, accepted.RemotePeer)
	if err != nil {
		_ = connected.Close()
		return
	}
	sessionGeneration := s.next.Add(1)
	endpoint, err := awg.SelectedEndpointFromICE(
		connected.Result(), networkstate.Generation(accepted.Local.TransportGeneration()),
		connsupervisor.SessionGeneration(sessionGeneration), accepted.RemotePeer,
	)
	if err != nil {
		_ = connected.Close()
		return
	}
	if err := s.config.Bindings.AuthorizePromotion(binding); err != nil {
		_ = connected.Close()
		return
	}
	var token tunnellease.SessionToken
	copy(token[:], accepted.SessionID[:])
	pending := &pendingExitSession{
		token: token, remote: accepted.RemotePeer, key: binding.Key,
		keyEpoch: binding.Binding.KeyEpoch, endpoint: endpoint,
		ice: connected, attempt: attempt, createdAt: time.Now().UTC(),
	}
	if err := s.plane.registerPending(pending); err != nil {
		_ = connected.Close()
	}
}

func (s *ExitServer) reserveAWGRotation() (*exitAWGRotation, error) {
	if s == nil || s.plane == nil || s.m12 == nil {
		return nil, ErrInvalidConfig
	}
	target, err := s.plane.rotationTarget()
	if err != nil {
		return nil, err
	}
	rotation := &exitAWGRotation{
		client: target.ClientPeer, leaseID: target.ID, session: target.Session,
		done: make(chan error, 1),
	}
	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()
	if s.rotation != nil && !s.rotation.committed {
		return nil, tunnellease.ErrExhausted
	}
	if len(s.slots) != 0 {
		return nil, tunnellease.ErrExhausted
	}
	// Revalidate under the reservation lock so revoked or superseded leases cannot inherit rotation.
	current, err := s.plane.rotationTarget()
	if err != nil || current.ClientPeer != rotation.client || current.ID != rotation.leaseID || current.Session != rotation.session {
		return nil, errors.Join(err, tunnellease.ErrStaleGeneration)
	}
	s.rotation = rotation
	return rotation, nil
}

func (s *ExitServer) cancelAWGRotation(rotation *exitAWGRotation) {
	if s == nil || rotation == nil {
		return
	}
	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()
	if s.rotation == rotation && rotation.epoch == 0 {
		s.rotation = nil
	}
}

func (s *ExitServer) stageAWGRotation(rotation *exitAWGRotation, privateKey, publicKey awg.Key, epoch uint64, signed []byte) (<-chan error, error) {
	if s == nil || rotation == nil || privateKey == (awg.Key{}) || publicKey == (awg.Key{}) || epoch == 0 || len(signed) == 0 || len(signed) > sessioncontrol.MaxSignedAWGBinding {
		return nil, ErrInvalidConfig
	}
	verified, err := awgbinding.Verify(signed, s.config.LocalPeer, time.Now().UTC())
	if err != nil || verified.KeyEpoch != epoch || verified.PublicKey != publicKey {
		return nil, errors.Join(err, ErrInvalidConfig)
	}
	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()
	if s.rotation != rotation || rotation.epoch != 0 {
		return nil, tunnellease.ErrStaleGeneration
	}
	target, err := s.plane.rotationTarget()
	if err != nil || target.ClientPeer != rotation.client || target.ID != rotation.leaseID || target.Session != rotation.session {
		return nil, errors.Join(err, tunnellease.ErrStaleGeneration)
	}
	rotation.privateKey, rotation.publicKey, rotation.epoch = privateKey, publicKey, epoch
	rotation.signed, rotation.digest = append([]byte(nil), signed...), sha256.Sum256(signed)
	return rotation.done, nil
}

func (s *ExitServer) rotationPending() bool {
	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()
	return s.rotation != nil && !s.rotation.committed
}

func (s *ExitServer) rotationProposal(client peer.ID, id tunnellease.ID, session tunnellease.SessionToken) (sessioncontrol.RotationProposal, error) {
	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()
	value := s.rotation
	if value == nil || value.committed || value.epoch == 0 || value.err != nil {
		return sessioncontrol.RotationProposal{}, sessioncontrol.ErrNoRotation
	}
	if value.client != client || value.leaseID != id || value.session != session {
		return sessioncontrol.RotationProposal{}, tunnellease.ErrWrongPeer
	}
	return sessioncontrol.RotationProposal{SignedBinding: append([]byte(nil), value.signed...), KeyEpoch: value.epoch}, nil
}

func (s *ExitServer) validateAWGRotation(client peer.ID, id tunnellease.ID, revision uint64, session tunnellease.SessionToken, epoch uint64, digest [sha256.Size]byte) error {
	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()
	value := s.rotation
	if value == nil || value.committed || value.err != nil || value.client != client || value.leaseID != id ||
		value.session != session || value.epoch != epoch || value.digest != digest {
		return tunnellease.ErrStaleGeneration
	}
	_, err := s.lifecycle.LookupSession(client, id, revision, session)
	return err
}

func (s *ExitServer) commitAWGRotation(ctx context.Context, client peer.ID, id tunnellease.ID, revision uint64, session tunnellease.SessionToken, epoch uint64, digest [sha256.Size]byte) (tunnellease.Lease, error) {
	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()
	value := s.rotation
	if value == nil || value.client != client || value.leaseID != id || value.session != session || value.epoch != epoch || value.digest != digest {
		return tunnellease.Lease{}, tunnellease.ErrStaleGeneration
	}
	if value.committed {
		if value.err != nil {
			return tunnellease.Lease{}, value.err
		}
		return s.lifecycle.LookupSession(client, id, revision+1, session)
	}
	if value.err != nil {
		return tunnellease.Lease{}, value.err
	}
	s.config.Logger.Info("AWG_DIRECT_ROTATION_COMMIT_STARTED", "client_peer_id", client, "key_epoch", epoch)
	// Serialize bounded device updates without holding the Data Plane lock while awaiting rotation ownership.
	renewed, err := s.lifecycle.MutateAndRenewSession(ctx, client, id, revision, session,
		func(mutationContext context.Context, target tunnellease.Lease) error {
			return s.plane.applyLocalAWGRotation(mutationContext, target, value.privateKey)
		})
	if s.rotation != value {
		return tunnellease.Lease{}, tunnellease.ErrStaleGeneration
	}
	if err != nil {
		s.config.Logger.Warn("AWG_DIRECT_ROTATION_COMMIT_FAILED", "client_peer_id", client, "key_epoch", epoch, "reason", err)
		value.err = err
		value.done <- err
		close(value.done)
		return tunnellease.Lease{}, err
	}
	value.committed = true
	s.config.PrivateKey = value.privateKey
	value.done <- nil
	close(value.done)
	s.config.Logger.Info("AWG_DIRECT_ROTATION_COMMITTED", "client_peer_id", client, "key_epoch", epoch)
	return renewed, nil
}

func (s *ExitServer) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalidConfig
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.cancel()
	return s.closeResources(ctx)
}

// stopAdmissions fences new leases before upstream teardown; Close revokes existing leases later.
func (s *ExitServer) stopAdmissions() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

func (s *ExitServer) closeResources(ctx context.Context) error {
	if s.m12 != nil {
		if err := s.m12.Close(); err != nil {
			return err
		}
		s.m12 = nil
	}
	if s.iceService != nil {
		if err := s.iceService.Close(); err != nil {
			return err
		}
		s.iceService = nil
	}
	if s.leaseService != nil {
		if err := s.leaseService.Close(); err != nil {
			return err
		}
		s.leaseService = nil
	}
	if s.done != nil {
		select {
		case <-s.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.lifecycle != nil {
		if err := s.lifecycle.Close(ctx); err != nil {
			return err
		}
		s.lifecycle = nil
	}
	if s.plane != nil {
		if err := s.plane.Close(); err != nil {
			return err
		}
		s.plane = nil
	}
	return nil
}

func exitReservedAddresses(addresses []netip.Prefix, pool4, pool6 netip.Prefix) []netip.Addr {
	var result []netip.Addr
	for _, prefix := range addresses {
		address := prefix.Addr()
		if pool4.IsValid() && pool4.Contains(address) || pool6.IsValid() && pool6.Contains(address) {
			result = append(result, address)
		}
	}
	return result
}

// ClientInstaller lends the Exit-owned Data Plane without transferring lifecycle ownership.
func (s *ExitServer) ClientInstaller() SessionInstaller {
	if s == nil {
		return nil
	}
	return s.plane
}

// ReconcileNetwork refreshes Exit egress without waiting for downstream reconnection.
func (s *ExitServer) ReconcileNetwork(ctx context.Context, state networkstate.State) error {
	if s == nil || s.plane == nil {
		return ErrClosed
	}
	return s.plane.ReconcileNetwork(ctx, state)
}
