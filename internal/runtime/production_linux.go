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
	"net/netip"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/tunnelsignal"
	"github.com/Irviodotnet/Irvio/internal/underlay"
)

func DefaultProductionObfuscation() awg.ObfuscationConfig {
	return awg.ObfuscationConfig{
		JunkPacketCount: 1, JunkMin: 24, JunkMax: 24,
		InitPadding: 16, ResponsePadding: 16, CookiePadding: 8,
		InitHeader: 101, ResponseHeader: 202, CookieHeader: 303, TransportHeader: 404,
		SignaturePackets: [5]string{"<b 0x4859445241>"},
	}
}

type ProductionConfig struct {
	UDPPort             int
	RequireFixedUDPPort bool
	STUNIPv4            netip.AddrPort
	STUNIPv4Pool        []netip.AddrPort
	STUNIPv6            netip.AddrPort
	TUNName             string
	TunnelDNSServers    []netip.Addr
	Bypass              []netip.Prefix
	Obfuscation         awg.ObfuscationConfig
	ConnectCountry      exitselector.CountryIntent

	ExitWANName      string
	ExitTUNAddresses []netip.Prefix
	ExitIPv4Pool     netip.Prefix
	ExitIPv6Pool     netip.Prefix
	ExitManagement   []netip.Prefix
	LeaseTTL         time.Duration
	LeaseSweep       time.Duration
	LeaseRenewLead   time.Duration
	LeaseRenewRetry  time.Duration
}

type runtimeCore struct {
	values []io.Closer
	mu     sync.Mutex
}

func (c *runtimeCore) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var result error
	for index := len(c.values) - 1; index >= 0; index-- {
		if c.values[index] == nil {
			continue
		}
		if err := c.values[index].Close(); err != nil {
			result = errors.Join(result, err)
		} else {
			c.values[index] = nil
		}
	}
	return result
}

type exitTransportPump struct {
	source   NetworkSource
	owner    TransportLifecycle
	logger   *slog.Logger
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
	stopOnce sync.Once
	err      error
}

func startExitTransportPump(ctx context.Context, source NetworkSource, owner TransportLifecycle, logger *slog.Logger, timeout time.Duration, onNetworkChange func(networkstate.State) error) (*exitTransportPump, error) {
	if ctx == nil || source == nil || owner == nil || logger == nil || timeout <= 0 {
		return nil, ErrInvalidConfig
	}
	initial, cancelInitial := context.WithTimeout(ctx, timeout)
	err := owner.Activate(initial, source.Current())
	cancelInitial()
	if err != nil {
		return nil, err
	}
	pumpContext, cancel := context.WithCancel(context.Background())
	pump := &exitTransportPump{source: source, owner: owner, logger: logger, ctx: pumpContext, cancel: cancel, done: make(chan struct{})}
	stop := context.AfterFunc(ctx, cancel)
	go func() {
		defer stop()
		pump.run(timeout, onNetworkChange)
	}()
	return pump, nil
}

func (p *exitTransportPump) run(timeout time.Duration, onNetworkChange func(networkstate.State) error) {
	defer close(p.done)
	changes := p.source.Changes()
	failures := p.source.Errors()
	for {
		select {
		case <-p.ctx.Done():
			return
		case state, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			if onNetworkChange != nil {
				if err := onNetworkChange(state); err != nil {
					if !errors.Is(err, context.Canceled) {
						p.logger.Error("HYDRAD_EXIT_NETWORK_RECONCILE_FAILED", "network_generation", state.Generation, "reason", err)
					}
					continue
				}
			}
			ctx, cancel := context.WithTimeout(p.ctx, timeout)
			err := p.owner.Activate(ctx, state)
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				p.logger.Error("HYDRAD_TRANSPORT_MIGRATION_FAILED", "network_generation", state.Generation, "reason", err)
			}
		case err, ok := <-failures:
			if !ok {
				failures = nil
				continue
			}
			if err != nil {
				p.logger.Error("HYDRAD_NETWORK_MONITOR_FAILED", "reason", err)
			}
		}
	}
}

func (p *exitTransportPump) Close() error {
	if p == nil {
		return nil
	}
	p.once.Do(func() {
		p.stop()
		p.err = p.owner.Close()
	})
	return p.err
}

func (p *exitTransportPump) stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		p.cancel()
		<-p.done
	})
}

