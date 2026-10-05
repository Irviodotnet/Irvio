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
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/awg"
	"hydra.local/hydra/internal/linuxroute"
	"hydra.local/hydra/internal/networkstate"
	"hydra.local/hydra/internal/transportgen"
	"hydra.local/hydra/internal/tunnel"
	"hydra.local/hydra/internal/tunnellease"
)

type ClientDataPlaneConfig struct {
	LocalPeer      string
	PrivateKey     awg.Key
	TUNName        string
	MTU            int
	Obfuscation    awg.ObfuscationConfig
	OuterMark      uint32
	Bypass         []netip.Prefix
	HandshakeRetry uint32
	Transports     TransportLifecycle
}

type ClientDataPlane struct {
	config ClientDataPlaneConfig
	next   atomic.Uint64

	mu        sync.Mutex
	current   *installedClientDataPlane
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

type installedClientDataPlane struct {
	token      uint64
	remoteKey  awg.Key
	remotePeer string
	lease      tunnellease.Lease
	generation networkstate.Generation
	tun        *tunnel.Linux
	transport  *awg.Transport
	routing    *linuxroute.Manager
	control    io.Closer
}

type stagedClientDataPlane struct {
	owner   *ClientDataPlane
	request InstallRequest
	token   uint64

	mu        sync.Mutex
	promoted  bool
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func NewClientDataPlane(config ClientDataPlaneConfig) (*ClientDataPlane, error) {
	if config.LocalPeer == "" || config.PrivateKey == (awg.Key{}) || config.Transports == nil {
		return nil, ErrInvalidConfig
	}
	if config.TUNName == "" {
		config.TUNName = tunnel.DefaultName
	}
	if config.MTU == 0 {
		config.MTU = tunnel.DefaultMTU
	}
	if config.OuterMark == 0 {
		config.OuterMark = linuxroute.IrvioBypassMark
	}
	if config.MTU < 576 || config.MTU > 65535 || len(config.TUNName) > 15 {
		return nil, ErrInvalidConfig
	}
	config.Bypass = append([]netip.Prefix(nil), config.Bypass...)
	return &ClientDataPlane{config: config}, nil
}

// Install stages inputs; only Selector's Promote publishes routing and AWG resources.
func (p *ClientDataPlane) Install(ctx context.Context, request InstallRequest) (io.Closer, error) {
	if p == nil || ctx == nil || request.LocalPeer.String() != p.config.LocalPeer ||
		request.RemotePeer == "" || request.Transport == nil || request.ICE == nil ||
		request.RemoteAWGKey == (awg.Key{}) || request.RemoteAWGKeyEpoch == 0 ||
		request.SelectedEndpoint.RemotePeer != request.RemotePeer ||
		request.SelectedEndpoint.NetworkGeneration != request.Transport.ID() ||
		request.Lease.ClientPeer != request.LocalPeer || request.Lease.ExitPeer != request.RemotePeer {
		return nil, ErrInvalidConfig
	}
	if err := tunnellease.Validate(request.Lease, time.Now().UTC()); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	return &stagedClientDataPlane{owner: p, request: request, token: p.next.Add(1)}, nil
}

func (s *stagedClientDataPlane) Promote(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.promoted {
		return nil
	}
	if err := s.owner.promote(ctx, s); err != nil {
		return err
	}
	s.promoted = true
	return nil
}

func (s *stagedClientDataPlane) StageRemoteAWGKey(oldKey, newKey awg.Key, remote peer.ID) error {
	if s == nil || remote == "" {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.promoted {
		return ErrGenerationStale
	}
	return s.owner.stageRemoteAWGKey(s.token, oldKey, newKey, remote)
}

func (s *stagedClientDataPlane) CommitRemoteAWGKey(ctx context.Context, oldKey, newKey awg.Key, remote peer.ID) error {
	if s == nil || ctx == nil || remote == "" {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.promoted {
		return ErrGenerationStale
	}
	return s.owner.commitRemoteAWGKey(ctx, s.token, oldKey, newKey, remote)
}

func (p *ClientDataPlane) stageRemoteAWGKey(token uint64, oldKey, newKey awg.Key, remote peer.ID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.current
	if p.closed || current == nil || current.token != token || current.remotePeer != remote.String() {
		return ErrGenerationStale
	}
	if current.remoteKey == newKey {
		return nil
	}
	if current.remoteKey != oldKey {
		return ErrGenerationStale
	}
	if err := current.transport.StagePeerKeyRotation(oldKey, newKey, remote); err != nil {
		return err
	}
	return current.transport.TriggerHandshake(newKey)
}

func (p *ClientDataPlane) commitRemoteAWGKey(ctx context.Context, token uint64, oldKey, newKey awg.Key, remote peer.ID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.current
	if p.closed || current == nil || current.token != token || current.remotePeer != remote.String() {
		return ErrGenerationStale
	}
	if current.remoteKey == newKey {
		return nil
	}
	if current.remoteKey != oldKey {
		return ErrGenerationStale
	}
	if _, err := current.transport.WaitHandshakeActive(ctx, newKey); err != nil {
		return err
	}
	if err := current.transport.FinalizePeerKeyRotation(oldKey, newKey, remote); err != nil {
		return err
	}
	current.remoteKey = newKey
	return nil
}

func (p *ClientDataPlane) promote(ctx context.Context, staged *stagedClientDataPlane) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	request := staged.request
	currentGeneration := p.config.Transports.Current()
	if currentGeneration == nil || currentGeneration != request.Transport ||
		currentGeneration.ID() != request.SelectedEndpoint.NetworkGeneration {
		return ErrGenerationStale
	}
	old := p.current
	if old != nil && old.generation == request.Transport.ID() && sameLeasePath(old, request) {
		if err := p.updateSameGeneration(ctx, old, request); err != nil {
			return err
		}
		old.token = staged.token
		old.lease = request.Lease
		old.remotePeer = request.RemotePeer.String()
		old.remoteKey = request.RemoteAWGKey
		return nil
	}

	var owner *tunnel.Linux
	if old != nil && sameLogicalTunnel(old, request) {
		owner = old.tun
		old.tun = nil
	}
	if old != nil {
		p.current = nil
		if err := closeInstalledClient(old); err != nil {
			if owner != nil {
				_ = owner.Close()
			}
			return localPromotionFailure("close_previous_data_plane", err)
		}
	}
	if owner == nil {
		addresses := leaseAddresses(request.Lease)
		var err error
		owner, err = tunnel.NewLinux(tunnel.Config{Name: p.config.TUNName, MTU: p.config.MTU, Addresses: addresses})
		if err != nil {
			return localPromotionFailure("create_tun", err)
		}
	}
	installed, err := p.createInstalled(ctx, owner, staged.token, request)
	if err != nil {
		_ = owner.Close()
		return err
	}
	p.current = installed
	if err := p.config.Transports.Commit(request.Transport.ID()); err != nil {
		p.current = nil
		_ = closeInstalledClient(installed)
		return localPromotionFailure("commit_transport_generation", err)
	}
	return nil
}

func (p *ClientDataPlane) updateSameGeneration(ctx context.Context, current *installedClientDataPlane, request InstallRequest) error {
	if current.remoteKey == request.RemoteAWGKey {
		if err := current.transport.UpdatePeerEndpoint(request.RemoteAWGKey, request.SelectedEndpoint); err != nil {
			return err
		}
	} else {
		if err := current.transport.RemovePeer(current.remoteKey); err != nil {
			return err
		}
		if err := current.transport.AddPeer(clientPeerConfig(request)); err != nil {
			return err
		}
	}
	if err := current.transport.TriggerHandshake(request.RemoteAWGKey); err != nil {
		return err
	}
	if _, err := current.transport.WaitHandshake(ctx, request.RemoteAWGKey); err != nil {
		return err
	}
	if err := waitSelectedAWGEndpoint(ctx, current.transport, request.SelectedEndpoint.Address); err != nil {
		return err
	}
	if request.BeforeUserRoutes != nil {
		control, err := request.BeforeUserRoutes(ctx)
		if err != nil {
			return err
		}
		if control == nil {
			return ErrInvalidConfig
		}
		previous := current.control
		current.control = control
		if previous != nil {
			return previous.Close()
		}
	}
	return nil
}

func (p *ClientDataPlane) createInstalled(ctx context.Context, owner *tunnel.Linux, token uint64, request InstallRequest) (_ *installedClientDataPlane, resultErr error) {
	borrowed, err := owner.Borrow()
	if err != nil {
		return nil, localPromotionFailure("borrow_tun", err)
	}
	transport, err := awg.NewTransport(awg.Config{
		Mux: request.Transport.Mux(), NetworkGeneration: request.Transport.ID(),
		TUN: borrowed, PrivateKey: p.config.PrivateKey,
		Peers:       []awg.PeerConfig{clientPeerConfig(request)},
		Obfuscation: p.config.Obfuscation, HandshakeRetrySeconds: p.config.HandshakeRetry,
	})
	if err != nil {
		_ = borrowed.Close()
		return nil, localPromotionFailure("create_awg_transport", err)
	}
	installed := &installedClientDataPlane{
		token: token, remoteKey: request.RemoteAWGKey, remotePeer: request.RemotePeer.String(),
		lease: request.Lease, generation: request.Transport.ID(), tun: owner, transport: transport,
	}
	defer func() {
		if resultErr != nil {
			_ = closeInstalledClient(installed)
		}
	}()
	if err := transport.Bind().SetMark(p.config.OuterMark); err != nil {
		return nil, localPromotionFailure("mark_awg_transport", err)
	}
	if err := transport.TriggerHandshake(request.RemoteAWGKey); err != nil {
		return nil, err
	}
	if _, err := transport.WaitHandshake(ctx, request.RemoteAWGKey); err != nil {
		return nil, err
	}
	if err := waitSelectedAWGEndpoint(ctx, transport, request.SelectedEndpoint.Address); err != nil {
		return nil, err
	}
	routeConfig, err := clientRouteConfig(request.Transport, owner, request.Lease, p.config.OuterMark, p.config.Bypass)
	if err != nil {
		return nil, localPromotionFailure("derive_session_routes", err)
	}
	routeConfig.TunnelControl = append([]netip.Prefix(nil), request.TunnelControl...)
	installed.routing, installed.control, err = configureSessionRoutes(ctx, routeConfig, request.BeforeUserRoutes)
	if err != nil {
		return nil, err
	}
	return installed, nil
}

func clientPeerConfig(request InstallRequest) awg.PeerConfig {
	allowed := make([]netip.Prefix, 0, 2)
	if request.Lease.IPv4.IsValid() {
		allowed = append(allowed, netip.MustParsePrefix("0.0.0.0/0"))
	}
	if request.Lease.IPv6.IsValid() {
		allowed = append(allowed, netip.MustParsePrefix("::/0"))
	}
	return awg.PeerConfig{
		RemotePeer: request.RemotePeer, PublicKey: request.RemoteAWGKey,
		Endpoint: request.SelectedEndpoint, AllowedIPs: allowed,
	}
}

func leaseAddresses(lease tunnellease.Lease) []netip.Prefix {
	result := make([]netip.Prefix, 0, 2)
	if lease.IPv4.IsValid() {
		result = append(result, lease.IPv4)
	}
	if lease.IPv6.IsValid() {
		result = append(result, lease.IPv6)
	}
	return result
}

func sameLeasePath(current *installedClientDataPlane, request InstallRequest) bool {
	return current.remotePeer == request.RemotePeer.String() &&
		current.lease.ID == request.Lease.ID &&
		current.lease.IPv4 == request.Lease.IPv4 && current.lease.IPv6 == request.Lease.IPv6
}

func sameLogicalTunnel(current *installedClientDataPlane, request InstallRequest) bool {
	return current.lease.IPv4 == request.Lease.IPv4 && current.lease.IPv6 == request.Lease.IPv6
}

func waitSelectedAWGEndpoint(ctx context.Context, transport *awg.Transport, endpoint netip.AddrPort) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if transport.Bind().Stats().LastSend == endpoint {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func clientRouteConfig(generation *transportgen.Generation, owner *tunnel.Linux, lease tunnellease.Lease, mark uint32, bypass []netip.Prefix) (linuxroute.Config, error) {
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
	return p.current.routing.ReconcileWAN(ipv4, ipv6)
}

func (s *stagedClientDataPlane) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		promoted := s.promoted
		s.mu.Unlock()
		if promoted {
			s.closeErr = s.owner.deactivate(s.token)
		}
	})
	return s.closeErr
}

func (p *ClientDataPlane) deactivate(token uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == nil || p.current.token != token {
		return nil
	}
	current := p.current
	p.current = nil
	return closeInstalledClient(current)
}

func closeInstalledClient(current *installedClientDataPlane) error {
	if current == nil {
		return nil
	}
	return errors.Join(closeOptional(current.control), closeOptional(current.routing), closeOptional(current.transport), closeOptional(current.tun))
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

func closeOptional(value io.Closer) error {
	if value == nil {
		return nil
	}
	return value.Close()
}

func (p *ClientDataPlane) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		current := p.current
		p.current = nil
		p.mu.Unlock()
		p.closeErr = closeInstalledClient(current)
	})
	return p.closeErr
}

var _ SessionInstaller = (*ClientDataPlane)(nil)
var _ SessionPromoter = (*stagedClientDataPlane)(nil)
var _ SessionAWGKeyRotator = (*stagedClientDataPlane)(nil)
var _ NetworkReconciler = (*ClientDataPlane)(nil)
