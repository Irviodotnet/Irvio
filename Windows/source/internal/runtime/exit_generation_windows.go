// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/exitbridge"
	"github.com/Irviodotnet/Irvio/internal/exitnat"
	"github.com/Irviodotnet/Irvio/internal/socketpolicy"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/windivert"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

type windowsExitPeer struct {
	lease     tunnellease.Lease
	binding   ResolvedBinding
	endpoint  awg.SelectedEndpoint
	ready     bool
	installed bool
}

type windowsExitUpstream struct {
	windowsExitPeer
	attachment *exitbridge.Peer
}

type windowsExitBorrow struct {
	tun.Device
	mu     sync.Mutex
	closed bool
}

func (b *windowsExitBorrow) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	if err := b.Device.Close(); err != nil {
		return err
	}
	b.closed = true
	return nil
}

// windowsExitGeneration owns one AWG/bridge/NAT lifetime; its caller owns ICE, routes and the adapter.
type windowsExitGeneration struct {
	config     ExitServerConfig
	generation *transportgen.Generation
	resources  *exitnat.BridgedResources
	transport  *awg.Transport
	borrowed   tun.Device
	mu         sync.Mutex
	peers      map[tunnellease.ID]*windowsExitPeer
	upstream   *windowsExitUpstream
	closing    bool
}

func newWindowsExitGeneration(ctx context.Context, c ExitServerConfig, owner tunnel.DeviceOwner, generation *transportgen.Generation, module *windivert.Module, local []netip.Addr, traffic *exitbridge.Traffic) (*windowsExitGeneration, error) {
	if generation == nil || module == nil {
		return nil, ErrInvalidConfig
	}
	binding, err := winroute.ForSource(generation.BindPlan().IPv4)
	if err != nil {
		return nil, err
	}
	nat := exitnat.DefaultConfig(exitnat.Path{Generation: uint64(generation.ID()), Interface: binding.LUID, Source: binding.Source}, nil)
	nat.LocalAddresses = append([]netip.Addr(nil), local...)
	nat.Blocked = append([]netip.Prefix(nil), c.Management...)
	nat.MaxLeases = min(c.MaxPending, awg.DefaultMaxPeers)
	return openWindowsExitGeneration(ctx, c, owner, generation, func(deliver exitnat.Emit) (*exitnat.Backend, error) {
		return exitnat.NewWindowsBackend(nat, module, deliver)
	}, traffic)
}