func newTransportOwner(config ProductionConfig, logger *slog.Logger) (*TransportOwner, error) {
	builderConfig := transportgen.DefaultConfig(transportgen.PreferredBindPlanner{})
	builderConfig.Port = config.UDPPort
	builderConfig.SocketMark = linuxroute.IrvioBypassMark
	builderConfig.Logger = logger
	builderConfig.STUNServers = make(map[socketmux.AddressFamily]netip.AddrPort, 2)
	if config.STUNIPv4.IsValid() {
		builderConfig.STUNServers[socketmux.IPv4] = config.STUNIPv4
	}
	if len(config.STUNIPv4Pool) != 0 {
		builderConfig.STUNServerPools = map[socketmux.AddressFamily][]netip.AddrPort{
			socketmux.IPv4: append([]netip.AddrPort(nil), config.STUNIPv4Pool...),
		}
	}
	if config.STUNIPv6.IsValid() {
		builderConfig.STUNServers[socketmux.IPv6] = config.STUNIPv6
	}
	builder, err := transportgen.NewBuilder(builderConfig)
	if err != nil {
		return nil, err
	}
	owner, err := NewTransportOwner(builder)
	if err != nil {
		return nil, err
	}
	if config.RequireFixedUDPPort && config.UDPPort == 0 {
		return nil, fmt.Errorf("fixed server UDP port must be nonzero: %w", ErrInvalidConfig)
	}
	owner.requireFixedPort = config.RequireFixedUDPPort
	return owner, nil
}

type productionClient struct {
	connector exitselector.Connector
	plane     io.Closer
	dns       io.Closer
	signal    *icesignal.Client
	network   NetworkReconciler
}

