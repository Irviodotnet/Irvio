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
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/libp2p/go-libp2p/core/peer"
)

type exitConstructionSpy struct {
	exitServerPlane
	closed     atomic.Int32
	failClose  atomic.Bool
	failRevoke atomic.Bool
}

func (p *exitConstructionSpy) Close() error {
	p.closed.Add(1)
	if p.failClose.Load() {
		return errors.New("injected plane close failure")
	}
	return nil
}
func (p *exitConstructionSpy) RevokeLease(context.Context, tunnellease.Lease, tunnellease.RevocationReason) error {
	if p.failRevoke.Load() {
		return errors.New("injected lease revoke failure")
	}
	return nil
}
func (*exitConstructionSpy) expirePending(time.Time) {}

func TestExitShutdownRetainsResourcesForRetry(t *testing.T) {
	t.Run("plane", func(t *testing.T) {
		p := &exitConstructionSpy{}
		p.failClose.Store(true)
		s := &ExitServer{plane: p, cancel: func() {}}
		if err := s.Close(context.Background()); err == nil || s.plane != p {
			t.Fatal("failed plane cleanup lost owner", err)
		}
		p.failClose.Store(false)
		if err := s.Close(context.Background()); err != nil || p.closed.Load() != 2 || s.plane != nil {
			t.Fatal("plane retry failed", err, p.closed.Load())
		}
		if err := s.Close(context.Background()); err != nil || p.closed.Load() != 2 {
			t.Fatal("close not idempotent", err)
		}
	})
	t.Run("workers", func(t *testing.T) {
		p := &exitConstructionSpy{}
		done := make(chan struct{})
		s := &ExitServer{plane: p, cancel: func() {}, done: done}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.Close(ctx); !errors.Is(err, context.Canceled) || s.plane != p || p.closed.Load() != 0 {
			t.Fatal("plane closed before workers stopped", err, p.closed.Load())
		}
		close(done)
		if err := s.Close(context.Background()); err != nil || p.closed.Load() != 1 {
			t.Fatal("cleanup did not resume after worker completion", err)
		}
	})
	t.Run("leases", func(t *testing.T) {
		p := &exitConstructionSpy{}
		c := tunnellease.DefaultConfig(peer.ID("exit"))
		c.IPv4Pool = netip.MustParsePrefix("10.17.0.0/24")
		a, err := tunnellease.New(c)
		if err != nil {
			t.Fatal(err)
		}
		l, err := tunnellease.NewLifecycle(tunnellease.DefaultLifecycleConfig(a, p))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { p.failRevoke.Store(false); _ = l.Close(context.Background()) })
		if _, err := l.Allocate(peer.ID("client")); err != nil {
			t.Fatal(err)
		}
		s := &ExitServer{plane: p, lifecycle: l, cancel: func() {}}
		p.failRevoke.Store(true)
		if err := s.Close(context.Background()); err == nil || s.lifecycle != l || s.plane != p || p.closed.Load() != 0 {
			t.Fatal("plane/lease owner lost after revoke failure", err)
		}
		p.failRevoke.Store(false)
		if err := s.Close(context.Background()); err != nil || p.closed.Load() != 1 || len(l.Status()) != 0 {
			t.Fatal("lease cleanup retry failed", err)
		}
	})
}

