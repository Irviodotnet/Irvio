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
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/multiformats/go-multiaddr"

	"hydra.local/hydra/internal/awgbinding"
	"hydra.local/hydra/internal/awgidentity"
	"hydra.local/hydra/internal/controlbypass"
	"hydra.local/hydra/internal/discovery"
	"hydra.local/hydra/internal/exitselector"
	"hydra.local/hydra/internal/killswitch"
	"hydra.local/hydra/internal/linuxroute"
	"hydra.local/hydra/internal/networkstate"
	"hydra.local/hydra/internal/rendezvous"
	"hydra.local/hydra/internal/stunserver"
	"hydra.local/hydra/internal/underlay"
)

type DaemonConfig struct {
	DataDir            string
	ListenPort         int
	Bootstrap          []string
	Exit               bool
	Country            string
	DHTMode            string
	Logger             *slog.Logger
	ProtectedMode      bool
	ProtectAtStart     bool
	RotateAWGKey       bool
	Production         *ProductionConfig
	PublicRole         PublicRoleConfig
	M12                M12Config
	PublicSTUN         bool
	PublicSTUNBind     netip.Addr
	AutoExitCountry    bool
	RequireExitCountry bool

	Connector        exitselector.Connector
	UnderlayTargets  underlay.TargetProvider
	OperationTimeout time.Duration
}

// Daemon owns process-long resources and exposes a narrow production lifecycle.
type Daemon struct {
	config DaemonConfig

	mu                sync.RWMutex
	rotateMu          sync.Mutex
	identity          *awgidentity.Identity
	node              *discovery.Node
	network           *networkstate.Monitor
	protection        *killswitch.Controller
	oracle            *underlay.Oracle
	underlayChallenge *underlay.Libp2pChallengeService
	runtime           *Runtime
	transport         TransportLifecycle
	exitServer        *ExitServer
	exitPump          *exitTransportPump
	clientCore        io.Closer
	controlBypass     *controlbypass.Ownership
	publicRole        *publicRole
	m12               *m12Runtime
	stunServer        *stunserver.Server
	started           bool
	closed            bool
	closeMu           sync.Mutex
}