// openWindowsExitGeneration retains partial owners on rollback failure, including the borrowed TUN.
func openWindowsExitGeneration(ctx context.Context, c ExitServerConfig, owner tunnel.DeviceOwner, generation *transportgen.Generation, backend func(exitnat.Emit) (*exitnat.Backend, error), traffic *exitbridge.Traffic) (*windowsExitGeneration, error) {
	if ctx == nil || owner == nil || generation == nil || backend == nil || c.LocalPeer == "" ||
		c.PrivateKey == (awg.Key{}) || c.Bindings.Registry == nil || c.MaxPending < 1 ||
		len(c.TUNAddresses) != 1 || !c.M12Enabled || c.SessionControlPort == 0 || c.IPv6Pool.IsValid() ||
		!c.IPv4Pool.IsValid() || !c.IPv4Pool.Addr().Is4() || c.IPv4Pool != c.TUNAddresses[0].Masked() ||
		!slices.Contains(owner.Addresses(), c.TUNAddresses[0]) {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if generation.Lifecycle() != transportgen.LifecycleReady {
		return nil, ErrGenerationStale
	}
	g := &windowsExitGeneration{config: c, generation: generation, peers: make(map[tunnellease.ID]*windowsExitPeer)}
	borrowed, err := owner.Borrow()
	if err != nil {
		return nil, err
	}
	g.borrowed = &windowsExitBorrow{Device: borrowed}
	fail := func(cause error) (*windowsExitGeneration, error) {
		if cleanup := g.Close(context.Background()); cleanup != nil {
			return g, errors.Join(cause, cleanup)
		}
		return nil, cause
	}
	g.resources, err = exitnat.NewBridgedResources(exitnat.BridgedConfig{
		TUN: g.borrowed, Control: netip.AddrPortFrom(c.TUNAddresses[0].Addr(), c.SessionControlPort),
		Limit: awg.DefaultMaxPeers, Backend: backend, Install: g.install, Remove: g.remove, Traffic: traffic,
	})
	if err != nil {
		if g.resources != nil {
			return g, err
		}
		return fail(err)
	}
	g.transport, err = awg.NewTransport(awg.Config{
		Mux: generation.Mux(), NetworkGeneration: generation.ID(), TUN: g.resources.Bridge(),
		PrivateKey: c.PrivateKey, Obfuscation: c.Obfuscation, MaxPeers: awg.DefaultMaxPeers,
	})
	if err != nil {
		return fail(err)
	}
	if err := g.transport.Bind().SetMark(socketpolicy.Underlay); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if generation.Lifecycle() != transportgen.LifecycleReady {
		return fail(ErrGenerationStale)
	}
	return g, nil
}

func (g *windowsExitGeneration) authorize(ctx context.Context, v tunnellease.Lease, binding ResolvedBinding, endpoint awg.SelectedEndpoint, upstream bool) error {
	if ctx == nil || g.closing || g.transport == nil || g.generation.Lifecycle() != transportgen.LifecycleReady {
		return ErrGenerationStale
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tunnellease.Validate(v, time.Now().UTC()); err != nil {
		return err
	}
	remote, local := v.ClientPeer, v.ExitPeer
	if upstream {
		remote, local = v.ExitPeer, v.ClientPeer
	}
	if local != g.config.LocalPeer || remote == local || v.Session.IsZero() || !v.IPv4.IsValid() || v.IPv6.IsValid() ||
		binding.Binding.PeerID != remote || binding.Key != binding.Binding.PublicKey ||
		endpoint.RemotePeer != remote || endpoint.NetworkGeneration != g.generation.ID() ||
		upstream == g.config.IPv4Pool.Contains(v.IPv4.Addr()) {
		return ErrInvalidConfig
	}
	return g.config.Bindings.AuthorizePromotion(binding)
}

func (g *windowsExitGeneration) AddDownstream(ctx context.Context, v tunnellease.Lease, binding ResolvedBinding, endpoint awg.SelectedEndpoint) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.authorize(ctx, v, binding, endpoint, false); err != nil {
		return err
	}
	if previous := g.peers[v.ID]; previous != nil {
		if previous.ready && previous.lease == v && previous.binding.Key == binding.Key && previous.endpoint == endpoint {
			return nil
		}
		return tunnellease.ErrStaleGeneration
	}
	g.peers[v.ID] = &windowsExitPeer{lease: v, binding: binding, endpoint: endpoint}
	err := g.resources.ActivateLease(ctx, v)
	if err == nil && g.generation.Lifecycle() != transportgen.LifecycleReady {
		err = ErrGenerationStale
	}
	if err != nil {
		cleanup := g.resources.RevokeLease(ctx, v, tunnellease.RevokeAdministrative)
		if cleanup == nil {
			delete(g.peers, v.ID)
		}
		return errors.Join(err, cleanup)
	}
	g.peers[v.ID].ready = true
	return nil
}

func (g *windowsExitGeneration) install(_ context.Context, v tunnellease.Lease) error {
	p := g.peers[v.ID]
	if p == nil || p.lease != v || g.transport == nil {
		return ErrGenerationStale
	}
	if err := g.config.Bindings.AuthorizePromotion(p.binding); err != nil {
		return err
	}
	if err := g.transport.AddPeer(awg.PeerConfig{RemotePeer: v.ClientPeer, PublicKey: p.binding.Key, Endpoint: p.endpoint, AllowedIPs: []netip.Prefix{v.IPv4}}); err != nil {
		return err
	}
	p.installed = true
	return nil
}

func (g *windowsExitGeneration) remove(_ context.Context, v tunnellease.Lease) error {
	p := g.peers[v.ID]
	if p == nil || p.lease != v || g.transport == nil {
		return ErrGenerationStale
	}
	if !p.installed {
		return nil
	}
	err := g.transport.RemovePeer(p.binding.Key)
	if errors.Is(err, awg.ErrPeerNotFound) {
		err = nil
	}
	if err == nil {
		p.installed = false
	}
	return err
}

func (g *windowsExitGeneration) RenewLease(ctx context.Context, previous, renewed tunnellease.Lease) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.peers[previous.ID]
	if g.closing || p == nil || !p.ready || p.lease != previous || g.generation.Lifecycle() != transportgen.LifecycleReady {
		return ErrGenerationStale
	}
	if err := g.resources.RenewLease(ctx, previous, renewed); err != nil {
		return err
	}
	p.lease = renewed
	return nil
}

