// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnelsession

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

type fakeTUN struct{ closed atomic.Int32 }

func (*fakeTUN) Name() string                   { return "hydra0" }
func (*fakeTUN) IfIndex() int                   { return 7 }
func (*fakeTUN) Addresses() []netip.Prefix      { return nil }
func (*fakeTUN) Borrow() (awgtun.Device, error) { return nil, errors.New("unused") }
func (f *fakeTUN) Close() error                 { f.closed.Add(1); return nil }

type fakeCloser struct{ closed atomic.Int32 }

func (f *fakeCloser) Close() error { f.closed.Add(1); return nil }

func sessionFixture(t *testing.T) (*Session, *socketmux.Mux, *fakeTUN) {
	t.Helper()
	local := peer.ID("local")
	remote := peer.ID("remote")
	owner := &fakeTUN{}
	lease := tunnellease.Lease{ExitPeer: remote, ClientPeer: local, Generation: 1, IPv4: netip.MustParsePrefix("10.77.0.2/32"), ValidUntil: time.Now().Add(time.Hour)}
	lease.ID[0] = 1
	s, err := New(context.Background(), Config{LocalPeer: local, RemoteExit: remote, Lease: lease, NewTUN: func(context.Context) (tunnel.DeviceOwner, error) { return owner, nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.markMux = func(*socketmux.Mux, uint32) error { return nil }
	c := socketmux.DefaultConfig()
	c.IPv4Policy = socketmux.FamilyRequired
	c.IPv6Policy = socketmux.FamilyDisabled
	c.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	mux, err := socketmux.New(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mux.Close(); _ = s.Close() })
	return s, mux, owner
}

func TestSessionKeepsTUNAcrossGenerationSwap(t *testing.T) {
	s, mux, owner := sessionFixture(t)
	routing1, data1 := &fakeCloser{}, &fakeCloser{}
	if err := s.ActivateGeneration(Generation{ID: 1, Mux: mux, Routing: routing1, DataPlane: data1}); err != nil {
		t.Fatal(err)
	}
	routing2, data2 := &fakeCloser{}, &fakeCloser{}
	if err := s.ActivateGeneration(Generation{ID: 2, Mux: mux, Routing: routing2, DataPlane: data2}); err != nil {
		t.Fatal(err)
	}
	if routing1.closed.Load() != 1 || data1.closed.Load() != 1 || owner.closed.Load() != 0 {
		t.Fatalf("old=%d/%d tun=%d", routing1.closed.Load(), data1.closed.Load(), owner.closed.Load())
	}
	if snapshot := s.Snapshot(); snapshot.TUNIfIndex != 7 || snapshot.NetworkGeneration != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if owner.closed.Load() != 1 || routing2.closed.Load() != 1 || data2.closed.Load() != 1 {
		t.Fatal("final resources not closed")
	}
}

func TestSessionRejectsAndClosesStaleGeneration(t *testing.T) {
	s, mux, _ := sessionFixture(t)
	if err := s.ActivateGeneration(Generation{ID: 2, Mux: mux, Routing: &fakeCloser{}, DataPlane: &fakeCloser{}}); err != nil {
		t.Fatal(err)
	}
	routing, data := &fakeCloser{}, &fakeCloser{}
	err := s.ActivateGeneration(Generation{ID: 1, Mux: mux, Routing: routing, DataPlane: data})
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("error=%v", err)
	}
	if routing.closed.Load() != 1 || data.closed.Load() != 1 {
		t.Fatal("stale resources retained")
	}
}

func TestSessionReleaseIsBounded(t *testing.T) {
	local, remote := peer.ID("local"), peer.ID("remote")
	owner := &fakeTUN{}
	lease := tunnellease.Lease{ExitPeer: remote, ClientPeer: local, Generation: 1, IPv4: netip.MustParsePrefix("10.77.0.2/32"), ValidUntil: time.Now().Add(time.Hour)}
	lease.ID[0] = 2
	released := make(chan struct{})
	s, err := New(context.Background(), Config{
		LocalPeer: local, RemoteExit: remote, Lease: lease,
		NewTUN:       func(context.Context) (tunnel.DeviceOwner, error) { return owner, nil },
		CloseTimeout: 10 * time.Millisecond,
		Release: func(ctx context.Context, _ tunnellease.ID) error {
			<-ctx.Done()
			close(released)
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error=%v", err)
	}
	select {
	case <-released:
	default:
		t.Fatal("release callback did not observe bounded context")
	}
}

func TestExitServiceRollsBackPartialSetup(t *testing.T) {
	exitPeer := peer.ID("exit")
	allocatorConfig := tunnellease.DefaultConfig(exitPeer)
	allocatorConfig.IPv4Pool = netip.MustParsePrefix("10.90.0.0/29")
	allocator, err := tunnellease.New(allocatorConfig)
	if err != nil {
		t.Fatal(err)
	}
	owner := &fakeTUN{}
	forwarding := &fakeCloser{}
	service, err := NewExit(context.Background(), ExitConfig{
		LocalPeer: exitPeer, Allocator: allocator,
		NewTUN:           func(context.Context) (tunnel.DeviceOwner, error) { return owner, nil },
		EnableForwarding: func(context.Context) (io.Closer, error) { return forwarding, nil },
		InstallFirewall: func(context.Context, tunnel.DeviceOwner) (io.Closer, error) {
			return nil, errors.New("injected firewall failure")
		},
	})
	if err == nil || service != nil {
		t.Fatalf("partial Exit setup result=%v error=%v", service, err)
	}
	if owner.closed.Load() != 1 || forwarding.closed.Load() != 1 {
		t.Fatalf("partial Exit resources retained tun=%d forwarding=%d", owner.closed.Load(), forwarding.closed.Load())
	}
	if _, err := allocator.Allocate(peer.ID("client")); !errors.Is(err, tunnellease.ErrClosed) {
		t.Fatalf("partial Exit retained allocator: %v", err)
	}
}

func TestExitServiceKeepsOtherClientLeasesAfterOneRelease(t *testing.T) {
	exitPeer := peer.ID("exit")
	allocatorConfig := tunnellease.DefaultConfig(exitPeer)
	allocatorConfig.IPv4Pool = netip.MustParsePrefix("10.91.0.0/29")
	allocator, err := tunnellease.New(allocatorConfig)
	if err != nil {
		t.Fatal(err)
	}
	owner, forwarding, firewall := &fakeTUN{}, &fakeCloser{}, &fakeCloser{}
	service, err := NewExit(context.Background(), ExitConfig{
		LocalPeer: exitPeer, Allocator: allocator,
		NewTUN:           func(context.Context) (tunnel.DeviceOwner, error) { return owner, nil },
		EnableForwarding: func(context.Context) (io.Closer, error) { return forwarding, nil },
		InstallFirewall:  func(context.Context, tunnel.DeviceOwner) (io.Closer, error) { return firewall, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	clients := []peer.ID{"a", "b", "c"}
	leases := make([]tunnellease.Lease, len(clients))
	for index, client := range clients {
		leases[index], err = service.Allocate(client)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := service.Release(clients[0], leases[0].ID); err != nil {
		t.Fatal(err)
	}
	if active := allocator.Active(); len(active) != 2 {
		t.Fatalf("active leases after one release = %d", len(active))
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if owner.closed.Load() != 1 || forwarding.closed.Load() != 1 || firewall.closed.Load() != 1 {
		t.Fatal("Exit close did not release all shared owners")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

var _ io.Closer = (*fakeCloser)(nil)
