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
	"sync"
	"time"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/exitbridge"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/windivert"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/libp2p/go-libp2p/core/peer"
)

func NewExitServer(ctx context.Context, config ExitServerConfig) (*ExitServer, error) {
	return newExitServer(ctx, config, func(ctx context.Context, c ExitServerConfig) (exitServerPlane, error) {
		p, err := newWindowsExitDataPlane(ctx, c)
		if p == nil {
			return nil, err
		}
		return p, err
	})
}

type windowsExitLease struct {
	lease   tunnellease.Lease
	session *pendingExitSession
	ready   bool
}

type windowsExitDataPlane struct {
	traffic        exitbridge.Traffic
	config         ExitServerConfig
	device         *windowsExitDevice
	directory      io.Closer
	openGeneration func(context.Context, *transportgen.Generation) (*windowsExitGeneration, error)
	configureOwn   func(context.Context, *windowsExitOwnSession) error
	ownProtection  windowsTunnelPolicy
	mu             sync.Mutex
	active         *windowsExitGeneration
	ready          bool
	pending        map[tunnellease.SessionToken]*pendingExitSession
	retired        map[tunnellease.SessionToken]*pendingExitSession
	leases         map[tunnellease.ID]*windowsExitLease
	upstream       *windowsExitOwnSession
	next           uint64
	networkFloor   networkstate.Generation
	changed        chan struct{}
	closing        bool
}

var _ exitServerPlane = (*windowsExitDataPlane)(nil)