func (g *windowsExitGeneration) RevokeLease(ctx context.Context, v tunnellease.Lease, reason tunnellease.RevocationReason) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.peers[v.ID]
	if p == nil {
		return nil
	}
	if p.lease != v {
		return tunnellease.ErrStaleGeneration
	}
	p.ready = false
	if err := g.resources.RevokeLease(ctx, v, reason); err != nil {
		return err
	}
	delete(g.peers, v.ID)
	return nil
}

func (g *windowsExitGeneration) AddUpstream(ctx context.Context, v tunnellease.Lease, binding ResolvedBinding, endpoint awg.SelectedEndpoint, control ...netip.AddrPort) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.authorize(ctx, v, binding, endpoint, true); err != nil {
		return err
	}
	if g.upstream != nil {
		return tunnellease.ErrRevocationPending
	}
	p, err := g.resources.Bridge().Attach(v.IPv4.Addr(), true, control...)
	if err != nil {
		return err
	}
	g.upstream = &windowsExitUpstream{windowsExitPeer: windowsExitPeer{lease: v, binding: binding, endpoint: endpoint}, attachment: p}
	err = g.transport.AddPeer(awg.PeerConfig{RemotePeer: v.ExitPeer, PublicKey: binding.Key, Endpoint: endpoint, AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}})
	if err == nil {
		g.upstream.installed = true
		err = ctx.Err()
	}
	if err == nil && g.generation.Lifecycle() != transportgen.LifecycleReady {
		err = ErrGenerationStale
	}
	if err != nil {
		return errors.Join(err, g.removeUpstream(ctx))
	}
	g.upstream.ready = true
	return nil
}

func (g *windowsExitGeneration) RemoveUpstream(ctx context.Context, id tunnellease.ID, session tunnellease.SessionToken) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.upstream == nil {
		return nil
	}
	if g.upstream.lease.ID != id || g.upstream.lease.Session != session {
		return tunnellease.ErrStaleGeneration
	}
	return g.removeUpstream(ctx)
}

func (g *windowsExitGeneration) removeUpstream(ctx context.Context) error {
	p := g.upstream
	if p == nil {
		return nil
	}
	p.ready = false
	if err := g.resources.Bridge().Retire(ctx, p.attachment); err != nil {
		return err
	}
	if p.installed {
		if err := g.transport.RemovePeer(p.binding.Key); err != nil && !errors.Is(err, awg.ErrPeerNotFound) {
			return err
		}
		p.installed = false
	}
	if err := g.resources.Bridge().Forget(p.attachment); err != nil {
		return err
	}
	g.upstream = nil
	return nil
}

func (g *windowsExitGeneration) Close(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalidConfig
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closing = true
	if err := g.removeUpstream(ctx); err != nil {
		return err
	}
	if g.resources != nil {
		if err := g.resources.Close(ctx); err != nil {
			return err
		}
		g.resources = nil
		g.borrowed = nil
		clear(g.peers)
	}
	if g.transport != nil {
		if err := g.transport.Close(); err != nil {
			return err
		}
		g.transport = nil
	}
	if g.borrowed != nil {
		if err := g.borrowed.Close(); err != nil {
			return err
		}
		g.borrowed = nil
	}
	return nil
}
