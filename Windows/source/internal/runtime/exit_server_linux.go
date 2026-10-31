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

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/exitfirewall"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/ipforward"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

func NewExitServer(ctx context.Context, config ExitServerConfig) (*ExitServer, error) {
	return newExitServer(ctx, config, func(ctx context.Context, c ExitServerConfig) (exitServerPlane, error) {
		return newExitDataPlane(ctx, c)
	})
}

type activeExitResource struct {
	lease     tunnellease.Lease
	transport *awg.Transport
	key       awg.Key
	ice       *iceconnect.ConnectedSession
	attempt   *iceconnect.Attempt
	revoking  bool
}

type exitDataPlane struct {
	config     ExitServerConfig
	tun        *tunnel.Linux
	forwarding *ipforward.Manager
	firewall   exitfirewall.Installation

	mu           sync.Mutex
	transport    *awg.Transport
	generation   networkstate.Generation
	pending      map[tunnellease.SessionToken]*pendingExitSession
	resources    map[tunnellease.ID]*activeExitResource
	upstream     *dualRoleUpstream
	upstreamNext atomic.Uint64
	natIPv4      netip.Addr
	natIPv6      netip.Addr
	changed      chan struct{}
	closed       bool
}

type dualRoleUpstream struct {
	token      uint64
	remote     peer.ID
	key        awg.Key
	lease      tunnellease.Lease
	generation networkstate.Generation
	routing    *linuxroute.Manager
	control    io.Closer
	controlDst []netip.Prefix
}

type stagedDualRoleUpstream struct {
	plane   *exitDataPlane
	request InstallRequest
	token   uint64

	mu        sync.Mutex
	promoted  bool
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func newExitDataPlane(ctx context.Context, config ExitServerConfig) (_ *exitDataPlane, resultErr error) {
	owner, err := tunnel.NewLinux(tunnel.Config{Name: config.TUNName, MTU: tunnel.DefaultMTU, Addresses: config.TUNAddresses})
	if err != nil {
		return nil, err
	}
	plane := &exitDataPlane{
		config: config, tun: owner,
		pending:   make(map[tunnellease.SessionToken]*pendingExitSession, config.MaxPending),
		resources: make(map[tunnellease.ID]*activeExitResource, config.MaxPending),
		changed:   make(chan struct{}),
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, plane.Close())
		}
	}()
	plane.forwarding, err = ipforward.Enable(ctx, ipforward.Config{IPv4: config.IPv4Pool.IsValid(), IPv6: config.IPv6Pool.IsValid()})
	if err != nil {
		return nil, err
	}
	policy := exitfirewall.Policy{
		TUNName: owner.Name(), WANName: config.WANName,
		ClientIPv4: config.IPv4Pool, ClientIPv6: config.IPv6Pool,
		BlockedDestinations: append([]netip.Prefix(nil), config.Management...),
	}
	if config.M12Enabled {
		for _, prefix := range config.TUNAddresses {
			policy.SessionControl = append(policy.SessionControl, netip.AddrPortFrom(prefix.Addr(), config.SessionControlPort))
		}
	}
	if current := config.Transports.Current(); current != nil {
		plan := current.BindPlan()
		policy.WANIPv4, policy.WANIPv6 = plan.IPv4, plan.IPv6
		plane.natIPv4, plane.natIPv6 = plan.IPv4, plan.IPv6
	}
	plane.firewall, err = (exitfirewall.NFTBackend{}).Apply(ctx, policy)
	if err != nil {
		return nil, err
	}
	return plane, nil
}

func (p *exitDataPlane) registerPending(pending *pendingExitSession) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if len(p.pending) >= p.config.MaxPending {
		return fmt.Errorf("Exit pending ICE capacity: %w", tunnellease.ErrExhausted)
	}
	if previous := p.pending[pending.token]; previous != nil {
		return ErrGenerationStale
	}
	p.pending[pending.token] = pending
	close(p.changed)
	p.changed = make(chan struct{})
	return nil
}

func (p *exitDataPlane) expirePending(cutoff time.Time) {
	p.mu.Lock()
	var expired []*pendingExitSession
	for token, pending := range p.pending {
		if pending.createdAt.Before(cutoff) {
			expired = append(expired, pending)
			delete(p.pending, token)
		}
	}
	p.mu.Unlock()
	for _, pending := range expired {
		_ = pending.ice.Close()
	}
}

