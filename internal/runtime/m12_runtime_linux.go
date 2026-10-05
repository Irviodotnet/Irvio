// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	libp2ppeerstore "github.com/libp2p/go-libp2p/core/peerstore"

	"github.com/Irviodotnet/Irvio/internal/bootstrap"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type M12Config struct {
	Enabled            bool
	Serve              bool
	RendezvousPeers    []string
	GeoIPDatabase      string
	SessionControlPort uint16
	AllowTestAddresses bool
	Policy             rendezvous.Config
}

type m12Runtime struct {
	config    M12Config
	node      *discovery.Node
	public    *publicRole
	logger    *slog.Logger
	client    *rendezvous.Client
	discovery *rendezvous.DiscoveryServerProvider
	servers   rendezvous.RendezvousServerProvider
	forwarder *rendezvous.VerifiedForwarder
	provider  *rendezvous.CandidateProvider
	countries *rendezvous.CountryAggregator

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wake   chan struct{}

	mu             sync.Mutex
	service        *rendezvous.Service
	registry       *rendezvous.Registry
	resolver       rendezvous.CountryResolver
	registration   *rendezvous.RegistrationManager
	exitHandler    rendezvous.IncomingSignalHandler
	registrationWG sync.WaitGroup
	closed         bool
	closeOnce      sync.Once
	closeErr       error
}

func newM12Runtime(parent context.Context, node *discovery.Node, public *publicRole, config M12Config, logger *slog.Logger) (*m12Runtime, error) {
	if parent == nil || node == nil || public == nil || logger == nil || !config.Enabled {
		return nil, ErrInvalidConfig
	}
	if config.Policy.MaxRegistrations == 0 {
		config.Policy = rendezvous.DefaultConfig()
	}
	if config.AllowTestAddresses {
		config.Policy.ObservedIPPolicy = rendezvous.ObservedIPPolicy{
			AllowLoopback: true, AllowPrivate: true, AllowDocumentation: true,
		}
	}
	client, err := rendezvous.NewClient(node.Host(), config.Policy.PendingTimeout)
	if err != nil {
		return nil, err
	}
	discovered, err := rendezvous.NewDiscoveryServerProvider(node, config.Policy.MaxConcurrentDHTLookup)
	if err != nil {
		return nil, err
	}
	var servers rendezvous.RendezvousServerProvider = discovered
	var countryServers rendezvous.RendezvousServerProvider = discovered
	if len(config.RendezvousPeers) != 0 {
		configured, parseErr := bootstrap.Parse(config.RendezvousPeers)
		if parseErr != nil {
			return nil, fmt.Errorf("parse configured Rendezvous peers: %w", parseErr)
		}
		ids := make([]peer.ID, 0, len(configured))
		for _, info := range configured {
			if info.ID == node.PeerID() {
				continue
			}
			node.Host().Peerstore().AddAddrs(info.ID, info.Addrs, libp2ppeerstore.PermanentAddrTTL)
			ids = append(ids, info.ID)
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("configured Rendezvous peers contain no remote peer: %w", ErrInvalidConfig)
		}
		staticProvider := rendezvous.StaticServerProvider{Peers: ids}
		servers = rendezvous.FallbackServerProvider{
			Primary: staticProvider, Fallback: discovered,
		}
		countryServers = rendezvous.UnionServerProvider{staticProvider, discovered}
	}
	forwarder, err := rendezvous.NewVerifiedForwarder(client, 1024)
	if err != nil {
		return nil, err
	}
	provider, err := rendezvous.NewCandidateProvider(node.PeerID(), client, servers, forwarder)
	if err != nil {
		return nil, err
	}
	provider.SetLogger(logger)
	ctx, cancel := context.WithCancel(parent)
	owner := &m12Runtime{
		config: config, node: node, public: public, logger: logger,
		client: client, discovery: discovered, servers: servers, forwarder: forwarder, provider: provider,
		ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1),
	}
	countries, err := rendezvous.NewCountryAggregator(rendezvous.CountryAggregatorOptions{
		Local: node.PeerID(), Client: client, Provider: countryServers,
		LocalSource: owner.localAvailableCountries,
	})
	if err != nil {
		cancel()
		_ = discovered.Close()
		return nil, err
	}
	owner.countries = countries
	if config.Serve {
		if !public.Status().Requested {
			cancel()
			return nil, fmt.Errorf("Rendezvous service requires requested M11 public role: %w", ErrInvalidConfig)
		}
		public.setObserver(owner.notify)
		go owner.run()
		owner.notify()
	} else {
		close(owner.done)
	}
	return owner, nil
}