func NewDaemon(config DaemonConfig) (*Daemon, error) {
	if config.DataDir == "" || config.ListenPort < 0 || config.ListenPort > 65535 {
		return nil, ErrInvalidConfig
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.OperationTimeout == 0 {
		config.OperationTimeout = 10 * time.Second
	}
	if config.OperationTimeout <= 0 || config.OperationTimeout > time.Minute {
		return nil, ErrInvalidConfig
	}
	if config.M12.Enabled {
		if config.M12.Serve && !config.PublicRole.Requested ||
			config.Exit && config.Production != nil && config.M12.SessionControlPort == 0 {
			return nil, ErrInvalidConfig
		}
		if config.M12.Policy.MaxRegistrations == 0 {
			config.M12.Policy = rendezvous.DefaultConfig()
		}
	}
	if config.PublicRole.OperationTimeout == 0 {
		config.PublicRole.OperationTimeout = config.OperationTimeout
	}
	if config.PublicRole.Logger == nil {
		config.PublicRole.Logger = config.Logger
	}
	config.Bootstrap = append([]string(nil), config.Bootstrap...)
	config.PublicRole.VerifierAddresses = append([]string(nil), config.PublicRole.VerifierAddresses...)
	config.PublicRole.VerifierBootstrap = append([]string(nil), config.PublicRole.VerifierBootstrap...)
	config.M12.RendezvousPeers = append([]string(nil), config.M12.RendezvousPeers...)
	if config.Production != nil {
		copy := *config.Production
		if copy.UDPPort < 0 || copy.UDPPort > 65535 {
			return nil, ErrInvalidConfig
		}
		copy.Bypass = append([]netip.Prefix(nil), copy.Bypass...)
		copy.STUNIPv4Pool = append([]netip.AddrPort(nil), copy.STUNIPv4Pool...)
		copy.ExitTUNAddresses = append([]netip.Prefix(nil), copy.ExitTUNAddresses...)
		copy.ExitManagement = append([]netip.Prefix(nil), copy.ExitManagement...)
		config.Production = &copy
	}
	return &Daemon{config: config}, nil
}

func (d *Daemon) Start(ctx context.Context) (resultErr error) {
	if ctx == nil {
		return ErrInvalidConfig
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return ErrClosed
	}
	if d.started {
		d.mu.Unlock()
		return ErrAlreadyStarted
	}
	d.started = true
	d.mu.Unlock()
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, d.closeResources(context.Background()))
		}
	}()

	identity, err := awgidentity.LoadOrCreate(d.config.DataDir)
	if err != nil {
		return fmt.Errorf("load AWG identity: %w", err)
	}
	if d.config.RotateAWGKey {
		if _, _, err := identity.Rotate(); err != nil {
			return fmt.Errorf("rotate AWG identity: %w", err)
		}
	}
	d.identity = identity

	protection, err := killswitch.New(killswitch.Config{
		Backend: killswitch.NFTBackend{},
		Policy: killswitch.Policy{
			TUNName: "hydra0", BypassMark: linuxroute.IrvioBypassMark,
			TunnelControlMark: linuxroute.IrvioTunnelControlMark,
		},
	})
	if err != nil {
		return err
	}
	d.protection = protection
	if d.config.PublicSTUN {
		bind := d.config.PublicSTUNBind
		if !bind.IsValid() {
			bind = netip.IPv4Unspecified()
		}
		server, err := stunserver.Listen(bind, stunserver.DefaultPort)
		if err != nil {
			return err
		}
		d.stunServer = server
		d.config.Logger.Info("PUBLIC_STUN_READY", "bind", server.Addr())
	}
	controlMark := uint32(0)
	runtimeConfigured := d.config.Connector != nil || d.config.Production != nil &&
		(!d.config.Exit || d.config.Production.ConnectCountry != "")
	if d.config.ProtectedMode && (!runtimeConfigured || d.config.ProtectAtStart) {
		generation, err := protection.Protect(ctx)
		if err != nil {
			return err
		}
		d.config.Logger.Info("HYDRAD_PROTECTED_MODE", "generation", generation, "adopted", protection.Snapshot().Adopted)
		controlMark = linuxroute.IrvioBypassMark
	}
	if d.config.Connector != nil || d.config.Production != nil || d.config.M12.Enabled {
		controlMark = linuxroute.IrvioBypassMark
	}
	publicKey := identity.PublicKey
	announcementTTL := time.Duration(0)
	announcementRefresh := time.Duration(0)
	if d.config.M12.Enabled {
		announcementTTL = d.config.M12.Policy.DescriptorTTL
		announcementRefresh = announcementTTL / 2
	}
	node, err := discovery.Start(ctx, discovery.Config{
		DataDir: d.config.DataDir, ListenPort: d.config.ListenPort,
		Bootstrap: d.config.Bootstrap, Exit: d.config.Exit, Country: d.config.Country,
		Logger: d.config.Logger, DHTMode: d.config.DHTMode, ControlMark: controlMark,
		AWGPublicKey: publicKey[:], AWGKeyEpoch: identity.Epoch,
		DeferExitAdvertisement: d.config.Exit && d.config.Production != nil,
		AnnouncementTTL:        announcementTTL, AnnouncementRefresh: announcementRefresh,
	})
	if err != nil {
		return err
	}
	d.node = node
	if needsAcceptedControlBypass(d.config) {
		ports, err := acceptedControlListenPorts(node.ListenAddresses())
		if err != nil {
			return fmt.Errorf("resolve accepted-control listeners: %w", err)
		}
		digest := sha256.Sum256([]byte(d.config.DataDir))
		suffix := fmt.Sprintf("%x", digest[:8])
		owner, err := controlbypass.Install(controlbypass.Config{
			Name: "hydrad-" + suffix, TableName: "hydra_ctl_" + suffix,
			Mark: linuxroute.IrvioBypassMark, PID: os.Getpid(), ListenPorts: ports,
		})
		if err != nil {
			return fmt.Errorf("install accepted-control bypass ownership: %w", err)
		}
		d.controlBypass = owner
	}
	if d.config.M12.Enabled {
		if err := node.EnableSessionEstablishment(); err != nil {
			return fmt.Errorf("publish M12 session protocols: %w", err)
		}
	}
	if d.config.Production != nil {
		if node.ControlMark() != linuxroute.IrvioBypassMark {
			return fmt.Errorf("production libp2p transport is not Irvio-marked: %w", ErrInvalidConfig)
		}
		d.underlayChallenge, err = underlay.NewLibp2pChallengeService(node.Host())
		if err != nil {
			return err
		}
	}
	monitor, err := networkstate.NewMonitor(networkstate.Config{})
	if err != nil {
		return fmt.Errorf("start NetworkGeneration monitor: %w", err)
	}
	d.network = monitor
	publicConfig := d.config.PublicRole
	if publicConfig.Requested && publicConfig.DiscoverVerifiers && len(publicConfig.VerifierAddresses) == 0 {
		publicConfig.VerifierBootstrap = append([]string(nil), d.config.Bootstrap...)
		lookup, cancel := context.WithTimeout(ctx, d.config.OperationTimeout)
		publicConfig.VerifierAddresses, err = discoverPublicVerifiers(lookup, node, d.config.Bootstrap, publicConfig.AllowTestAddresses)
		cancel()
		if err != nil {
			return fmt.Errorf("select M11 verifiers: %w", err)
		}
	} else {
		// Explicit engineering verifier addresses remain pinned.
		publicConfig.DiscoverVerifiers = false
	}
	role, err := newPublicRole(ctx, node, publicConfig)
	if err != nil {
		return fmt.Errorf("start public reachability role: %w", err)
	}
	d.publicRole = role
	if d.config.M12.Enabled {
		m12, err := newM12Runtime(ctx, node, role, d.config.M12, d.config.Logger)
		if err != nil {
			return fmt.Errorf("start M12 Rendezvous runtime: %w", err)
		}
		d.m12 = m12
	}

	if d.config.Production != nil {
		owner, err := newTransportOwner(*d.config.Production, d.config.Logger)
		if err != nil {
			return err
		}
		d.transport = owner
		bindings := productionBindingResolver(node, awgbinding.NewRegistry())
		if d.config.Exit {
			initial, cancelInitial := context.WithTimeout(ctx, d.config.OperationTimeout)
			err := owner.Activate(initial, monitor.Current())
			cancelInitial()
			if err != nil {
				return err
			}
			server, err := newProductionExit(ctx, node, identity.PrivateKey, owner, bindings, *d.config.Production, d.config.OperationTimeout, d.config.Logger, d.config.M12)
			if err != nil {
				return err
			}
			d.exitServer = server
			pump, err := startExitTransportPump(ctx, monitor, owner, d.config.Logger, d.config.OperationTimeout, func(state networkstate.State) error {
				publicEpoch, publicRequested, err := role.invalidateNetwork(state)
				if err != nil {
					return err
				}
				reconcileContext, cancel := context.WithTimeout(ctx, d.config.OperationTimeout)
				reconcileErr := server.ReconcileNetwork(reconcileContext, state)
				cancel()
				if reconcileErr == nil && publicRequested {
					role.reverifyNetwork(state, publicEpoch)
				}
				return reconcileErr
			})
			if err != nil {
				return err
			}
			d.exitPump = pump
			exitReady := true
			if d.config.AutoExitCountry {
				resolved, resolveErr := observedExitCountry(ctx, owner.Current(), d.config.M12.GeoIPDatabase)
				if resolveErr != nil {
					if d.config.RequireExitCountry {
						return fmt.Errorf("desktop Exit country is required before CONNECT: %w", resolveErr)
					}
					d.config.Logger.Warn("EXIT_COUNTRY_UNKNOWN", "reason", resolveErr)
					exitReady = false
				} else if err := node.SetExitCountry(resolved); err != nil {
					return fmt.Errorf("set observed Exit country: %w", err)
				}
			}
			if exitReady {
				if d.config.PublicRole.Requested && d.config.M12.Serve {
					if err := d.m12.ArmExit(server.SessionEstablishmentHandler()); err != nil {
						return fmt.Errorf("arm M11-gated Exit advertisement: %w", err)
					}
				} else {
					if err := node.ActivateExit(ctx); err != nil {
						return fmt.Errorf("advertise ready production Exit: %w", err)
					}
					if d.m12 != nil {
						if err := d.m12.StartExitRegistrations(server.SessionEstablishmentHandler()); err != nil {
							return fmt.Errorf("start Exit Rendezvous registrations: %w", err)
						}
					}
				}
			}
		}
		if !d.config.Exit || d.config.Production.ConnectCountry != "" {
			var sharedInstaller SessionInstaller
			if d.exitServer != nil {
				sharedInstaller = d.exitServer.ClientInstaller()
				if d.exitPump != nil {
					d.exitPump.stop()
					d.exitPump = nil
				}
			}
			var client *productionClient
			if d.m12 != nil {
				client, err = newProductionBrokeredClient(
					node, node.IdentityPrivateKey(), identity.PrivateKey, identity.Epoch,
					owner, bindings, *d.config.Production, d.config.OperationTimeout, sharedInstaller, d.m12,
				)
			} else {
				client, err = newProductionClient(node, identity.PrivateKey, owner, bindings, *d.config.Production, d.config.OperationTimeout, sharedInstaller)
			}
			if err != nil {
				return err
			}
			d.clientCore = client.core()
			if err := d.startClientRuntime(ctx, node, monitor, protection, client.connector, owner, d.clientCore, client.network); err != nil {
				return err
			}
			if d.config.Production.ConnectCountry != "" {
				if err := d.runtime.Connect(ctx, d.config.Production.ConnectCountry); err != nil {
					return err
				}
			}
		}
	} else if d.config.Connector != nil {
		if err := d.startClientRuntime(ctx, node, monitor, protection, d.config.Connector, nil, nil, nil); err != nil {
			return err
		}
	}
	if d.config.PublicRole.Requested {
		role.NetworkChanged(monitor.Current())
		if d.runtime == nil && d.exitPump == nil {
			if err := role.WatchNetwork(monitor); err != nil {
				return err
			}
		}
	}
	d.config.Logger.Info(
		"HYDRAD_RUNTIME_STARTED", "peer_id", node.PeerID(),
		"awg_key_fingerprint", identity.PublicKey.Fingerprint(),
		"awg_key_epoch", identity.Epoch, "network_generation", monitor.Current().Generation,
	)
	return nil
}