func (p *exitDataPlane) ActivateLease(ctx context.Context, lease tunnellease.Lease) (resultErr error) {
	defer func() {
		if resultErr != nil {
			p.config.Logger.Warn("TUNNEL_LEASE_ACTIVATION_FAILED", "client_peer_id", lease.ClientPeer,
				"lease_id", lease.ID.String(), "reason", resultErr)
		}
	}()
	if ctx == nil || lease.Session.IsZero() {
		return ErrInvalidConfig
	}
	pending, err := p.takePending(ctx, lease.Session, lease.ClientPeer)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = pending.ice.Close()
		return ErrClosed
	}
	current := p.config.Transports.Current()
	if current == nil || current.ID() != pending.endpoint.NetworkGeneration {
		_ = pending.ice.Close()
		return ErrGenerationStale
	}
	if err := p.ensureTransportLocked(current); err != nil {
		_ = pending.ice.Close()
		return err
	}
	if err := p.config.Bindings.AuthorizePromotion(ResolvedBinding{
		Binding: awgbinding.Binding{PeerID: pending.remote, PublicKey: pending.key, KeyEpoch: pending.keyEpoch},
		Key:     pending.key,
	}); err != nil {
		_ = pending.ice.Close()
		return err
	}
	if err := p.transport.AddPeer(awg.PeerConfig{
		RemotePeer: pending.remote, PublicKey: pending.key, Endpoint: pending.endpoint,
		AllowedIPs: leaseAddresses(lease),
	}); err != nil {
		_ = pending.ice.Close()
		return err
	}
	resource := &activeExitResource{
		lease: lease, transport: p.transport, key: pending.key,
		ice: pending.ice, attempt: pending.attempt,
	}
	p.resources[lease.ID] = resource
	if p.upstream != nil {
		routes, routeErr := p.tunnelControlRoutesLocked(nil)
		if routeErr == nil {
			routeErr = p.upstream.routing.ReconcileTunnelControl(routes)
		}
		if routeErr != nil {
			delete(p.resources, lease.ID)
			removeErr := p.transport.RemovePeer(pending.key)
			if errors.Is(removeErr, awg.ErrPeerNotFound) || errors.Is(removeErr, awg.ErrClosed) {
				removeErr = nil
			}
			return errors.Join(routeErr, removeErr, pending.ice.Close())
		}
	}
	p.config.Logger.Info("TUNNEL_LEASE_DATA_PLANE_ACTIVATED", "client_peer_id", lease.ClientPeer,
		"lease_id", lease.ID.String(), "selected_endpoint", pending.endpoint.Address)
	return nil
}

