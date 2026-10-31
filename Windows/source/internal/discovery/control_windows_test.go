// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

type discoveryControlGrant struct {
	once   sync.Once
	active *atomic.Int32
}

func (g *discoveryControlGrant) Close() error {
	g.once.Do(func() { g.active.Add(-1) })
	return nil
}

func TestWindowsDiscoveryClosesOwnedControlConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server, err := Start(ctx, Config{DataDir: discoveryTestDir(t), DHTMode: "server"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bootstrap, err := server.IPv4LoopbackAddress()
	if err != nil {
		t.Fatal(err)
	}
	var active, calls atomic.Int32
	factory, err := controlsocket.DialerForOwnedBindings(func(_ context.Context, target netip.AddrPort) (winroute.Binding, io.Closer, error) {
		binding, err := winroute.ForSource(netip.MustParseAddr("127.0.0.1"))
		if err != nil {
			return binding, nil, err
		}
		calls.Add(1)
		active.Add(1)
		return binding, &discoveryControlGrant{active: &active}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := Start(ctx, Config{DataDir: discoveryTestDir(t), DHTMode: "server", Bootstrap: []string{bootstrap}, ControlDialer: factory})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.dht.Ping(ctx, server.PeerID()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() == 0 || active.Load() == 0 {
		t.Fatal("DHT did not retain owned TCP connection")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for active.Load() != 0 {
		select {
		case <-deadline.C:
			t.Fatal("libp2p closed with outstanding TCP grants", active.Load())
		case <-tick.C:
		}
	}
}

func TestWindowsDiscoveryUsesOwnedControlListener(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	listeners := &controlsocket.WindowsListeners{}
	defer listeners.Close()
	var active, calls atomic.Int32
	factory, err := listeners.Factory(ctx, func(_ context.Context, ipv4 bool) (winroute.Binding, error) {
		source := "::1"
		if ipv4 {
			source = "127.0.0.1"
		}
		return winroute.ForSource(netip.MustParseAddr(source))
	}, func(context.Context, winroute.Binding, netip.AddrPort) (io.Closer, error) {
		active.Add(1)
		calls.Add(1)
		return &discoveryControlGrant{active: &active}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := Start(ctx, Config{DataDir: discoveryTestDir(t), DHTMode: "server", ControlListener: factory})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bootstrap, err := server.IPv4LoopbackAddress()
	if err != nil {
		t.Fatal(err)
	}
	client, err := Start(ctx, Config{DataDir: discoveryTestDir(t), DHTMode: "server", Bootstrap: []string{bootstrap}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.dht.Ping(ctx, server.PeerID()); err != nil {
		t.Fatal(err)
	}
	if active.Load() == 0 || calls.Load() == 0 {
		t.Fatal("listener not owned")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := listeners.Close(); err != nil || active.Load() != 0 {
		t.Fatal("listener grant remained", err)
	}
}

func TestWindowsDiscoveryUsesBoundControlTCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server, err := Start(ctx, Config{DataDir: discoveryTestDir(t), DHTMode: "server"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bootstrap, err := server.IPv4LoopbackAddress()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	factory, err := controlsocket.DialerForBindings(func(_ context.Context, target netip.Addr) (winroute.Binding, error) {
		calls.Add(1)
		return winroute.ForSource(netip.MustParseAddr("127.0.0.1"))
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := Start(ctx, Config{DataDir: discoveryTestDir(t), DHTMode: "server", Bootstrap: []string{bootstrap}, ControlDialer: factory})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.dht.Ping(ctx, server.PeerID()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 1 || client.dht.RoutingTable().Size() == 0 {
		t.Fatal("bound control discovery path was not exercised")
	}
	if client.ControlMark() != 0 {
		t.Fatal("Windows dialer falsely reports a Linux mark")
	}
	var denied atomic.Int32
	blocked, err := controlsocket.DialerForBindings(func(context.Context, netip.Addr) (winroute.Binding, error) {
		denied.Add(1)
		return winroute.Binding{}, errors.New("underlay generation unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := Start(ctx, Config{DataDir: discoveryTestDir(t), Bootstrap: []string{bootstrap}, ControlDialer: blocked})
	if failed != nil {
		failed.Close()
	}
	if err == nil || denied.Load() == 0 {
		t.Fatalf("discovery bypassed failed binding: %v", err)
	}
}