// Mark accepted control traffic so protected Exits and verifiers can answer without user-WAN bypass.
func needsAcceptedControlBypass(config DaemonConfig) bool {
	return config.ProtectedMode && (config.Exit || config.PublicRole.Requested)
}

func acceptedControlListenPorts(addresses []string) ([]uint16, error) {
	seen := make(map[uint16]struct{})
	ports := make([]uint16, 0, 2)
	for _, address := range addresses {
		value, err := multiaddr.NewMultiaddr(address)
		if err != nil {
			return nil, err
		}
		tcpPort, err := value.ValueForProtocol(multiaddr.P_TCP)
		if err != nil {
			continue
		}
		port, err := strconv.ParseUint(tcpPort, 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("invalid control TCP listener port %q", tcpPort)
		}
		if _, ok := seen[uint16(port)]; !ok {
			seen[uint16(port)] = struct{}{}
			ports = append(ports, uint16(port))
		}
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("no control TCP listener")
	}
	return ports, nil
}

func (d *Daemon) Node() *discovery.Node {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.node
}

func (d *Daemon) AWGKeyEpoch() uint64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.identity == nil {
		return 0
	}
	_, _, epoch := d.identity.Snapshot()
	return epoch
}

// RotateAWGKey durably signs one relationship's new key and fences admissions until commit.
func (d *Daemon) RotateAWGKey(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	d.rotateMu.Lock()
	defer d.rotateMu.Unlock()
	d.mu.RLock()
	identity, node, server, m12, closed := d.identity, d.node, d.exitServer, d.m12, d.closed
	d.mu.RUnlock()
	if closed || identity == nil || node == nil || server == nil || m12 == nil {
		return ErrNotStarted
	}
	reservation, err := server.reserveAWGRotation()
	if err != nil {
		return fmt.Errorf("AWG direct rotation unavailable: %w", err)
	}
	publicKey, epoch, err := identity.Rotate()
	if err != nil {
		server.cancelAWGRotation(reservation)
		return fmt.Errorf("persist AWG rotation: %w", err)
	}
	privateKey, currentPublic, currentEpoch := identity.Snapshot()
	if currentPublic != publicKey || currentEpoch != epoch {
		return ErrGenerationStale
	}
	if err := node.UpdateAWGBinding(publicKey[:], epoch); err != nil {
		return fmt.Errorf("publish AWG rotation binding: %w", err)
	}
	m12.DescriptorChanged()
	done, err := server.stageAWGRotation(reservation, privateKey, publicKey, epoch, node.LocalSignedAnnouncement())
	if err != nil {
		return fmt.Errorf("stage direct AWG rotation: %w", err)
	}
	d.config.Logger.Info("AWG_DIRECT_ROTATION_STAGED", "key_epoch", epoch)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("commit direct AWG rotation: %w", err)
		}
		d.config.Logger.Info("AWG_DIRECT_ROTATION_COMPLETE", "key_epoch", epoch)
		return nil
	}
}