func (p *exitDataPlane) takePending(ctx context.Context, token tunnellease.SessionToken, remote peer.ID) (*pendingExitSession, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, ErrClosed
		}
		if pending := p.pending[token]; pending != nil {
			if pending.remote != remote {
				p.mu.Unlock()
				return nil, tunnellease.ErrWrongPeer
			}
			delete(p.pending, token)
			p.mu.Unlock()
			return pending, nil
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (p *exitDataPlane) ensureTransportLocked(generation *transportgen.Generation) error {
	if p.transport != nil && p.generation == generation.ID() {
		return nil
	}
	if p.transport != nil {
		if p.upstream != nil {
			if err := p.closeUpstreamLocked(p.upstream); err != nil {
				return err
			}
		}
		if err := p.transport.Close(); err != nil {
			return err
		}
		p.transport = nil
	}
	borrowed, err := p.tun.Borrow()
	if err != nil {
		return err
	}
	transport, err := awg.NewTransport(awg.Config{
		Mux: generation.Mux(), NetworkGeneration: generation.ID(), TUN: borrowed,
		PrivateKey: p.config.PrivateKey, Obfuscation: p.config.Obfuscation,
		MaxPeers: awg.DefaultMaxPeers,
	})
	if err != nil {
		_ = borrowed.Close()
		return err
	}
	if err := transport.Bind().SetMark(linuxroute.IrvioBypassMark); err != nil {
		_ = transport.Close()
		return err
	}
	p.transport, p.generation = transport, generation.ID()
	return p.config.Transports.Commit(generation.ID())
}

// Install stages the upstream peer on the shared Exit-owned TUN/AWG without disrupting downstream peers.
func (p *exitDataPlane) Install(_ context.Context, request InstallRequest) (io.Closer, error) {
	if request.LocalPeer.String() != p.config.LocalPeer.String() || request.RemotePeer == "" || request.Transport == nil ||
		request.ICE == nil || request.RemoteAWGKey == (awg.Key{}) || request.RemoteAWGKeyEpoch == 0 ||
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
	return &stagedDualRoleUpstream{plane: p, request: request, token: p.upstreamNext.Add(1)}, nil
}

func (s *stagedDualRoleUpstream) Promote(ctx context.Context) error {
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
	if err := s.plane.promoteUpstream(ctx, s.request, s.token); err != nil {
		return err
	}
	s.promoted = true
	return nil
}

func (s *stagedDualRoleUpstream) StageRemoteAWGKey(oldKey, newKey awg.Key, remote peer.ID) error {
	if s == nil || remote == "" {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.promoted {
		return ErrGenerationStale
	}
	p := s.plane
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.upstream
	if p.closed || current == nil || current.token != s.token || current.remote != remote {
		return ErrGenerationStale
	}
	if current.key == newKey {
		return nil
	}
	if current.key != oldKey {
		return ErrGenerationStale
	}
	if err := p.transport.StagePeerKeyRotation(oldKey, newKey, remote); err != nil {
		return err
	}
	return p.transport.TriggerHandshake(newKey)
}

func (s *stagedDualRoleUpstream) CommitRemoteAWGKey(ctx context.Context, oldKey, newKey awg.Key, remote peer.ID) error {
	if s == nil || ctx == nil || remote == "" {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.promoted {
		return ErrGenerationStale
	}
	p := s.plane
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.upstream
	if p.closed || current == nil || current.token != s.token || current.remote != remote {
		return ErrGenerationStale
	}
	if current.key == newKey {
		return nil
	}
	if current.key != oldKey {
		return ErrGenerationStale
	}
	if _, err := p.transport.WaitHandshakeActive(ctx, newKey); err != nil {
		return err
	}
	if err := p.transport.FinalizePeerKeyRotation(oldKey, newKey, remote); err != nil {
		return err
	}
	current.key = newKey
	return nil
}

func (p *exitDataPlane) promoteUpstream(ctx context.Context, request InstallRequest, token uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	current := p.config.Transports.Current()
	if current == nil || current != request.Transport || current.ID() != request.SelectedEndpoint.NetworkGeneration {
		return ErrGenerationStale
	}
	if err := p.ensureTransportLocked(current); err != nil {
		return err
	}
	if previous := p.upstream; previous != nil {
		if previous.generation == current.ID() && previous.remote == request.RemotePeer && previous.key == request.RemoteAWGKey &&
			previous.lease.ID == request.Lease.ID {
			if err := p.transport.UpdatePeerEndpoint(previous.key, request.SelectedEndpoint); err != nil {
				return err
			}
			if err := p.transport.TriggerHandshake(previous.key); err != nil {
				return err
			}
			if _, err := p.transport.WaitHandshake(ctx, previous.key); err != nil {
				return err
			}
			oldDestinations := previous.controlDst
			previous.controlDst = append([]netip.Prefix(nil), request.TunnelControl...)
			routes, err := p.tunnelControlRoutesLocked(nil)
			if err == nil {
				err = previous.routing.ReconcileTunnelControl(routes)
			}
			if err != nil {
				previous.controlDst = oldDestinations
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
				oldControl := previous.control
				previous.control = control
				if oldControl != nil {
					if err := oldControl.Close(); err != nil {
						return err
					}
				}
			}
			previous.token, previous.lease = token, request.Lease
			return nil
		}
		if err := p.closeUpstreamLocked(previous); err != nil {
			return err
		}
	}
	addresses := leaseAddresses(request.Lease)
	if err := p.tun.AddAddresses(addresses); err != nil {
		return err
	}
	configured := false
	defer func() {
		if !configured {
			_ = p.tun.RemoveAddresses(addresses)
		}
	}()
	if err := p.transport.AddPeer(clientPeerConfig(request)); err != nil {
		return err
	}
	peerAdded := true
	defer func() {
		if !configured && peerAdded {
			_ = p.transport.RemovePeer(request.RemoteAWGKey)
		}
	}()
	if err := p.transport.TriggerHandshake(request.RemoteAWGKey); err != nil {
		return err
	}
	if _, err := p.transport.WaitHandshake(ctx, request.RemoteAWGKey); err != nil {
		return err
	}
	if err := waitSelectedAWGEndpoint(ctx, p.transport, request.SelectedEndpoint.Address); err != nil {
		return err
	}
	localAddresses := leaseAddressesOnly(request.Lease)
	if err := p.firewall.SetLocalTunnelAddresses(ctx, localAddresses); err != nil {
		return err
	}
	firewallConfigured := true
	defer func() {
		if !configured && firewallConfigured {
			_ = p.firewall.SetLocalTunnelAddresses(context.Background(), nil)
		}
	}()
	routeConfig, err := clientRouteConfig(request.Transport, p.tun, request.Lease, linuxroute.IrvioBypassMark, p.config.Bypass)
	if err != nil {
		return err
	}
	routeConfig.ForwardedTUNName = p.tun.Name()
	routes, err := p.tunnelControlRoutesLocked(&request)
	if err != nil {
		return err
	}
	routeConfig.TunnelControl = nil
	routeConfig.TunnelControlRoutes = routes
	routing, control, err := configureSessionRoutes(ctx, routeConfig, request.BeforeUserRoutes)
	if err != nil {
		return err
	}
	p.upstream = &dualRoleUpstream{
		token: token, remote: request.RemotePeer, key: request.RemoteAWGKey,
		lease: request.Lease, generation: current.ID(), routing: routing, control: control,
		controlDst: append([]netip.Prefix(nil), request.TunnelControl...),
	}
	configured = true
	return nil
}

func (p *exitDataPlane) closeUpstreamLocked(value *dualRoleUpstream) error {
	if value == nil {
		return nil
	}
	removeErr := p.transport.RemovePeer(value.key)
	if errors.Is(removeErr, awg.ErrPeerNotFound) || errors.Is(removeErr, awg.ErrClosed) {
		removeErr = nil
	}
	result := errors.Join(
		closeOptional(value.control), p.firewall.SetLocalTunnelAddresses(context.Background(), nil),
		value.routing.Close(), removeErr, p.tun.RemoveAddresses(leaseAddresses(value.lease)),
	)
	if result == nil {
		p.upstream = nil
	}
	return result
}

func leaseAddressesOnly(lease tunnellease.Lease) []netip.Addr {
	result := make([]netip.Addr, 0, 2)
	if lease.IPv4.IsValid() {
		result = append(result, lease.IPv4.Addr())
	}
	if lease.IPv6.IsValid() {
		result = append(result, lease.IPv6.Addr())
	}
	return result
}

// tunnelControlRoutesLocked requires p.mu and chooses each upstream/downstream lease's exact source.
func (p *exitDataPlane) tunnelControlRoutesLocked(proposed *InstallRequest) ([]linuxroute.TunnelControlRoute, error) {
	routes := make([]linuxroute.TunnelControlRoute, 0, 2+2*len(p.resources))
	appendUpstream := func(destinations []netip.Prefix, lease tunnellease.Lease) error {
		for _, destination := range destinations {
			source := lease.IPv6.Addr()
			if destination.Addr().Is4() {
				source = lease.IPv4.Addr()
			}
			if !source.IsValid() || source.Is4() != destination.Addr().Is4() {
				return ErrInvalidConfig
			}
			routes = append(routes, linuxroute.TunnelControlRoute{Destination: destination, Source: source})
		}
		return nil
	}
	if proposed != nil {
		if err := appendUpstream(proposed.TunnelControl, proposed.Lease); err != nil {
			return nil, err
		}
	} else if p.upstream != nil {
		if err := appendUpstream(p.upstream.controlDst, p.upstream.lease); err != nil {
			return nil, err
		}
	}
	for _, resource := range p.resources {
		if resource.revoking {
			continue
		}
		for _, destination := range leaseAddresses(resource.lease) {
			source, err := p.exitControlSource(destination.Addr())
			if err != nil {
				return nil, err
			}
			routes = append(routes, linuxroute.TunnelControlRoute{Destination: destination, Source: source})
		}
	}
	return routes, nil
}

func (p *exitDataPlane) exitControlSource(client netip.Addr) (netip.Addr, error) {
	for _, prefix := range p.config.TUNAddresses {
		if prefix.Addr().Is4() == client.Is4() && prefix.Contains(client) {
			return prefix.Addr(), nil
		}
	}
	return netip.Addr{}, ErrInvalidConfig
}

func (s *stagedDualRoleUpstream) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		promoted := s.promoted
		s.mu.Unlock()
		if promoted {
			s.plane.mu.Lock()
			if current := s.plane.upstream; current != nil && current.token == s.token {
				s.closeErr = s.plane.closeUpstreamLocked(current)
			}
			s.plane.mu.Unlock()
		}
	})
	return s.closeErr
}

