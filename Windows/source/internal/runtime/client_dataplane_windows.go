// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"path/filepath"
	"slices"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketpolicy"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/windns"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

type ClientDataPlanePlatform struct {
	Protection       *killswitch.WindowsNetworkUpdater
	JournalDirectory string
	DNSServers       []netip.Addr
}

func normalizeClientPlatform(config *ClientDataPlaneConfig) error {
	if config.Platform.Protection == nil || !filepath.IsAbs(config.Platform.JournalDirectory) ||
		len(config.Bypass) != 0 || len(config.Platform.DNSServers) == 0 || len(config.Platform.DNSServers) > 8 {
		return ErrInvalidConfig
	}
	if config.OuterMark == 0 {
		config.OuterMark = socketpolicy.Underlay
	}
	if config.OuterMark != socketpolicy.Underlay {
		return ErrInvalidConfig
	}
	for _, a := range config.Platform.DNSServers {
		if !a.IsGlobalUnicast() || a.Is4In6() || a.Zone() != "" {
			return ErrInvalidConfig
		}
	}
	config.Platform.DNSServers = slices.Clone(config.Platform.DNSServers)
	return nil
}

func validateClientInstall(r InstallRequest) error {
	e := r.TunnelControlEndpoint
	if r.BeforeUserRoutes == nil || !e.IsValid() || e.Port() == 0 || !e.Addr().IsGlobalUnicast() || e.Addr().Is4In6() || e.Addr().Zone() != "" {
		return ErrInvalidConfig
	}
	source := r.Lease.IPv6
	if e.Addr().Is4() {
		source = r.Lease.IPv4
	}
	if !source.IsValid() || source.Addr() == e.Addr() ||
		!slices.Contains(r.TunnelControl, netip.PrefixFrom(e.Addr(), e.Addr().BitLen())) {
		return ErrInvalidConfig
	}
	return nil
}

func newClientTunnel(config tunnel.Config) (tunnel.DeviceOwner, error) {
	return tunnel.NewWindows(config)
}

// Network changes fence staged promotions and revoke user permission before transport migration.
func (p *ClientDataPlane) ReconcileNetwork(ctx context.Context, state networkstate.State) error {
	if p == nil {
		return ErrInvalidConfig
	}
	return p.reconcileWindowsNetwork(ctx, state, p.config.Platform.Protection)
}

func (p *ClientDataPlane) reconcileWindowsNetwork(ctx context.Context, state networkstate.State, policy windowsTunnelPolicy) error {
	if ctx == nil || state.Generation == 0 || policy == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if state.Generation < p.networkFloor {
		return ErrGenerationStale
	}
	p.networkFloor = state.Generation
	if p.current == nil || p.current.generation >= state.Generation {
		return nil
	}
	return policy.UpdateTunnel(ctx, 0, nil)
}

var _ NetworkReconciler = (*ClientDataPlane)(nil)

type windowsTunnelPolicy interface {
	UpdateTunnel(context.Context, uint64, []killswitch.WindowsTunnelControl) error
}

type windowsSessionRoutes interface {
	EnsureControl(context.Context, []netip.Addr) error
	Ensure(context.Context, bool, bool) error
	Remove(context.Context) error
	Close() error
}

type windowsSessionDNS interface {
	Apply(context.Context, []netip.Addr) error
	Close() error
}

type windowsClientRoutes struct {
	protection windowsTunnelPolicy
	routes     windowsSessionRoutes
	dns        windowsSessionDNS
	directory  io.Closer
	mu         sync.Mutex
}