func (d *Daemon) PublicRoleStatus() PublicRoleStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.publicRole == nil {
		return PublicRoleStatus{}
	}
	return d.publicRole.Status()
}

func (d *Daemon) DisablePublicRole(ctx context.Context) error {
	d.mu.RLock()
	role := d.publicRole
	d.mu.RUnlock()
	if role == nil {
		return ErrNotStarted
	}
	return role.Disable(ctx)
}

func (d *Daemon) ControlBypassStatus() (controlbypass.Snapshot, error) {
	d.mu.RLock()
	owner := d.controlBypass
	d.mu.RUnlock()
	if owner == nil {
		return controlbypass.Snapshot{}, ErrNotStarted
	}
	return owner.Snapshot()
}

func (d *Daemon) BindingResolver() (BindingResolver, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.node == nil {
		return BindingResolver{}, ErrNotStarted
	}
	return BindingResolver{
		Directory: d.node.Directory(), Registry: awgbinding.NewRegistry(), Now: time.Now,
		Refresh: d.node.AuthenticatePeer,
	}, nil
}

func (d *Daemon) Candidates(ctx context.Context, country string, limit int) (exitselector.CandidateBatch, error) {
	d.mu.RLock()
	node, m12 := d.node, d.m12
	d.mu.RUnlock()
	if node == nil {
		return exitselector.CandidateBatch{}, ErrNotStarted
	}
	request := exitselector.CandidateRequest{Country: exitselector.CountryIntent(country), Limit: limit}
	if m12 != nil {
		return m12.CandidateProvider().GetCandidates(ctx, request)
	}
	return (&DiscoveryCandidateProvider{Node: node, Now: time.Now}).GetCandidates(ctx, request)
}