func newWindowsExitDataPlane(ctx context.Context, c ExitServerConfig) (*windowsExitDataPlane, error) {
	if ctx == nil || !filepath.IsAbs(c.Platform.JournalDirectory) || !c.M12Enabled || len(c.Bypass) != 0 {
		return nil, ErrInvalidConfig
	}
	if c.Platform.Protection != nil {
		cc := ClientDataPlaneConfig{Platform: c.Platform}
		if err := normalizeClientPlatform(&cc); err != nil {
			return nil, err
		}
		c.Platform = cc.Platform
	}
	module, err := windivert.Load()
	if err != nil {
		return nil, err
	}
	if err := module.Probe(); err != nil {
		return nil, err
	}
	p := &windowsExitDataPlane{config: c, pending: make(map[tunnellease.SessionToken]*pendingExitSession), retired: make(map[tunnellease.SessionToken]*pendingExitSession), leases: make(map[tunnellease.ID]*windowsExitLease), changed: make(chan struct{})}
	p.configureOwn = p.openOwnRoutes
	if c.Platform.Protection != nil {
		p.ownProtection = c.Platform.Protection
	}
	fail := func(cause error) (*windowsExitDataPlane, error) {
		if err := p.Close(); err != nil {
			return p, errors.Join(cause, err)
		}
		return nil, cause
	}
	p.directory, err = winfile.OpenDirectory(c.Platform.JournalDirectory, true)
	if err != nil {
		return fail(err)
	}
	p.device, err = newWindowsExitDevice(ctx, c, filepath.Join(c.Platform.JournalDirectory, "control"))
	if err != nil {
		return fail(err)
	}
	if c.Platform.Protection != nil {
		if err := p.device.protectControl(ctx, c.Platform.Protection); err != nil {
			return fail(err)
		}
	}
	p.openGeneration = func(ctx context.Context, g *transportgen.Generation) (*windowsExitGeneration, error) {
		var local []netip.Addr
		for _, link := range g.Snapshot().Links {
			for _, prefix := range link.Addresses {
				local = append(local, prefix.Addr())
			}
		}
		return newWindowsExitGeneration(ctx, p.config, p.device.tun, g, module, local, &p.traffic)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return p, nil
}

func (p *windowsExitDataPlane) wake() { close(p.changed); p.changed = make(chan struct{}) }

func (p *windowsExitDataPlane) registerPending(s *pendingExitSession) error {
	if s == nil || s.ice == nil || s.token.IsZero() || s.remote == "" || s.key == (awg.Key{}) || s.keyEpoch == 0 || s.endpoint.RemotePeer != s.remote {
		return ErrInvalidConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing {
		return ErrClosed
	}
	if s.endpoint.NetworkGeneration < p.networkFloor {
		return ErrGenerationStale
	}
	if p.pending[s.token] != nil || p.retired[s.token] != nil {
		return ErrGenerationStale
	}
	for _, r := range p.leases {
		if r.lease.Session == s.token {
			return ErrGenerationStale
		}
	}
	if len(p.pending)+len(p.retired) >= p.config.MaxPending {
		return tunnellease.ErrExhausted
	}
	p.pending[s.token] = s
	p.wake()
	return nil
}

func (p *windowsExitDataPlane) closePending(s *pendingExitSession) error {
	if err := s.ice.Close(); err != nil {
		p.retired[s.token] = s
		return err
	}
	delete(p.retired, s.token)
	return nil
}

func (p *windowsExitDataPlane) expirePending(cutoff time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for token, s := range p.pending {
		if s.createdAt.Before(cutoff) {
			delete(p.pending, token)
			_ = p.closePending(s)
		}
	}
	for _, s := range p.retired {
		_ = p.closePending(s)
	}
}

func (p *windowsExitDataPlane) discardPending(token tunnellease.SessionToken, remote peer.ID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.pending[token]
	if s == nil {
		s = p.retired[token]
	}
	if s == nil {
		return nil
	}
	if s.remote != remote {
		return tunnellease.ErrWrongPeer
	}
	delete(p.pending, token)
	return p.closePending(s)
}

func (p *windowsExitDataPlane) takePending(ctx context.Context, v tunnellease.Lease) (*pendingExitSession, error) {
	for {
		p.mu.Lock()
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		if p.closing {
			p.mu.Unlock()
			return nil, ErrClosed
		}
		if s := p.pending[v.Session]; s != nil {
			if s.remote != v.ClientPeer {
				p.mu.Unlock()
				return nil, tunnellease.ErrWrongPeer
			}
			delete(p.pending, v.Session)
			p.mu.Unlock()
			return s, nil
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

func (p *windowsExitDataPlane) ensureGeneration(ctx context.Context, g *transportgen.Generation) error {
	if g == nil || g != p.config.Transports.Current() || g.Lifecycle() != transportgen.LifecycleReady || g.ID() < p.networkFloor {
		return ErrGenerationStale
	}
	p.networkFloor = g.ID()
	if p.active != nil && p.active.generation == g && p.ready {
		return nil
	}
	if err := p.retireGeneration(ctx, g.ID()); err != nil {
		return err
	}
	var err error
	p.active, err = p.openGeneration(ctx, g)
	if err != nil {
		return err
	}
	if p.active == nil {
		return ErrInvalidConfig
	}
	if err := p.config.Transports.Commit(g.ID()); err != nil {
		return err
	}
	p.ready = true
	return nil
}

func (p *windowsExitDataPlane) ActivateLease(ctx context.Context, v tunnellease.Lease) error {
	if ctx == nil || v.Session.IsZero() || v.ExitPeer != p.config.LocalPeer {
		return ErrInvalidConfig
	}
	if err := tunnellease.Validate(v, time.Now().UTC()); err != nil {
		return err
	}
	s, err := p.takePending(ctx, v)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fail := func(err error) error { return errors.Join(err, p.closePending(s)) }
	if p.closing {
		return fail(ErrClosed)
	}
	if p.leases[v.ID] != nil {
		return fail(tunnellease.ErrStaleGeneration)
	}
	g := p.config.Transports.Current()
	if g == nil || g.ID() != s.endpoint.NetworkGeneration {
		return fail(ErrGenerationStale)
	}
	if err := p.ensureGeneration(ctx, g); err != nil {
		return fail(err)
	}
	r := &windowsExitLease{lease: v, session: s}
	p.leases[v.ID] = r
	binding := ResolvedBinding{Binding: awgbinding.Binding{PeerID: s.remote, PublicKey: s.key, KeyEpoch: s.keyEpoch}, Key: s.key}
	if err := p.active.AddDownstream(ctx, v, binding, s.endpoint); err != nil {
		return err
	}
	r.ready = true
	return nil
}

func (p *windowsExitDataPlane) RenewLease(ctx context.Context, previous, renewed tunnellease.Lease) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.leases[previous.ID]
	if p.closing || p.active == nil || !p.ready || r == nil || !r.ready || r.lease != previous {
		return ErrGenerationStale
	}
	if err := p.active.RenewLease(ctx, previous, renewed); err != nil {
		return err
	}
	r.lease = renewed
	return nil
}

func (p *windowsExitDataPlane) RevokeLease(ctx context.Context, v tunnellease.Lease, reason tunnellease.RevocationReason) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	p.mu.Lock()
	r := p.leases[v.ID]
	if r == nil {
		p.mu.Unlock()
		return p.discardPending(v.Session, v.ClientPeer)
	}
	defer p.mu.Unlock()
	if r.lease != v {
		return tunnellease.ErrStaleGeneration
	}
	r.ready = false
	if p.active != nil {
		if err := p.active.RevokeLease(ctx, v, reason); err != nil {
			return err
		}
	}
	if err := p.closePending(r.session); err != nil {
		return err
	}
	delete(p.leases, v.ID)
	return nil
}

// retireGeneration stops physical forwarding before releasing ICE and leaves the long-lived adapter intact.
func (p *windowsExitDataPlane) retireGeneration(ctx context.Context, keep networkstate.Generation) error {
	p.ready = false
	if err := p.closeUpstream(ctx); err != nil {
		return err
	}
	if p.active != nil {
		if err := p.active.Close(ctx); err != nil {
			return err
		}
		p.active = nil
	}
	var result error
	for id, r := range p.leases {
		if err := p.closePending(r.session); err != nil {
			result = errors.Join(result, err)
		} else {
			delete(p.leases, id)
		}
	}
	for token, s := range p.pending {
		if keep == 0 || s.endpoint.NetworkGeneration != keep {
			delete(p.pending, token)
			result = errors.Join(result, p.closePending(s))
		}
	}
	for _, s := range p.retired {
		result = errors.Join(result, p.closePending(s))
	}
	return result
}

func (p *windowsExitDataPlane) ReconcileNetwork(ctx context.Context, state networkstate.State) error {
	if ctx == nil || state.Generation == 0 {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing {
		return ErrClosed
	}
	if state.Generation < p.networkFloor {
		return ErrGenerationStale
	}
	p.networkFloor = state.Generation
	if p.active != nil && p.active.generation.ID() == state.Generation && p.ready {
		return nil
	}
	return p.retireGeneration(ctx, state.Generation)
}

func (p *windowsExitDataPlane) rotationTarget() (tunnellease.Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing || !p.ready || p.active == nil {
		return tunnellease.Lease{}, ErrClosed
	}
	if p.upstream != nil || len(p.leases) != 1 || len(p.pending) != 0 || len(p.retired) != 0 {
		return tunnellease.Lease{}, tunnellease.ErrExhausted
	}
	for _, r := range p.leases {
		if r.ready {
			return r.lease, nil
		}
	}
	return tunnellease.Lease{}, tunnellease.ErrUnknownLease
}

func (p *windowsExitDataPlane) applyLocalAWGRotation(ctx context.Context, v tunnellease.Lease, key awg.Key) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.leases[v.ID]
	if p.closing || !p.ready || p.active == nil || p.upstream != nil || len(p.leases) != 1 || len(p.pending) != 0 || r == nil || !r.ready || r.lease.ClientPeer != v.ClientPeer || r.lease.Session != v.Session {
		return ErrGenerationStale
	}
	t := p.active.transport
	if err := t.PrepareHandshake(r.session.key); err != nil {
		return err
	}
	if err := t.UpdatePrivateKey(key); err != nil {
		return err
	}
	p.config.PrivateKey, p.active.config.PrivateKey = key, key
	_, err := t.WaitHandshake(ctx, r.session.key)
	return err
}

func (p *windowsExitDataPlane) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closing {
		p.closing = true
		p.wake()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.retireGeneration(ctx, 0); err != nil {
		return err
	}
	if p.device != nil {
		if err := p.device.Close(); err != nil {
			return err
		}
		p.device = nil
	}
	if p.directory != nil {
		if err := p.directory.Close(); err != nil {
			return err
		}
		p.directory = nil
	}
	return nil
}
