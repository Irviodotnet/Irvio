// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnelsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"time"

	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

// Session owns the long-lived TUN and lease above transport generations.
type Session struct {
	config Config
	tun    tunnel.DeviceOwner

	mu         sync.Mutex
	generation networkstate.Generation
	routing    io.Closer
	dataPlane  io.Closer
	closed     bool
	closeOnce  sync.Once
	closeErr   error
	markMux    func(*socketmux.Mux, uint32) error
}

// New creates the TUN without installing generation-local routes or AWG.
func New(ctx context.Context, config Config) (*Session, error) {
	if config.OuterMark == 0 {
		config.OuterMark = linuxroute.DefaultOuterMark
	}
	if config.CloseTimeout == 0 {
		config.CloseTimeout = 5 * time.Second
	}
	if err := validateConfig(ctx, config); err != nil {
		return nil, err
	}
	owner, err := config.NewTUN(ctx)
	if err != nil {
		return nil, fmt.Errorf("create logical tunnel TUN: %w", err)
	}
	if owner == nil || owner.IfIndex() <= 0 || owner.Name() == "" {
		if owner != nil {
			_ = owner.Close()
		}
		return nil, ErrInvalidConfig
	}
	config.DNS.Servers = append([]netip.AddrPort(nil), config.DNS.Servers...)
	return &Session{config: config, tun: owner, markMux: func(mux *socketmux.Mux, mark uint32) error { return mux.SetMark(mark) }}, nil
}

// TUN lends the stable owner; AWG must Borrow and never close it directly.
func (s *Session) TUN() tunnel.DeviceOwner {
	if s == nil {
		return nil
	}
	return s.tun
}

// ActivateGeneration rejects and closes stale resources, publishing replacements before closing predecessors.
func (s *Session) ActivateGeneration(generation Generation) error {
	if s == nil || generation.ID == 0 || generation.Mux == nil || generation.Routing == nil || generation.DataPlane == nil {
		closeGeneration(generation)
		return ErrInvalidConfig
	}
	if err := s.markMux(generation.Mux, s.config.OuterMark); err != nil {
		closeGeneration(generation)
		return fmt.Errorf("mark Irvio outer transport: %w", err)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		closeGeneration(generation)
		return ErrClosed
	}
	if generation.ID <= s.generation {
		s.mu.Unlock()
		closeGeneration(generation)
		return ErrStaleGeneration
	}
	oldRouting, oldDataPlane := s.routing, s.dataPlane
	s.generation, s.routing, s.dataPlane = generation.ID, generation.Routing, generation.DataPlane
	s.mu.Unlock()
	return errors.Join(closeResource(oldRouting), closeResource(oldDataPlane))
}

// Snapshot returns redacted state with the same lease across generations.
func (s *Session) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{Closed: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{LocalPeer: s.config.LocalPeer, RemoteExit: s.config.RemoteExit, Lease: s.config.Lease, TUNName: s.tun.Name(), TUNIfIndex: s.tun.IfIndex(), NetworkGeneration: s.generation, DNS: DNSPolicy{Servers: append([]netip.AddrPort(nil), s.config.DNS.Servers...)}, Closed: s.closed}
}

// Close fences late installs, tears down routing/AWG, then destroys the TUN and releases the lease.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		routing, dataPlane := s.routing, s.dataPlane
		s.routing = nil
		s.dataPlane = nil
		s.mu.Unlock()
		result := errors.Join(closeResource(routing), closeResource(dataPlane), s.tun.Close())
		if s.config.Release != nil {
			ctx, cancel := context.WithTimeout(context.Background(), s.config.CloseTimeout)
			result = errors.Join(result, s.config.Release(ctx, s.config.Lease.ID))
			cancel()
		}
		s.closeErr = result
	})
	return s.closeErr
}

func validateConfig(ctx context.Context, config Config) error {
	if ctx == nil || config.LocalPeer == "" || config.RemoteExit == "" || config.LocalPeer == config.RemoteExit || config.NewTUN == nil || config.Lease.ExitPeer != config.RemoteExit || config.Lease.ClientPeer != config.LocalPeer || config.OuterMark == 0 || config.CloseTimeout <= 0 || config.CloseTimeout > time.Minute || tunnellease.Validate(config.Lease, time.Now()) != nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	seen := make(map[netip.AddrPort]struct{}, len(config.DNS.Servers))
	for _, server := range config.DNS.Servers {
		if !server.IsValid() || server.Port() == 0 || server.Addr().IsUnspecified() || server.Addr().IsMulticast() || server.Addr().Is4In6() {
			return ErrInvalidConfig
		}
		if _, ok := seen[server]; ok {
			return ErrInvalidConfig
		}
		seen[server] = struct{}{}
	}
	return nil
}

func closeGeneration(g Generation) { _ = closeResource(g.Routing); _ = closeResource(g.DataPlane) }
func closeResource(resource io.Closer) error {
	if resource == nil {
		return nil
	}
	return resource.Close()
}