// AvailableCountries reads product metadata without changing session intent or lifecycle.
func (d *Daemon) AvailableCountries(ctx context.Context, refresh bool) (rendezvous.AvailableCountries, error) {
	d.mu.RLock()
	m12 := d.m12
	d.mu.RUnlock()
	if m12 == nil {
		return rendezvous.AvailableCountries{
			Status: rendezvous.AvailableCountriesUnavailable, GeneratedAt: time.Now().UTC(),
		}, nil
	}
	return m12.AvailableCountries(ctx, refresh)
}

func (d *Daemon) Connect(ctx context.Context, country exitselector.CountryIntent) error {
	d.mu.RLock()
	runtime := d.runtime
	d.mu.RUnlock()
	if runtime == nil {
		return fmt.Errorf("production connection driver is not configured: %w", ErrInvalidConfig)
	}
	return runtime.Connect(ctx, country)
}

func (d *Daemon) Disconnect(ctx context.Context) error {
	d.mu.RLock()
	runtime, protection := d.runtime, d.protection
	d.mu.RUnlock()
	if runtime != nil {
		return runtime.Disconnect(ctx)
	}
	if protection == nil || !protection.Snapshot().State.Protected() {
		return ErrNotConnected
	}
	return protection.Disconnect(ctx)
}

func (d *Daemon) Events() <-chan Event {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.runtime == nil {
		return nil
	}
	return d.runtime.Events()
}

