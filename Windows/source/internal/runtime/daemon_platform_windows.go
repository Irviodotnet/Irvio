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
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketpolicy"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
)

type DaemonPlatformConfig struct {
	JournalDirectory string
	ProtectionOwner  wfp.Config
	// Product intent owns final unprotect; retiring a network attempt retains a deny-only policy.
	RetainProtection bool
}

type daemonPlatform struct {
	lifecycle          sync.Mutex
	mu                 sync.RWMutex
	state              networkstate.State
	migrating, closing bool
	backend            *killswitch.WindowsBackend
	updater            *killswitch.WindowsNetworkUpdater
	underlay           *killswitch.WindowsUnderlayOwner
	permissions        *WindowsSocketPermissions
	sockets            WindowsTransportSockets
	listeners          controlsocket.WindowsListeners
	dialer             tcptransport.DialerForAddr
	directory          io.Closer
	stunConn           *net.UDPConn
	stunGrant          io.Closer
}

type windowsDaemonJournal struct {
	directory *winfile.Directory
	lock      *os.File
}

func (j *windowsDaemonJournal) Close() error {
	return errors.Join(winfile.Unlock(j.lock), j.lock.Close(), j.directory.Close())
}

func openWindowsDaemonJournal(path string) (io.Closer, error) {
	directory, err := winfile.OpenDirectory(path, true)
	if err != nil {
		return nil, err
	}
	lock, err := directory.OpenFile("daemon.lock", true, true)
	if err != nil {
		return nil, errors.Join(err, directory.Close())
	}
	if err := winfile.Lock(lock); err != nil {
		return nil, errors.Join(err, lock.Close(), directory.Close())
	}
	return &windowsDaemonJournal{directory: directory, lock: lock}, nil
}

func validateDaemonPlatform(c DaemonConfig) error {
	if !filepath.IsAbs(c.Platform.JournalDirectory) {
		return fmt.Errorf("Windows daemon requires an absolute protected journal directory: %w", ErrInvalidConfig)
	}
	if c.Production != nil && c.Production.MuxFactory != nil {
		return fmt.Errorf("Windows daemon owns its transport socket factory: %w", ErrInvalidConfig)
	}
	client := c.Connector != nil || c.Production != nil && (!c.Exit || c.Production.ConnectCountry != "" || c.Production.PrivateTarget != "")
	if client && !c.ProtectedMode {
		return fmt.Errorf("Windows client requires ProtectedMode: %w", ErrInvalidConfig)
	}
	return nil
}

func (d *Daemon) newPlatformProtection(ctx context.Context) (*killswitch.Controller, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	app, err := wfp.ApplicationIDForPath(exe)
	if err != nil {
		return nil, err
	}
	policy := killswitch.Policy{TUNName: "irvio0", BypassMark: socketpolicy.Underlay}
	b, err := killswitch.NewWindowsBackend(killswitch.WindowsBackendConfig{
		Policy: policy, Owner: d.config.Platform.ProtectionOwner, Network: killswitch.WindowsNetwork{Application: app},
	})
	if err != nil {
		return nil, err
	}
	d.platform.backend = b
	var backend killswitch.Backend = b
	if d.config.Platform.RetainProtection {
		backend = windowsRetainedProtection{WindowsBackend: b}
	}
	return killswitch.New(killswitch.Config{Backend: backend, Policy: policy})
}