func (p *exitDataPlane) RenewLease(_ context.Context, previous, renewed tunnellease.Lease) error {
	if previous.ID != renewed.ID || previous.ClientPeer != renewed.ClientPeer || previous.ExitPeer != renewed.ExitPeer ||
		previous.Session != renewed.Session || previous.IPv4 != renewed.IPv4 || previous.IPv6 != renewed.IPv6 ||
		renewed.Generation <= previous.Generation {
		return tunnellease.ErrStaleGeneration
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	resource := p.resources[previous.ID]
	if resource == nil {
		return tunnellease.ErrUnknownLease
	}
	if resource.lease.Generation != previous.Generation || resource.lease.Session != previous.Session {
		return tunnellease.ErrStaleGeneration
	}
	resource.lease = renewed
	return nil
}

func (p *exitDataPlane) RevokeLease(_ context.Context, lease tunnellease.Lease, _ tunnellease.RevocationReason) error {
	p.mu.Lock()
	resource := p.resources[lease.ID]
	if resource != nil && resource.lease.Generation != lease.Generation {
		p.mu.Unlock()
		return tunnellease.ErrStaleGeneration
	}
	if resource != nil {
		resource.revoking = true
		if p.upstream != nil {
			routes, routeErr := p.tunnelControlRoutesLocked(nil)
			if routeErr == nil {
				routeErr = p.upstream.routing.ReconcileTunnelControl(routes)
			}
			if routeErr != nil {
				resource.revoking = false
				p.mu.Unlock()
				return routeErr
			}
		}
	}
	p.mu.Unlock()
	if resource == nil {
		return p.discardPending(lease.Session, lease.ClientPeer)
	}
	p.logAWGResource("TUNNEL_LEASE_DATA_PLANE_REVOKED", resource)
	removeErr := resource.transport.RemovePeer(resource.key)
	if errors.Is(removeErr, awg.ErrPeerNotFound) || errors.Is(removeErr, awg.ErrClosed) {
		removeErr = nil
	}
	closeErr := resource.ice.Close()
	if result := errors.Join(removeErr, closeErr); result != nil {
		return result
	}
	p.mu.Lock()
	if current := p.resources[lease.ID]; current == resource && current.lease.Generation == lease.Generation {
		delete(p.resources, lease.ID)
	}
	p.mu.Unlock()
	return nil
}

func (p *exitDataPlane) rotationTarget() (tunnellease.Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.transport == nil {
		return tunnellease.Lease{}, ErrClosed
	}
	// A private AWG key spans the device; M12 coordinates one relationship, not fleet-wide rekeying.
	if p.upstream != nil || len(p.resources) != 1 || len(p.pending) != 0 {
		return tunnellease.Lease{}, tunnellease.ErrExhausted
	}
	for _, resource := range p.resources {
		return resource.lease, nil
	}
	return tunnellease.Lease{}, tunnellease.ErrUnknownLease
}

func (p *exitDataPlane) applyLocalAWGRotation(ctx context.Context, target tunnellease.Lease, privateKey awg.Key) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.transport == nil || p.upstream != nil || len(p.resources) != 1 {
		return tunnellease.ErrStaleGeneration
	}
	resource := p.resources[target.ID]
	if resource == nil || resource.lease.ClientPeer != target.ClientPeer || resource.lease.Session != target.Session {
		return tunnellease.ErrStaleGeneration
	}
	if err := p.transport.PrepareHandshake(resource.key); err != nil {
		return err
	}
	if err := p.transport.UpdatePrivateKey(privateKey); err != nil {
		return err
	}
	p.config.Logger.Info("AWG_DIRECT_ROTATION_EXIT_KEY_INSTALLED", "client_peer_id", target.ClientPeer)
	if _, err := p.transport.WaitHandshake(ctx, resource.key); err != nil {
		p.config.Logger.Warn("AWG_DIRECT_ROTATION_EXIT_HANDSHAKE_FAILED", "client_peer_id", target.ClientPeer, "reason", err)
		return err
	}
	p.config.Logger.Info("AWG_DIRECT_ROTATION_EXIT_HANDSHAKE", "client_peer_id", target.ClientPeer)
	p.config.PrivateKey = privateKey
	return nil
}