func TestCommonExitConstructionPreservesFactoryAndCleanup(t *testing.T) {
	h := newRuntimeHost(t)
	c := ExitServerConfig{Host: h, LocalPeer: h.ID(), PrivateKey: awg.Key{1}, Transports: &fakeTransportLifecycle{},
		SignalConfig: icesignal.DefaultConfig(),
		Bindings:     BindingResolver{Directory: &bindingDirectory{}, Registry: awgbinding.NewRegistry()},
		Authorizer:   icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		WANName:      "test-wan", TUNAddresses: []netip.Prefix{netip.MustParsePrefix("10.17.0.1/24")}, IPv4Pool: netip.MustParsePrefix("10.17.0.0/24")}
	if s, err := newExitServer(context.Background(), c, nil); err == nil || s != nil {
		t.Fatal("nil platform factory accepted")
	}
	boom := errors.New("platform factory failure")
	factory := func(_ context.Context, resolved ExitServerConfig) (exitServerPlane, error) {
		if resolved.TUNName != tunnel.DefaultName || resolved.LeaseTTL != tunnellease.DefaultTTL || resolved.MaxPending != tunnellease.DefaultMaxActive || resolved.Logger == nil {
			t.Fatal("legacy defaults changed")
		}
		return nil, boom
	}
	if s, err := newExitServer(context.Background(), c, factory); !errors.Is(err, boom) || s != nil {
		t.Fatal("platform failure was hidden", err)
	}
	if s, err := newExitServer(context.Background(), c, func(context.Context, ExitServerConfig) (exitServerPlane, error) { return nil, nil }); err == nil || s != nil {
		t.Fatal("nil data plane accepted")
	}
	p := &exitConstructionSpy{}
	c.M12Enabled = true
	if s, err := newExitServer(context.Background(), c, func(context.Context, ExitServerConfig) (exitServerPlane, error) { return p, nil }); err == nil || s != nil || p.closed.Load() != 1 {
		t.Fatal("M12 readiness failure did not close platform resources", err, p.closed.Load())
	}
	c.M12Enabled = false
	p = &exitConstructionSpy{}
	s, err := newExitServer(context.Background(), c, func(context.Context, ExitServerConfig) (exitServerPlane, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	if s.lifecycle == nil || s.iceService == nil || s.leaseService == nil {
		t.Fatal("common protocol services missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil || p.closed.Load() != 1 {
		t.Fatal("common shutdown not idempotent", err, p.closed.Load())
	}
}

func TestExitConstructionRetainsFailedRollback(t *testing.T) {
	for _, partialFactory := range []bool{true, false} {
		name := "protocol_setup"
		if partialFactory {
			name = "partial_factory"
		}
		for _, cleanupFails := range []bool{true, false} {
			cleanup := "clean_rollback"
			if cleanupFails {
				cleanup = "failed_rollback"
			}
			t.Run(name+"/"+cleanup, func(t *testing.T) {
				h := newRuntimeHost(t)
				c := ExitServerConfig{Host: h, LocalPeer: h.ID(), PrivateKey: awg.Key{1}, Transports: &fakeTransportLifecycle{},
					SignalConfig: icesignal.DefaultConfig(), M12Enabled: true,
					Bindings:   BindingResolver{Directory: &bindingDirectory{}, Registry: awgbinding.NewRegistry()},
					Authorizer: icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
					WANName:    "test-wan", TUNAddresses: []netip.Prefix{netip.MustParsePrefix("10.17.0.1/24")}, IPv4Pool: netip.MustParsePrefix("10.17.0.0/24")}
				p := &exitConstructionSpy{}
				p.failClose.Store(cleanupFails)
				boom := errors.New("partial platform construction failure")
				var returned *ExitServer
				t.Cleanup(func() {
					p.failClose.Store(false)
					if returned != nil {
						_ = returned.Close(context.Background())
					}
				})
				s, err := newExitServer(context.Background(), c, func(context.Context, ExitServerConfig) (exitServerPlane, error) {
					if partialFactory {
						return p, boom
					}
					return p, nil
				})
				returned = s
				if err == nil || partialFactory && !errors.Is(err, boom) {
					t.Fatal("construction error lost", err)
				}
				if p.closed.Load() != 1 {
					t.Fatal("rollback did not attempt plane cleanup", p.closed.Load())
				}
				if !cleanupFails {
					if s != nil {
						t.Fatal("clean rollback returned a live owner")
					}
					return
				}
				if s == nil || s.plane != p || s.ctx.Err() == nil {
					t.Fatal("failed rollback lost its fenced cleanup owner")
				}
				if err := s.Close(context.Background()); err == nil || p.closed.Load() != 2 || s.plane != p {
					t.Fatal("persistent cleanup failure lost ownership", err)
				}
				p.failClose.Store(false)
				if err := s.Close(context.Background()); err != nil || s.plane != nil || p.closed.Load() != 3 {
					t.Fatal("cleanup retry failed", err, p.closed.Load())
				}
				if err := s.Close(context.Background()); err != nil || p.closed.Load() != 3 {
					t.Fatal("successful cleanup not idempotent", err)
				}
			})
		}
	}
}