func (r *m12Runtime) CandidateProvider() *rendezvous.CandidateProvider { return r.provider }

func (r *m12Runtime) AvailableCountries(ctx context.Context, refresh bool) (rendezvous.AvailableCountries, error) {
	if r == nil || r.countries == nil {
		return rendezvous.AvailableCountries{}, ErrNotStarted
	}
	return r.countries.Get(ctx, refresh)
}

func (r *m12Runtime) localAvailableCountries() ([]string, bool) {
	r.mu.Lock()
	registry := r.registry
	r.mu.Unlock()
	if registry == nil {
		return nil, false
	}
	return registry.CountriesExcluding(r.node.PeerID()), true
}

func (r *m12Runtime) Probe() exitselector.CandidateProbe {
	return rendezvous.MetadataProbe{Provider: r.provider, Now: time.Now, Logger: r.logger}
}

func (r *m12Runtime) DescriptorChanged() {
	r.mu.Lock()
	registration := r.registration
	r.mu.Unlock()
	if registration != nil {
		registration.DescriptorChanged()
	}
}

func (r *m12Runtime) notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *m12Runtime) run() {
	defer close(r.done)
	interval := r.config.Policy.HeartbeatInterval / 2
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.wake:
			r.reconcileService()
		case <-ticker.C:
			r.reconcileService()
		}
	}
}

func (r *m12Runtime) reconcileService() {
	status := r.public.Status()
	verified := status.IPv4.Ready(time.Now()) || status.IPv6.Ready(time.Now())
	r.mu.Lock()
	service := r.service
	registry := r.registry
	r.mu.Unlock()
	if !verified {
		r.mu.Lock()
		armed := r.exitHandler != nil
		r.mu.Unlock()
		if armed {
			ctx, cancel := context.WithTimeout(r.ctx, r.config.Policy.PendingTimeout)
			if err := r.stopExitRegistrations(ctx); err != nil && r.ctx.Err() == nil {
				r.logger.Warn("EXIT_REGISTRATION_WITHDRAW_FAILED", "reason", err)
			}
			cancel()
			if err := r.node.SuspendExit(); err != nil && r.ctx.Err() == nil {
				r.logger.Warn("EXIT_CAPABILITY_SUSPEND_FAILED", "reason", err)
			}
		}
		if service != nil {
			r.stopService()
		}
		return
	}
	if service == nil {
		if err := r.startService(); err != nil {
			if r.ctx.Err() == nil {
				r.logger.Warn("RENDEZVOUS_SERVICE_DEGRADED", "reason", err)
			}
		} else {
			r.notify()
		}
		return
	}
	r.reconcileExit()
	ctx, cancel := context.WithTimeout(r.ctx, r.config.Policy.PendingTimeout)
	err := r.node.UpdateRendezvousCountries(ctx, registry.Countries())
	cancel()
	if err != nil && r.ctx.Err() == nil {
		r.logger.Warn("RENDEZVOUS_COUNTRY_ADVERTISE_FAILED", "reason", err)
	}
}

