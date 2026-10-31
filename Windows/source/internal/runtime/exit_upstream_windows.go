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
	"time"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/windns"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/libp2p/go-libp2p/core/peer"
)

type windowsExitAddresses interface {
	AddAddresses([]netip.Prefix) error
	RemoveAddresses([]netip.Prefix) error
}

type windowsExitOwnSession struct {
	request   InstallRequest
	token     uint64
	addresses windowsExitAddresses
	control   io.Closer
	routing   windowsClientRoutes
	stagedKey awg.Key
	ready     bool
}

type stagedWindowsExitUpstream struct {
	plane    *windowsExitDataPlane
	request  InstallRequest
	token    uint64
	mu       sync.Mutex
	closing  bool
	promoted bool
}

var _ SessionPromoter = (*stagedWindowsExitUpstream)(nil)
var _ SessionAWGKeyRotator = (*stagedWindowsExitUpstream)(nil)

func (p *windowsExitDataPlane) Install(ctx context.Context, r InstallRequest) (io.Closer, error) {
	if ctx == nil || r.LocalPeer != p.config.LocalPeer || r.RemotePeer == "" || r.RemotePeer == r.LocalPeer ||
		r.Transport == nil || r.ICE == nil || r.RemoteAWGKey == (awg.Key{}) || r.RemoteAWGKeyEpoch == 0 ||
		r.SelectedEndpoint.RemotePeer != r.RemotePeer || r.SelectedEndpoint.NetworkGeneration != r.Transport.ID() ||
		r.Lease.ClientPeer != r.LocalPeer || r.Lease.ExitPeer != r.RemotePeer || r.Lease.Session.IsZero() ||
		!r.Lease.IPv4.IsValid() || r.Lease.IPv6.IsValid() || p.config.IPv4Pool.Contains(r.Lease.IPv4.Addr()) ||
		p.config.Platform.Protection == nil {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := tunnellease.Validate(r.Lease, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := validateClientInstall(r); err != nil {
		return nil, err
	}
	if err := p.config.Bindings.AuthorizePromotion(windowsOwnBinding(r)); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing {
		return nil, ErrClosed
	}
	if p.next == ^uint64(0) {
		return nil, ErrGenerationStale
	}
	p.next++
	r.TunnelControl = slices.Clone(r.TunnelControl)
	return &stagedWindowsExitUpstream{plane: p, request: r, token: p.next}, nil
}

func windowsOwnBinding(r InstallRequest) ResolvedBinding {
	return ResolvedBinding{Binding: awgbinding.Binding{PeerID: r.RemotePeer, PublicKey: r.RemoteAWGKey, KeyEpoch: r.RemoteAWGKeyEpoch}, Key: r.RemoteAWGKey}
}

func (s *stagedWindowsExitUpstream) Promote(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return ErrClosed
	}
	p := s.plane
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.promoted {
		if p.upstream == nil || p.upstream.token != s.token || !p.upstream.ready {
			return ErrGenerationStale
		}
		return nil
	}
	if p.upstream != nil && p.upstream.token > s.token {
		return ErrGenerationStale
	}
	r := s.request
	if r.Transport != p.config.Transports.Current() {
		return ErrGenerationStale
	}
	if err := tunnellease.Validate(r.Lease, time.Now().UTC()); err != nil {
		return err
	}
	if err := p.config.Bindings.AuthorizePromotion(windowsOwnBinding(r)); err != nil {
		return err
	}
	addresses, ok := p.device.tun.(windowsExitAddresses)
	if !ok {
		return ErrInvalidConfig
	}
	if err := p.ensureGeneration(ctx, r.Transport); err != nil {
		return err
	}
	if err := p.closeUpstream(ctx); err != nil {
		return err
	}
	u := &windowsExitOwnSession{request: r, token: s.token, addresses: addresses}
	u.routing.protection = p.ownProtection
	p.upstream = u
	fail := func(err error) error {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(err, p.closeUpstream(cleanup))
	}
	if err := addresses.AddAddresses(leaseAddresses(r.Lease)); err != nil {
		return fail(err)
	}
	if err := p.active.AddUpstream(ctx, r.Lease, windowsOwnBinding(r), r.SelectedEndpoint, r.TunnelControlEndpoint); err != nil {
		return fail(err)
	}
	if err := p.active.transport.TriggerHandshake(r.RemoteAWGKey); err != nil {
		return fail(err)
	}
	if _, err := p.active.transport.WaitHandshake(ctx, r.RemoteAWGKey); err != nil {
		return fail(err)
	}
	if err := waitSelectedAWGEndpoint(ctx, p.active.transport, r.SelectedEndpoint.Address); err != nil {
		return fail(err)
	}
	if err := p.configureOwn(ctx, u); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	u.ready, s.promoted = true, true
	return nil
}

// openOwnRoutes records every partial owner before the next fallible activation step.
func (p *windowsExitDataPlane) openOwnRoutes(ctx context.Context, u *windowsExitOwnSession) error {
	path := filepath.Join(p.config.Platform.JournalDirectory, "upstream")
	directory, err := winfile.OpenDirectory(path, true)
	if err != nil {
		return err
	}
	u.routing.directory = directory
	routes, err := winroute.OpenTunnelRoutes(p.device.tun.LUID(), filepath.Join(path, "routes"))
	if err != nil {
		return err
	}
	u.routing.routes = &windowsExitOwnedRoutes{windowsSessionRoutes: routes, cleanup: windowsExitJournalCleanup{
		close: routes.Close, recover: func(ctx context.Context) error {
			return winroute.RecoverTunnelRoutes(ctx, filepath.Join(path, "routes"))
		},
	}}
	dns, err := windns.Open(p.device.tun.LUID(), filepath.Join(path, "dns"))
	if err != nil {
		return err
	}
	u.routing.dns = &windowsExitOwnedDNS{windowsSessionDNS: dns, cleanup: windowsExitJournalCleanup{
		close: dns.Close, recover: func(ctx context.Context) error { return windns.Recover(ctx, filepath.Join(path, "dns")) },
	}}
	r := u.request
	r.BeforeUserRoutes = func(ctx context.Context) (io.Closer, error) {
		control, err := u.request.BeforeUserRoutes(ctx)
		u.control = control
		return control, err
	}
	_, err = u.routing.activate(ctx, p.device.tun.LUID(), r, p.config.Platform.DNSServers)
	return err
}

// closeUpstream requires p.mu; the connection's renewer sends RELEASE before this resource is closed.
func (p *windowsExitDataPlane) closeUpstream(ctx context.Context) error {
	u := p.upstream
	if u == nil {
		return nil
	}
	u.ready = false
	if u.control != nil {
		if err := u.control.Close(); err != nil {
			return err
		}
		u.control = nil
	}
	if u.routing.protection != nil {
		if err := u.routing.protection.UpdateTunnel(ctx, 0, nil); err != nil {
			return err
		}
		u.routing.protection = nil
	}
	if u.routing.dns != nil {
		if err := u.routing.dns.Close(); err != nil {
			return err
		}
		u.routing.dns = nil
	}
	if u.routing.routes != nil {
		if err := u.routing.routes.Close(); err != nil {
			return err
		}
		u.routing.routes = nil
	}
	if u.routing.directory != nil {
		if err := u.routing.directory.Close(); err != nil {
			return err
		}
		u.routing.directory = nil
	}
	if p.active != nil {
		if u.stagedKey != (awg.Key{}) {
			if err := p.active.transport.RemovePeer(u.stagedKey); err != nil && !errors.Is(err, awg.ErrPeerNotFound) {
				return err
			}
			u.stagedKey = awg.Key{}
		}
		if err := p.active.RemoveUpstream(ctx, u.request.Lease.ID, u.request.Lease.Session); err != nil {
			return err
		}
	}
	if u.addresses != nil {
		if err := u.addresses.RemoveAddresses(leaseAddresses(u.request.Lease)); err != nil {
			return err
		}
		u.addresses = nil
	}
	p.upstream = nil
	return nil
}

func (s *stagedWindowsExitUpstream) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing = true
	p := s.plane
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.upstream == nil || p.upstream.token != s.token {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.closeUpstream(ctx)
}

func (s *stagedWindowsExitUpstream) StageRemoteAWGKey(oldKey, newKey awg.Key, remote peer.ID) error {
	if s == nil || remote == "" || oldKey == (awg.Key{}) || newKey == (awg.Key{}) || oldKey == newKey {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.plane
	p.mu.Lock()
	defer p.mu.Unlock()
	u := p.upstream
	if s.closing || !s.promoted || p.closing || !p.ready || u == nil || !u.ready || u.token != s.token || u.request.RemotePeer != remote {
		return ErrGenerationStale
	}
	if u.request.RemoteAWGKey == newKey {
		return nil
	}
	if u.request.RemoteAWGKey != oldKey || (u.stagedKey != (awg.Key{}) && u.stagedKey != newKey) {
		return ErrGenerationStale
	}
	if err := p.active.transport.StagePeerKeyRotation(oldKey, newKey, remote); err != nil {
		return err
	}
	u.stagedKey = newKey
	return p.active.transport.TriggerHandshake(newKey)
}

func (s *stagedWindowsExitUpstream) CommitRemoteAWGKey(ctx context.Context, oldKey, newKey awg.Key, remote peer.ID) error {
	if s == nil || ctx == nil || remote == "" || oldKey == (awg.Key{}) || newKey == (awg.Key{}) || oldKey == newKey {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.plane
	p.mu.Lock()
	defer p.mu.Unlock()
	u := p.upstream
	if s.closing || !s.promoted || p.closing || !p.ready || u == nil || !u.ready || u.token != s.token || u.request.RemotePeer != remote {
		return ErrGenerationStale
	}
	if u.request.RemoteAWGKey == newKey {
		return nil
	}
	if u.request.RemoteAWGKey != oldKey || u.stagedKey != newKey {
		return ErrGenerationStale
	}
	if _, err := p.active.transport.WaitHandshakeActive(ctx, newKey); err != nil {
		return err
	}
	g := p.active
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.upstream == nil || g.upstream.lease.ID != u.request.Lease.ID || g.upstream.lease.Session != u.request.Lease.Session {
		return ErrGenerationStale
	}
	if err := g.transport.FinalizePeerKeyRotation(oldKey, newKey, remote); err != nil {
		return err
	}
	g.upstream.binding.Key, g.upstream.binding.Binding.PublicKey = newKey, newKey
	u.request.RemoteAWGKey, u.stagedKey = newKey, awg.Key{}
	return nil
}