func (d *Daemon) Status() Status {
	d.mu.RLock()
	runtime, monitor, protection, public, node := d.runtime, d.network, d.protection, d.publicRole, d.node
	d.mu.RUnlock()
	decorate := func(status Status) Status {
		if node != nil {
			status.ExitContributing = node.ExitActive()
		}
		if public == nil {
			return status
		}
		value := public.Status()
		status.PublicNodeRequested = value.Requested
		status.PublicControlIPv4, status.PublicControlIPv6 = value.IPv4.State, value.IPv6.State
		status.PublicEndpointIPv4 = value.IPv4.Proof.Endpoint.Address
		status.PublicEndpointIPv6 = value.IPv6.Proof.Endpoint.Address
		return status
	}
	if runtime != nil {
		return decorate(runtime.Status())
	}
	status := Status{State: StateIdle, UpdatedAt: time.Now().UTC()}
	if monitor != nil {
		status.NetworkGeneration = monitor.Current().Generation
	}
	if protection != nil {
		status.ProtectedMode = protection.Snapshot().State
	}
	return decorate(status)
}

func (d *Daemon) Close(ctx context.Context) error {
	if d == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalidConfig
	}
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	return d.closeResources(ctx)
}

func (d *Daemon) closeResources(ctx context.Context) error {
	if d.exitServer != nil {
		d.exitServer.stopAdmissions()
	}
	if d.m12 != nil {
		if err := d.m12.stopExitRegistrations(ctx); err != nil {
			return err
		}
	}
	if d.runtime != nil {
		if err := d.runtime.Close(ctx); err != nil {
			return err
		}
		d.runtime, d.network, d.oracle, d.protection = nil, nil, nil, nil
		d.transport, d.clientCore = nil, nil
	}
	if d.m12 != nil {
		if err := d.m12.Close(ctx); err != nil {
			return err
		}
		d.m12 = nil
	}
	if d.publicRole != nil {
		if err := d.publicRole.Close(); err != nil {
			return err
		}
		d.publicRole = nil
	}
	if d.exitServer != nil {
		if err := d.exitServer.Close(ctx); err != nil {
			return err
		}
		d.exitServer = nil
	}
	if d.exitPump != nil {
		if err := d.exitPump.Close(); err != nil {
			return err
		}
		d.exitPump, d.transport = nil, nil
	}
	if d.clientCore != nil {
		if err := d.clientCore.Close(); err != nil {
			return err
		}
		d.clientCore = nil
	}
	if d.transport != nil {
		if err := d.transport.Close(); err != nil {
			return err
		}
		d.transport = nil
	}
	if d.protection != nil {
		if d.protection.Snapshot().State.Protected() {
			if err := d.protection.Disconnect(ctx); err != nil {
				return err
			}
		}
		if err := d.protection.Close(ctx); err != nil {
			return err
		}
		d.protection = nil
	}
	if d.network != nil {
		if err := d.network.Close(); err != nil {
			return err
		}
		d.network = nil
	}
	if d.underlayChallenge != nil {
		if err := d.underlayChallenge.Close(); err != nil {
			return err
		}
		d.underlayChallenge = nil
	}
	if d.node != nil {
		if err := d.node.Close(); err != nil {
			return err
		}
		d.node = nil
	}
	if d.controlBypass != nil {
		if err := d.controlBypass.Close(); err != nil {
			return err
		}
		d.controlBypass = nil
	}
	if d.stunServer != nil {
		if err := d.stunServer.Close(); err != nil {
			return err
		}
		d.stunServer = nil
	}
	return nil
}

// StaticTargets supplies bounded literal-IP targets without coupling Oracle to discovery.
type StaticTargets []underlay.Target

func (s StaticTargets) Targets(_ context.Context, family networkstate.Family, limit int) ([]underlay.Target, error) {
	result := make([]underlay.Target, 0, limit)
	for _, target := range s {
		address := target.Endpoint.Addr()
		if target.Endpoint.IsValid() &&
			(family == networkstate.FamilyIPv4 && address.Is4() ||
				family == networkstate.FamilyIPv6 && address.Is6() && !address.Is4In6()) {
			result = append(result, target)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}