func (p *exitDataPlane) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	close(p.changed)
	pending := p.pending
	resources := p.resources
	upstream := p.upstream
	transport := p.transport
	p.pending = nil
	p.resources = nil
	p.transport = nil
	p.upstream = nil
	p.mu.Unlock()
	var result error
	for _, value := range pending {
		result = errors.Join(result, value.ice.Close())
	}
	for _, value := range resources {
		p.logAWGResource("TUNNEL_LEASE_DATA_PLANE_CLOSED", value)
		result = errors.Join(result, value.ice.Close())
	}
	if upstream != nil {
		result = errors.Join(result, closeOptional(upstream.control), upstream.routing.Close(), p.tun.RemoveAddresses(leaseAddresses(upstream.lease)))
	}
	return errors.Join(result, closeOptional(transport), closeOptional(p.firewall), closeOptional(p.forwarding), closeOptional(p.tun))
}

func (p *exitDataPlane) logAWGResource(event string, resource *activeExitResource) {
	if p == nil || resource == nil || resource.transport == nil {
		return
	}
	bind := resource.transport.Bind().Stats()
	peerStats, peerErr := resource.transport.PeerStats(resource.key)
	p.config.Logger.Info(event, "client_peer_id", resource.lease.ClientPeer, "lease_id", resource.lease.ID.String(),
		"bind_send_packets", bind.SendPackets, "bind_receive_packets", bind.ReceivePackets,
		"peer_tx_bytes", peerStats.TransmitBytes, "peer_rx_bytes", peerStats.ReceiveBytes, "stats_error", peerErr)
}