func (d *Daemon) preparePlatform(ctx context.Context) (uint32, error) {
	p := &d.platform
	var err error
	p.directory, err = openWindowsDaemonJournal(d.config.Platform.JournalDirectory)
	if err != nil {
		return 0, err
	}
	d.network, err = networkstate.NewMonitor(networkstate.Config{})
	if err != nil {
		return 0, err
	}
	p.state = d.network.Current()
	mode := WindowsSocketsHosting
	if d.config.ProtectedMode {
		generation, err := d.protection.Protect(ctx)
		if err != nil {
			return 0, err
		}
		d.config.Logger.Info("HYDRAD_PROTECTED_MODE", "generation", generation, "adopted", d.protection.Snapshot().Adopted)
		p.updater, err = p.backend.NetworkUpdater(ctx)
		if err != nil {
			return 0, err
		}
		p.underlay, err = p.updater.OwnUnderlay(ctx, uint64(p.state.Generation))
		if err != nil {
			return 0, err
		}
		mode = WindowsSocketsProtected
	}
	p.permissions, err = OpenWindowsSocketPermissions(WindowsSocketPermissionsConfig{
		Directory: filepath.Join(d.config.Platform.JournalDirectory, "sockets"), Generation: p.state.Generation, Mode: mode, Underlay: p.underlay,
	})
	if err != nil {
		return 0, err
	}
	if p.underlay == nil {
		p.dialer, err = controlsocket.DialerForBindings(func(ctx context.Context, target netip.Addr) (winroute.Binding, error) {
			binding, _, err := p.resolve(ctx, target)
			return binding, err
		})
	} else {
		p.dialer, err = controlsocket.DialerForOwnedBindings(func(ctx context.Context, target netip.AddrPort) (winroute.Binding, io.Closer, error) {
			binding, generation, err := p.resolve(ctx, target.Addr())
			if err != nil {
				return winroute.Binding{}, nil, err
			}
			lease, err := p.underlay.Acquire(ctx, uint64(generation), killswitch.WindowsBypass{Interface: binding.LUID, Source: binding.Source, Remote: target, Protocol: 6})
			return binding, lease, err
		})
	}
	if err != nil {
		return 0, err
	}
	d.config.PublicRole.ControlDialer = p.dialer
	if d.config.Production != nil {
		d.config.Production.Platform.Protection = p.updater
		d.config.Production.Platform.JournalDirectory = filepath.Join(d.config.Platform.JournalDirectory, "dataplane")
		d.config.Production.MuxFactory, err = p.sockets.Factory(p.permissions.Datagram)
		if err != nil {
			return 0, err
		}
	}
	if d.config.PublicSTUN {
		if err := d.startWindowsSTUN(ctx); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

func (p *daemonPlatform) binding(ctx context.Context, ipv4 bool) (winroute.Binding, networkstate.Generation, error) {
	if ctx == nil {
		return winroute.Binding{}, 0, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return winroute.Binding{}, 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closing || p.migrating {
		return winroute.Binding{}, 0, ErrGenerationStale
	}
	plan, err := (transportgen.PreferredBindPlanner{}).Plan(p.state.Snapshot)
	if err != nil {
		return winroute.Binding{}, 0, err
	}
	source := plan.IPv6
	if ipv4 {
		source = plan.IPv4
	}
	binding, err := winroute.ForSource(source)
	if err == nil && !transportSocketMatchesSnapshot(p.state.Snapshot, binding) {
		err = ErrGenerationStale
	}
	return binding, p.state.Generation, err
}

func (p *daemonPlatform) resolve(ctx context.Context, target netip.Addr) (winroute.Binding, networkstate.Generation, error) {
	binding, generation, err := p.binding(ctx, target.Is4())
	if err != nil {
		return winroute.Binding{}, 0, err
	}
	path, err := winroute.ResolveUnderlay(target, binding.LUID, nil)
	if err != nil {
		return winroute.Binding{}, 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closing || p.migrating || generation != p.state.Generation || !transportSocketMatchesSnapshot(p.state.Snapshot, path.Binding) {
		return winroute.Binding{}, 0, ErrGenerationStale
	}
	return path.Binding, generation, nil
}

func (d *Daemon) configurePlatformDiscovery(ctx context.Context, c *discovery.Config) error {
	p := &d.platform
	listener, err := p.listeners.Factory(ctx, func(ctx context.Context, ipv4 bool) (winroute.Binding, error) {
		b, _, err := p.binding(ctx, ipv4)
		return b, err
	}, func(ctx context.Context, b winroute.Binding, local netip.AddrPort) (io.Closer, error) {
		p.mu.RLock()
		defer p.mu.RUnlock()
		if p.closing || p.migrating || !transportSocketMatchesSnapshot(p.state.Snapshot, b) {
			return nil, ErrGenerationStale
		}
		return p.permissions.Listener(ctx, p.state.Generation, b, local)
	})
	if err != nil {
		return err
	}
	c.ControlDialer, c.ControlListener = p.dialer, listener
	return nil
}

type windowsListenCloser interface{ ListenClose(...multiaddr.Multiaddr) }

func (d *Daemon) platformNodeReady(_ context.Context, node *discovery.Node) error {
	if node.ControlMark() != 0 || d.platform.dialer == nil {
		return ErrInvalidConfig
	}
	if _, ok := node.Host().Network().(windowsListenCloser); !ok {
		return fmt.Errorf("Windows discovery requires owned listener migration: %w", ErrInvalidConfig)
	}
	return nil
}

type windowsDaemonTransport struct {
	*TransportOwner
	daemon *Daemon
}

func (d *Daemon) newDaemonTransport() (TransportLifecycle, error) {
	owner, err := newTransportOwner(*d.config.Production, d.config.Logger)
	if err != nil {
		return nil, err
	}
	return &windowsDaemonTransport{TransportOwner: owner, daemon: d}, nil
}

func (o *windowsDaemonTransport) Activate(ctx context.Context, state networkstate.State) error {
	if err := o.daemon.reconcileWindowsControl(ctx, state); err != nil {
		return err
	}
	return o.TransportOwner.Activate(ctx, state)
}

func (d *Daemon) reconcileWindowsControl(ctx context.Context, state networkstate.State) error {
	p := &d.platform
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.mu.Lock()
	if p.closing || state.Generation < p.state.Generation {
		p.mu.Unlock()
		return ErrGenerationStale
	}
	if state.Generation == p.state.Generation && !p.migrating {
		p.mu.Unlock()
		return nil
	}
	p.migrating = true
	p.mu.Unlock()
	if err := d.stopWindowsSTUN(); err != nil {
		return err
	}
	if d.node != nil {
		network := d.node.Host().Network()
		network.(windowsListenCloser).ListenClose(network.ListenAddresses()...)
		if err := p.listeners.Drain(); err != nil {
			return err
		}
		for _, c := range network.Conns() {
			if err := c.Close(); err != nil {
				return err
			}
		}
	}
	if err := p.permissions.Advance(ctx, state.Generation); err != nil {
		return err
	}
	p.mu.Lock()
	p.state = state
	p.state.Snapshot = state.Snapshot.Clone()
	p.migrating = false
	p.mu.Unlock()
	if d.node != nil {
		plan, err := (transportgen.PreferredBindPlanner{}).Plan(state.Snapshot)
		if err != nil {
			p.mu.Lock()
			p.migrating = true
			p.mu.Unlock()
			return err
		}
		var addresses []multiaddr.Multiaddr
		for _, source := range []netip.Addr{plan.IPv4, plan.IPv6} {
			if !source.IsValid() {
				continue
			}
			family, bind := "ip6", "::"
			if source.Is4() {
				family, bind = "ip4", "0.0.0.0"
			}
			address, err := multiaddr.NewMultiaddr(fmt.Sprintf("/%s/%s/tcp/%d", family, bind, d.config.ListenPort))
			if err != nil {
				return err
			}
			addresses = append(addresses, address)
		}
		if err := d.node.Host().Network().Listen(addresses...); err != nil {
			p.mu.Lock()
			p.migrating = true
			p.mu.Unlock()
			return err
		}
	}
	if d.config.PublicSTUN {
		if err := d.startWindowsSTUN(ctx); err != nil {
			p.mu.Lock()
			p.migrating = true
			p.mu.Unlock()
			return err
		}
	}
	return nil
}

// Unprotect is daemon-owned so downstream and discovery cannot survive an explicit disconnect.
type windowsDaemonProtection struct {
	*killswitch.Controller
	daemon *Daemon
}

func (d *Daemon) disconnectProtection(ctx context.Context) error {
	return (windowsDaemonProtection{Controller: d.protection, daemon: d}).Disconnect(ctx)
}

func (p windowsDaemonProtection) Disconnect(ctx context.Context) error {
	if err := p.daemon.closeWindowsParticipation(ctx); err != nil {
		return err
	}
	return p.Controller.Disconnect(ctx)
}

func (p windowsDaemonProtection) Close(ctx context.Context) error {
	if err := p.daemon.closeWindowsParticipation(ctx); err != nil {
		return err
	}
	return p.Controller.Close(ctx)
}

func (d *Daemon) closeWindowsParticipation(ctx context.Context) error {
	p := &d.platform
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.mu.Lock()
	p.closing = true
	p.mu.Unlock()
	d.mu.Lock()
	d.privateAdminExit = nil
	d.mu.Unlock()
	if d.exitServer != nil {
		d.exitServer.stopAdmissions()
	}
	if d.privateHosting != nil {
		d.privateHosting.policy.Close()
	}
	if d.m12 != nil {
		if err := d.m12.stopExitRegistrations(ctx); err != nil {
			return err
		}
		if err := d.m12.Close(ctx); err != nil {
			return err
		}
	}
	if d.publicRole != nil {
		if err := d.publicRole.Close(); err != nil {
			return err
		}
	}
	if d.exitServer != nil {
		if err := d.exitServer.Close(ctx); err != nil {
			return err
		}
	}
	if d.clientCore != nil {
		if err := d.clientCore.Close(); err != nil {
			return err
		}
	}
	if d.transport != nil {
		if err := d.transport.Close(); err != nil {
			return err
		}
	}
	if d.underlayChallenge != nil {
		if err := d.underlayChallenge.Close(); err != nil {
			return err
		}
	}
	if d.node != nil {
		if err := d.node.Close(); err != nil {
			return err
		}
	}
	if err := d.stopWindowsSTUN(); err != nil {
		return err
	}
	if err := errors.Join(p.listeners.Close(), p.sockets.Close()); err != nil {
		return err
	}
	if p.permissions != nil {
		if err := p.permissions.Close(); err != nil {
			return err
		}
	}
	if p.underlay != nil {
		if err := p.underlay.Close(); err != nil {
			return err
		}
	}
	if p.directory != nil {
		if err := p.directory.Close(); err != nil {
			return err
		}
		p.directory = nil
	}
	return nil
}

func (d *Daemon) closeResources(ctx context.Context) error {
	if d.exitPump != nil {
		d.exitPump.stop()
	}
	if d.runtime != nil {
		if err := d.runtime.Close(ctx); err != nil {
			return fmt.Errorf("close Windows runtime: %w", err)
		}
	}
	if err := d.closeWindowsParticipation(ctx); err != nil {
		return fmt.Errorf("close Windows participation: %w", err)
	}
	if d.oracle != nil {
		if err := d.oracle.Close(); err != nil {
			return fmt.Errorf("close Windows underlay oracle: %w", err)
		}
	}
	if d.network != nil {
		if err := d.network.Close(); err != nil {
			return fmt.Errorf("close Windows network monitor: %w", err)
		}
	}
	if d.protection != nil {
		if err := d.protection.Close(ctx); err != nil {
			return fmt.Errorf("close Windows protection: %w", err)
		}
	}
	return nil
}
