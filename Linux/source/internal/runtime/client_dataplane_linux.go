// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"fmt"
	"io"
	"net/netip"

	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

type ClientDataPlanePlatform struct{}

func validateClientInstall(InstallRequest) error { return nil }

func normalizeClientPlatform(config *ClientDataPlaneConfig) error {
	if config.OuterMark == 0 {
		config.OuterMark = linuxroute.IrvioBypassMark
	}
	return nil
}

func newClientTunnel(config tunnel.Config) (tunnel.DeviceOwner, error) {
	return tunnel.NewLinux(config)
}

func (p *ClientDataPlane) configureRoutes(ctx context.Context, owner tunnel.DeviceOwner, request InstallRequest) (io.Closer, io.Closer, error) {
	routeConfig, err := clientRouteConfig(request.Transport, owner, request.Lease, p.config.OuterMark, p.config.Bypass)
	if err != nil {
		return nil, nil, localPromotionFailure("derive_session_routes", err)
	}
	routeConfig.TunnelControl = append([]netip.Prefix(nil), request.TunnelControl...)
	return configureSessionRoutes(ctx, routeConfig, request.BeforeUserRoutes)
}

func clientRouteConfig(generation *transportgen.Generation, owner tunnel.DeviceOwner, lease tunnellease.Lease, mark uint32, bypass []netip.Prefix) (linuxroute.Config, error) {
	config := linuxroute.DefaultConfig()
	config.TUNIndex, config.TUNName, config.OuterMark = owner.IfIndex(), owner.Name(), mark
	config.ProtectIPv4, config.ProtectIPv6 = lease.IPv4.IsValid(), lease.IPv6.IsValid()
	config.RejectIPv4, config.RejectIPv6 = !lease.IPv4.IsValid(), !lease.IPv6.IsValid()
	if lease.IPv4.IsValid() {
		config.TunnelIPv4Source = lease.IPv4.Addr()
	}
	if lease.IPv6.IsValid() {
		config.TunnelIPv6Source = lease.IPv6.Addr()
	}
	config.Bypass = append([]netip.Prefix(nil), bypass...)
	plan := generation.BindPlan()
	snapshot := generation.Snapshot()
	var err error
	if plan.IPv4.IsValid() {
		config.OuterIPv4 = true
		config.WANIPv4, err = wanPath(snapshot, networkstate.FamilyIPv4, plan.IPv4)
		if err != nil {
			return linuxroute.Config{}, err
		}
	}
	if plan.IPv6.IsValid() {
		config.OuterIPv6 = true
		config.WANIPv6, err = wanPath(snapshot, networkstate.FamilyIPv6, plan.IPv6)
		if err != nil {
			return linuxroute.Config{}, err
		}
	}
	return config, nil
}

func wanPath(snapshot networkstate.Snapshot, family networkstate.Family, source netip.Addr) (linuxroute.WANPath, error) {
	links := make(map[int]networkstate.Link, len(snapshot.Links))
	for _, link := range snapshot.Links {
		links[link.Index] = link
	}
	for _, route := range snapshot.Routes {
		if route.Family != family {
			continue
		}
		link, ok := links[route.LinkIndex]
		if !ok || !link.Up {
			continue
		}
		for _, prefix := range link.Addresses {
			if prefix.Addr() == source {
				return linuxroute.WANPath{
					LinkIndex: route.LinkIndex, Source: source, Gateway: route.Gateway,
					Connected: prefix.Masked(),
				}, nil
			}
		}
	}
	return linuxroute.WANPath{}, fmt.Errorf("derive %s Irvio WAN route: %w", family, ErrInvalidConfig)
}

func networkWANPaths(state networkstate.State) (linuxroute.WANPath, linuxroute.WANPath, error) {
	plan, err := (transportgen.PreferredBindPlanner{}).Plan(state.Snapshot)
	if err != nil {
		return linuxroute.WANPath{}, linuxroute.WANPath{}, err
	}
	var ipv4, ipv6 linuxroute.WANPath
	if plan.IPv4.IsValid() {
		ipv4, err = wanPath(state.Snapshot, networkstate.FamilyIPv4, plan.IPv4)
		if err != nil {
			return linuxroute.WANPath{}, linuxroute.WANPath{}, err
		}
	}
	if plan.IPv6.IsValid() {
		ipv6, err = wanPath(state.Snapshot, networkstate.FamilyIPv6, plan.IPv6)
		if err != nil {
			return linuxroute.WANPath{}, linuxroute.WANPath{}, err
		}
	}
	return ipv4, ipv6, nil
}

// ReconcileNetwork moves marked egress without replacing the logical tunnel or AWG session.
func (p *ClientDataPlane) ReconcileNetwork(_ context.Context, state networkstate.State) error {
	if p == nil || state.Generation == 0 {
		return ErrInvalidConfig
	}
	ipv4, ipv6, err := networkWANPaths(state)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if p.current == nil || p.current.routing == nil {
		return nil
	}
	return p.current.routing.(*linuxroute.Manager).ReconcileWAN(ipv4, ipv6)
}

func configureSessionRoutes(ctx context.Context, full linuxroute.Config, before func(context.Context) (io.Closer, error)) (*linuxroute.Manager, io.Closer, error) {
	if before == nil {
		routing, err := linuxroute.Apply(full)
		return routing, nil, localPromotionFailure("install_session_routes", err)
	}
	bootstrap := full
	bootstrap.ProtectIPv4, bootstrap.ProtectIPv6 = false, false
	bootstrap.RejectIPv4, bootstrap.RejectIPv6 = true, true
	routing, err := linuxroute.Apply(bootstrap)
	if err != nil {
		return nil, nil, localPromotionFailure("install_bootstrap_routes", err)
	}
	control, err := before(ctx)
	if err != nil || control == nil {
		_ = routing.Close()
		if err == nil {
			err = ErrInvalidConfig
		}
		return nil, nil, err
	}
	if err := routing.Close(); err != nil {
		_ = control.Close()
		return nil, nil, localPromotionFailure("replace_bootstrap_routes", err)
	}
	routing, err = linuxroute.Apply(full)
	if err != nil {
		_ = control.Close()
		return nil, nil, localPromotionFailure("install_session_routes", err)
	}
	return routing, control, nil
}

var _ NetworkReconciler = (*ClientDataPlane)(nil)