// ArmExit waits for Data Plane and M11-gated Rendezvous readiness; desktop activation is unchanged.
func (r *m12Runtime) ArmExit(handler rendezvous.IncomingSignalHandler) error {
	if r == nil || !r.config.Serve || handler == nil {
		return ErrInvalidConfig
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.exitHandler != nil {
		return ErrInvalidState
	}
	r.exitHandler = handler
	r.notify()
	return nil
}

func (r *m12Runtime) reconcileExit() {
	status := r.public.Status()
	if !status.IPv4.Ready(time.Now()) && !status.IPv6.Ready(time.Now()) {
		return
	}
	r.mu.Lock()
	handler, active := r.exitHandler, r.registration != nil
	r.mu.Unlock()
	if handler == nil || active {
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, r.config.Policy.PendingTimeout)
	err := r.node.ActivateExit(ctx)
	cancel()
	if err == nil {
		err = r.StartExitRegistrations(handler)
	}
	if err != nil && r.ctx.Err() == nil {
		r.logger.Warn("EXIT_PUBLICATION_DEGRADED", "reason", err)
	}
}

func (r *m12Runtime) startService() (_ error) {
	r.mu.Lock()
	if r.closed || r.service != nil {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	joinCtx, joinCancel := context.WithTimeout(r.ctx, r.config.Policy.PendingTimeout)
	joinErr := r.node.EnsureRoutingPeers(joinCtx)
	joinCancel()
	if joinErr != nil {
		return fmt.Errorf("restore DHT routing before Rendezvous publication: %w", joinErr)
	}
	addresses, proofRef, err := rendezvousPublicRoute(r.node.LocalAnnouncement(), r.node.PeerID())
	if err != nil {
		return err
	}
	var resolver rendezvous.CountryResolver = rendezvous.UnknownCountryResolver{Reason: rendezvous.ErrCountryDegraded}
	if r.config.GeoIPDatabase != "" {
		opened, openErr := rendezvous.OpenMMDB(r.config.GeoIPDatabase)
		if openErr != nil {
			r.logger.Warn("RENDEZVOUS_GEOIP_DEGRADED", "reason", openErr)
		} else {
			resolver = opened
		}
	} else {
		r.logger.Warn("RENDEZVOUS_GEOIP_DEGRADED", "reason", "no local MMDB configured")
	}
	incarnation, err := randomNonzeroUint64()
	if err != nil {
		closeResolver(resolver)
		return err
	}
	registry, err := rendezvous.NewRegistry(rendezvous.RegistryOptions{
		Config: r.config.Policy, ServerPeer: r.node.PeerID(), ServerIncarnation: incarnation,
		Signer: r.node.IdentityPrivateKey(), Resolver: resolver, ControlAddresses: addresses,
		ServerPublicProof: proofRef,
	})
	if err != nil {
		closeResolver(resolver)
		return err
	}
	r.logger.Info(
		"RENDEZVOUS_REGISTRY_CREATED",
		"server_incarnation", incarnation,
		"live_registrations", registry.Snapshot().Registrations,
	)
	sampler, err := rendezvous.NewFanoutSampler(rendezvous.FanoutSamplerOptions{
		Local: r.node.PeerID(), Client: r.client, Provider: r.discovery,
		Forwarder: r.forwarder, Concurrency: r.config.Policy.MaxOutgoingSample,
	})
	if err != nil {
		registry.Close()
		closeResolver(resolver)
		return err
	}
	service, err := rendezvous.NewService(rendezvous.ServiceOptions{
		Host: r.node.Host(), Registry: registry, Forwarder: r.forwarder, RemoteSampler: sampler,
		AuthorizeSample: r.authorizeSample,
		Logger:          r.logger,
		MaxHandlers:     r.config.Policy.MaxPendingSignaling + r.config.Policy.MaxOutgoingSample,
		StreamTimeout:   r.config.Policy.PendingTimeout,
	})
	if err != nil {
		registry.Close()
		closeResolver(resolver)
		return err
	}
	ctx, cancel := context.WithTimeout(r.ctx, r.config.Policy.PendingTimeout)
	err = r.node.ActivateRendezvousCapability(ctx, registry.Countries())
	cancel()
	if err != nil {
		_ = service.Close()
		closeResolver(resolver)
		return err
	}
	r.mu.Lock()
	if r.closed || r.service != nil {
		r.mu.Unlock()
		_ = service.Close()
		closeResolver(resolver)
		return ErrGenerationStale
	}
	r.service, r.registry, r.resolver = service, registry, resolver
	r.mu.Unlock()
	r.logger.Info(
		"RENDEZVOUS_SERVICE_ACTIVE",
		"server_incarnation", incarnation,
		"live_registrations", registry.Snapshot().Registrations,
		"geoip_degraded", resolver.Snapshot().Degraded,
	)
	return nil
}

func (r *m12Runtime) authorizeSample(ctx context.Context, remote peer.ID) error {
	if ctx == nil || remote == "" || remote == r.node.PeerID() {
		return rendezvous.ErrUnauthorized
	}
	if err := r.node.AuthenticatePeer(ctx, remote); err != nil {
		return err
	}
	value, err := r.node.Directory().Peer(ctx, remote.String())
	if err != nil || value.ExpiresAt.Before(time.Now().UTC()) ||
		value.Capabilities&protocol.CapRendezvous == 0 ||
		value.Capabilities&(protocol.CapPublicControlV4|protocol.CapPublicControlV6) == 0 {
		return rendezvous.ErrUnauthorized
	}
	return nil
}

func (r *m12Runtime) stopService() {
	r.mu.Lock()
	service, resolver := r.service, r.resolver
	r.service, r.registry, r.resolver = nil, nil, nil
	r.mu.Unlock()
	if service != nil {
		_ = service.Close()
	}
	closeResolver(resolver)
	if err := r.node.DeactivateRendezvousCapability(); err != nil && r.ctx.Err() == nil {
		r.logger.Warn("RENDEZVOUS_CAPABILITY_DEACTIVATE_FAILED", "reason", err)
	}
}

func (r *m12Runtime) StartExitRegistrations(handler rendezvous.IncomingSignalHandler) error {
	if handler == nil {
		return ErrInvalidConfig
	}
	manager, err := rendezvous.NewRegistrationManager(rendezvous.RegistrationManagerOptions{
		Host: r.node.Host(), Servers: r.servers, SignedDescriptor: r.node.LocalSignedAnnouncement,
		Handler: handler, Config: r.config.Policy, OperationTimeout: r.config.Policy.PendingTimeout,
		Logger: r.logger,
	})
	if err != nil {
		return err
	}
	if err := manager.Start(r.ctx); err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed || r.registration != nil {
		r.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), r.config.Policy.PendingTimeout)
		_ = manager.Close(ctx)
		cancel()
		return ErrGenerationStale
	}
	r.registration = manager
	r.mu.Unlock()
	r.registrationWG.Add(1)
	go func() {
		defer r.registrationWG.Done()
		for snapshot := range manager.Events() {
			servers := make([]string, 0, len(snapshot.Servers))
			for _, server := range snapshot.Servers {
				servers = append(servers, server.String())
			}
			r.logger.Info("RENDEZVOUS_REGISTRATIONS", "active", snapshot.Active, "servers", servers, "closed", snapshot.Closed)
		}
	}()
	return nil
}