func (p *ClientDataPlane) configureRoutes(ctx context.Context, owner tunnel.DeviceOwner, request InstallRequest) (io.Closer, io.Closer, error) {
	w, ok := owner.(*tunnel.Windows)
	if !ok {
		return nil, nil, ErrInvalidConfig
	}
	directory, err := winfile.OpenDirectory(p.config.Platform.JournalDirectory, true)
	if err != nil {
		return nil, nil, localPromotionFailure("open_session_journal", err)
	}
	routing, err := winroute.OpenTunnelRoutes(w.LUID(), filepath.Join(p.config.Platform.JournalDirectory, "routes"))
	if err != nil {
		return nil, nil, localPromotionFailure("open_session_routes", errors.Join(err, directory.Close()))
	}
	dns, err := windns.Open(w.LUID(), filepath.Join(p.config.Platform.JournalDirectory, "dns"))
	if err != nil {
		return nil, nil, localPromotionFailure("open_session_dns", errors.Join(err, routing.Close(), directory.Close()))
	}
	resources := &windowsClientRoutes{protection: p.config.Platform.Protection, routes: routing, dns: dns, directory: directory}
	control, err := resources.activate(ctx, w.LUID(), request, p.config.Platform.DNSServers)
	if err != nil {
		return nil, nil, errors.Join(err, resources.Close())
	}
	return resources, control, nil
}

func (r *windowsClientRoutes) activate(ctx context.Context, luid uint64, request InstallRequest, servers []netip.Addr) (_ io.Closer, resultErr error) {
	if ctx == nil || luid == 0 {
		return nil, ErrInvalidConfig
	}
	if err := validateClientInstall(request); err != nil {
		return nil, err
	}
	source := request.Lease.IPv6.Addr()
	if request.TunnelControlEndpoint.Addr().Is4() {
		source = request.Lease.IPv4.Addr()
	}
	controlGrant := []killswitch.WindowsTunnelControl{{Interface: luid, Source: source, Remote: request.TunnelControlEndpoint}}
	if err := r.protection.UpdateTunnel(ctx, 0, controlGrant); err != nil {
		return nil, localPromotionFailure("protect_control_tunnel", err)
	}
	if err := r.routes.EnsureControl(ctx, []netip.Addr{request.TunnelControlEndpoint.Addr()}); err != nil {
		return nil, localPromotionFailure("install_control_routes", err)
	}
	control, err := request.BeforeUserRoutes(ctx)
	if err != nil || control == nil {
		if err == nil {
			err = ErrInvalidConfig
		}
		return nil, errors.Join(err, closeOptional(control))
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, control.Close())
		}
	}()
	if err := r.routes.Remove(ctx); err != nil {
		return nil, localPromotionFailure("remove_control_routes", err)
	}
	if err := r.routes.Ensure(ctx, request.Lease.IPv4.IsValid(), request.Lease.IPv6.IsValid()); err != nil {
		return nil, localPromotionFailure("install_session_routes", err)
	}
	var usableDNS []netip.Addr
	for _, a := range servers {
		if (a.Is4() && request.Lease.IPv4.IsValid()) || (a.Is6() && request.Lease.IPv6.IsValid()) {
			usableDNS = append(usableDNS, a)
		}
	}
	if len(usableDNS) == 0 {
		return nil, localPromotionFailure("tunnel_dns_family", ErrInvalidConfig)
	}
	if err := r.dns.Apply(ctx, usableDNS); err != nil {
		return nil, localPromotionFailure("tunnel_dns_activate", err)
	}
	if err := r.protection.UpdateTunnel(ctx, luid, nil); err != nil {
		return nil, localPromotionFailure("activate_user_tunnel", err)
	}
	return control, nil
}

func (r *windowsClientRoutes) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// RELEASE precedes this cleanup; keep failed owners retryable without repeating successful closes.
	var failure error
	if r.protection != nil {
		if err := r.protection.UpdateTunnel(context.Background(), 0, nil); err != nil {
			failure = errors.Join(failure, err)
		} else {
			r.protection = nil
		}
	}
	if r.dns != nil {
		if err := r.dns.Close(); err != nil {
			failure = errors.Join(failure, err)
		} else {
			r.dns = nil
		}
	}
	if r.routes != nil {
		if err := r.routes.Close(); err != nil {
			failure = errors.Join(failure, err)
		} else {
			r.routes = nil
		}
	}
	if failure != nil {
		return failure
	}
	if err := closeOptional(r.directory); err != nil {
		return err
	}
	r.directory = nil
	return nil
}