func wrapProductionTunnelDNS(installer SessionInstaller, network NetworkReconciler, config ProductionConfig) (SessionInstaller, NetworkReconciler, io.Closer, error) {
	if len(config.TunnelDNSServers) == 0 {
		return installer, network, nil, nil
	}
	tunName := config.TUNName
	if tunName == "" {
		tunName = tunnel.DefaultName
	}
	manager, err := newTunnelDNSManager(tunName, config.TunnelDNSServers, nil, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	wrapper, err := newTunnelDNSInstaller(installer, network, manager)
	if err != nil {
		_ = manager.Close()
		return nil, nil, nil, err
	}
	return wrapper, wrapper, manager, nil
}

func newProductionClient(
	node *discovery.Node,
	identityPrivate awg.Key,
	transports TransportLifecycle,
	bindings BindingResolver,
	config ProductionConfig,
	timeout time.Duration,
	sharedInstaller SessionInstaller,
) (*productionClient, error) {
	renewalLead, renewalRetry, err := leaseRenewalTimings(config.LeaseTTL, config.LeaseRenewLead, config.LeaseRenewRetry)
	if err != nil {
		return nil, err
	}
	signalConfig := icesignal.DefaultConfig()
	signalClient, err := icesignal.NewClient(node.Host(), signalConfig)
	if err != nil {
		return nil, err
	}
	leaseClient, err := tunnelsignal.NewClient(node.Host(), timeout)
	if err != nil {
		_ = signalClient.Close()
		return nil, err
	}
	var installer SessionInstaller
	var plane io.Closer
	var dns io.Closer
	var network NetworkReconciler
	if sharedInstaller != nil {
		installer = sharedInstaller
		network, _ = sharedInstaller.(NetworkReconciler)
	} else {
		owned, err := NewClientDataPlane(ClientDataPlaneConfig{
			LocalPeer: node.PeerID().String(), PrivateKey: identityPrivate,
			TUNName: config.TUNName, Obfuscation: config.Obfuscation,
			OuterMark: linuxroute.IrvioBypassMark, Bypass: config.Bypass,
			Transports: transports,
		})
		if err != nil {
			_ = signalClient.Close()
			return nil, err
		}
		installer, plane, network = owned, owned, owned
	}
	installer, network, dns, err = wrapProductionTunnelDNS(installer, network, config)
	if err != nil {
		_ = closeOptional(plane)
		_ = signalClient.Close()
		return nil, err
	}
	connector, err := NewClientConnector(ClientConnectorConfig{
		LocalPeer: node.PeerID(), Generation: transports.Current,
		Signaling: signalClient, Leases: leaseClient, Bindings: bindings,
		Installer: installer, Validation: icesession.DefaultValidationConfig(),
		RenewalLead: renewalLead, RenewalRetry: renewalRetry, RequestTimeout: timeout,
	})
	if err != nil {
		_ = closeOptional(dns)
		_ = closeOptional(plane)
		_ = signalClient.Close()
		return nil, err
	}
	return &productionClient{connector: connector, plane: plane, dns: dns, signal: signalClient, network: network}, nil
}

func leaseRenewalTimings(ttl, lead, retry time.Duration) (time.Duration, time.Duration, error) {
	if ttl <= 0 {
		return 0, 0, ErrInvalidConfig
	}
	if lead == 0 {
		lead = 20 * time.Second
		if scaled := ttl / 3; lead > scaled {
			lead = scaled
		}
	}
	if retry == 0 {
		retry = 2 * time.Second
		if scaled := ttl / 10; retry > scaled {
			retry = scaled
		}
	}
	if lead <= 0 || lead >= ttl || retry <= 0 || retry >= ttl {
		return 0, 0, ErrInvalidConfig
	}
	return lead, retry, nil
}

func (c *productionClient) core() io.Closer {
	values := make([]io.Closer, 0, 3)
	if c.signal != nil {
		values = append(values, c.signal)
	}
	if c.plane != nil {
		values = append(values, c.plane)
	}
	if c.dns != nil {
		values = append(values, c.dns)
	}
	return &runtimeCore{values: values}
}

func newProductionBrokeredClient(
	node *discovery.Node,
	irvioPrivate crypto.PrivKey,
	awgPrivate awg.Key,
	awgEpoch uint64,
	transports TransportLifecycle,
	bindings BindingResolver,
	config ProductionConfig,
	timeout time.Duration,
	sharedInstaller SessionInstaller,
	m12 *m12Runtime,
) (*productionClient, error) {
	if m12 == nil {
		return nil, ErrInvalidConfig
	}
	renewalLead, renewalRetry, err := leaseRenewalTimings(config.LeaseTTL, config.LeaseRenewLead, config.LeaseRenewRetry)
	if err != nil {
		return nil, err
	}
	var installer SessionInstaller
	var plane io.Closer
	var dns io.Closer
	var network NetworkReconciler
	if sharedInstaller != nil {
		installer = sharedInstaller
		network, _ = sharedInstaller.(NetworkReconciler)
	} else {
		owned, err := NewClientDataPlane(ClientDataPlaneConfig{
			LocalPeer: node.PeerID().String(), PrivateKey: awgPrivate,
			TUNName: config.TUNName, Obfuscation: config.Obfuscation,
			OuterMark: linuxroute.IrvioBypassMark, Bypass: config.Bypass,
			Transports: transports,
		})
		if err != nil {
			return nil, err
		}
		installer, plane, network = owned, owned, owned
	}
	installer, network, dns, err = wrapProductionTunnelDNS(installer, network, config)
	if err != nil {
		_ = closeOptional(plane)
		return nil, err
	}
	connector, err := NewBrokeredClientConnector(BrokeredClientConnectorConfig{
		LocalPeer: node.PeerID(), IdentityPrivate: irvioPrivate, LocalAWGKeyEpoch: awgEpoch,
		SignedDescriptor: node.LocalSignedAnnouncement, Generation: transports.Current,
		Provider: m12.CandidateProvider(), Bindings: bindings, Installer: installer,
		Validation: icesession.DefaultValidationConfig(), SignalConfig: icesignal.DefaultConfig(),
		Obfuscation: config.Obfuscation, RenewalLead: renewalLead, RenewalRetry: renewalRetry,
		RequestTimeout: timeout, TUNName: config.TUNName,
	})
	if err != nil {
		_ = closeOptional(dns)
		_ = closeOptional(plane)
		return nil, err
	}
	return &productionClient{connector: connector, plane: plane, dns: dns, network: network}, nil
}

func newProductionExit(
	ctx context.Context,
	node *discovery.Node,
	identityPrivate awg.Key,
	transports TransportLifecycle,
	bindings BindingResolver,
	config ProductionConfig,
	timeout time.Duration,
	logger *slog.Logger,
	m12 M12Config,
) (*ExitServer, error) {
	if config.ExitWANName == "" || len(config.ExitTUNAddresses) == 0 {
		return nil, fmt.Errorf("Exit runtime requires WAN and TUN configuration: %w", ErrInvalidConfig)
	}
	maxPending := tunnellease.DefaultMaxActive
	if config.ConnectCountry != "" {
		maxPending--
	}
	return NewExitServer(ctx, ExitServerConfig{
		Host: node.Host(), LocalPeer: node.PeerID(), PrivateKey: identityPrivate,
		Transports: transports, Bindings: bindings,
		Authorizer: CoreAuthorizer{Node: node, Now: time.Now},
		Validation: icesession.DefaultValidationConfig(), SignalConfig: icesignal.DefaultConfig(),
		TUNName: config.TUNName, TUNAddresses: config.ExitTUNAddresses,
		WANName: config.ExitWANName, IPv4Pool: config.ExitIPv4Pool, IPv6Pool: config.ExitIPv6Pool,
		Management: config.ExitManagement, Bypass: config.Bypass,
		LeaseTTL: config.LeaseTTL, LeaseSweep: config.LeaseSweep,
		RequestTimeout: timeout, MaxPending: maxPending, Obfuscation: config.Obfuscation, Logger: logger,
		M12Enabled: m12.Enabled, IdentityPrivate: node.IdentityPrivateKey(),
		SignedDescriptor: node.LocalSignedAnnouncement, SessionControlPort: m12.SessionControlPort,
	})
}

func (d *Daemon) startClientRuntime(
	ctx context.Context, node *discovery.Node, monitor *networkstate.Monitor,
	protection *killswitch.Controller, connector exitselector.Connector,
	transport TransportLifecycle, core io.Closer,
	network NetworkReconciler,
) error {
	if node.ControlMark() != linuxroute.IrvioBypassMark {
		return fmt.Errorf("underlay host is not Irvio-marked: %w", ErrInvalidConfig)
	}
	targets := combinedUnderlayTargets{
		configured: d.config.UnderlayTargets,
		discovered: &DiscoveryUnderlayTargets{Node: node, Now: time.Now},
	}
	rawProbe, err := underlay.NewMarkedChallengeProbe(linuxroute.IrvioBypassMark)
	if err != nil {
		return err
	}
	peerProbe, err := underlay.NewLibp2pChallengeProbe(node.Host())
	if err != nil {
		return err
	}
	probe := productionUnderlayProbe{raw: rawProbe, libp2p: peerProbe}
	oracleConfig := underlay.DefaultConfig(targets, probe)
	oracleConfig.FailureThreshold = 1
	oracle, err := underlay.New(oracleConfig)
	if err != nil {
		return err
	}
	d.oracle = oracle
	var provider exitselector.CandidateProvider = &DiscoveryCandidateProvider{Node: node, Now: time.Now}
	var candidateProbe exitselector.CandidateProbe
	if d.m12 != nil {
		provider = d.m12.CandidateProvider()
		candidateProbe = d.m12.Probe()
	} else {
		candidateProbe, err = exitselector.NewMarkedTCPProbe(linuxroute.IrvioBypassMark)
		if err != nil {
			return err
		}
	}
	newSelector := func(country exitselector.CountryIntent, initial exitselector.UnderlayState) (Selector, error) {
		config := exitselector.DefaultConfig(
			node.PeerID(), country, provider, candidateProbe, connector,
			func() bool { return protection.Snapshot().State.Protected() }, initial,
		)
		return exitselector.New(config)
	}
	coordinator, err := New(Config{
		Core: core, Network: monitor, Underlay: oracle, Transport: transport,
		Protection: protection, NewSelector: newSelector, Now: time.Now,
		OperationTimeout: d.config.OperationTimeout, EventCapacity: 128,
		OnNetworkChange: func(ctx context.Context, state networkstate.State) error {
			var publicEpoch uint64
			var publicRequested bool
			if d.publicRole != nil {
				var err error
				publicEpoch, publicRequested, err = d.publicRole.invalidateNetwork(state)
				if err != nil {
					return fmt.Errorf("invalidate public reachability: %w", err)
				}
			}
			if network != nil {
				if err := network.ReconcileNetwork(ctx, state); err != nil {
					return fmt.Errorf("reconcile Irvio marked WAN routes: %w", err)
				}
			}
			if publicRequested {
				d.publicRole.reverifyNetwork(state, publicEpoch)
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	d.runtime = coordinator
	return coordinator.Start(ctx)
}

func productionBindingResolver(node *discovery.Node, registry *awgbinding.Registry) BindingResolver {
	return BindingResolver{
		Directory: node.Directory(), Registry: registry, Now: time.Now,
		Refresh: node.AuthenticatePeer,
	}
}