func (r *m12Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.public.setObserver(nil)
		r.cancel()
		<-r.done
		r.mu.Lock()
		r.closed = true
		registration := r.registration
		r.registration = nil
		r.mu.Unlock()
		if registration != nil {
			r.closeErr = errors.Join(r.closeErr, registration.Close(ctx))
		}
		r.registrationWG.Wait()
		r.stopService()
		r.closeErr = errors.Join(r.closeErr, r.discovery.Close())
	})
	return r.closeErr
}

// stopExitRegistrations withdraws Exit availability but preserves discovery needed for upstream teardown.
func (r *m12Runtime) stopExitRegistrations(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	registration := r.registration
	r.registration = nil
	r.mu.Unlock()
	if registration == nil {
		return nil
	}
	err := registration.Close(ctx)
	r.registrationWG.Wait()
	return err
}

func rendezvousPublicRoute(announcement *irviov1.NodeAnnouncement, local peer.ID) ([]string, []byte, error) {
	if announcement == nil || local == "" || announcement.Capabilities&(protocol.CapPublicControlV4|protocol.CapPublicControlV6) == 0 {
		return nil, nil, ErrInvalidConfig
	}
	result := make([]string, 0, len(announcement.PublicControlEndpoints))
	for _, endpoint := range announcement.PublicControlEndpoints {
		address, ok := netip.AddrFromSlice(endpoint.GetObservedIp())
		if !ok || endpoint.GetExternalControlPort() == 0 || endpoint.GetExternalControlPort() > 65535 {
			continue
		}
		family := "ip6"
		if address.Is4() {
			family = "ip4"
		}
		result = append(result, fmt.Sprintf("/%s/%s/tcp/%d/p2p/%s", family, address, endpoint.ExternalControlPort, local))
	}
	if len(result) == 0 {
		return nil, nil, ErrInvalidConfig
	}
	return result, nil, nil
}

func randomNonzeroUint64() (uint64, error) {
	var raw [8]byte
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
			return 0, err
		}
		if value := binary.BigEndian.Uint64(raw[:]); value != 0 {
			return value, nil
		}
	}
	return 0, errors.New("generated zero Rendezvous server incarnation twice")
}

func closeResolver(resolver rendezvous.CountryResolver) {
	if closer, ok := resolver.(io.Closer); ok {
		_ = closer.Close()
	}
}