var _ tunnellease.ResourceActivator = (*exitDataPlane)(nil)
var _ tunnellease.ResourceRenewer = (*exitDataPlane)(nil)
var _ tunnellease.ResourceRevoker = (*exitDataPlane)(nil)
var _ SessionInstaller = (*exitDataPlane)(nil)
var _ SessionAWGKeyRotator = (*stagedDualRoleUpstream)(nil)

// ReconcileNetwork updates upstream-owned marked WAN routes without disturbing downstream resources.
func (p *exitDataPlane) ReconcileNetwork(ctx context.Context, state networkstate.State) error {
	if p == nil || ctx == nil || state.Generation == 0 {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
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
	if p.config.IPv4Pool.IsValid() && !ipv4.Source.IsValid() || p.config.IPv6Pool.IsValid() && !ipv6.Source.IsValid() {
		return ErrInvalidConfig
	}
	previousIPv4, previousIPv6 := p.natIPv4, p.natIPv6
	previousWAN := p.config.WANName
	wanName := ""
	for _, path := range []linuxroute.WANPath{ipv4, ipv6} {
		if !path.Source.IsValid() {
			continue
		}
		for _, link := range state.Snapshot.Links {
			if link.Index != path.LinkIndex || !link.Up || link.Name == "" {
				continue
			}
			if wanName != "" && wanName != link.Name {
				return fmt.Errorf("reconcile Exit WAN: IPv4 and IPv6 use different interfaces: %w", ErrInvalidConfig)
			}
			wanName = link.Name
		}
	}
	if wanName == "" {
		return fmt.Errorf("reconcile Exit WAN: no usable interface: %w", ErrInvalidConfig)
	}
	reconciler, canReconcileWAN := p.firewall.(exitfirewall.WANPathReconciler)
	if wanName != previousWAN && !canReconcileWAN {
		return fmt.Errorf("reconcile Exit WAN interface: %w", ErrInvalidConfig)
	}
	var applyErr error
	if wanName != previousWAN {
		applyErr = reconciler.SetWANPath(ctx, wanName, ipv4.Source, ipv6.Source)
	} else {
		applyErr = p.firewall.SetNATAddresses(ctx, ipv4.Source, ipv6.Source)
	}
	if applyErr != nil {
		return applyErr
	}
	if p.upstream != nil && p.upstream.routing != nil {
		if err := p.upstream.routing.ReconcileWAN(ipv4, ipv6); err != nil {
			var rollback error
			if wanName != previousWAN {
				rollback = reconciler.SetWANPath(context.Background(), previousWAN, previousIPv4, previousIPv6)
			} else {
				rollback = p.firewall.SetNATAddresses(context.Background(), previousIPv4, previousIPv6)
			}
			return errors.Join(err, rollback)
		}
	}
	p.config.WANName = wanName
	p.natIPv4, p.natIPv6 = ipv4.Source, ipv6.Source
	return nil
}

var _ NetworkReconciler = (*exitDataPlane)(nil)
