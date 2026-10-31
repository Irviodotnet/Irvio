// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

type testStack struct {
	registry *Registry
	mux      *socketmux.Mux
	adapter  *Adapter
}

func newTestStack(t *testing.T, dualStack bool, queueCapacity int) *testStack {
	t.Helper()
	registry, err := NewRegistry(DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	addresses := []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	if dualStack {
		muxConfig.IPv6Policy = socketmux.FamilyRequired
		muxConfig.IPv6Bind = netip.MustParseAddr("::1")
		addresses = append(addresses, netip.MustParseAddr("::1"))
	}
	muxConfig.Classifier = NewClassifier(registry)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		if dualStack {
			t.Skipf("IPv6 loopback is unavailable: %v", err)
		}
		t.Fatal(err)
	}
	config := DefaultConfig(addresses...)
	if queueCapacity > 0 {
		config.ConnectionQueueCapacity = queueCapacity
	}
	config.Logger = muxConfig.Logger
	adapter, err := New(mux.Control(), registry, config)
	if err != nil {
		_ = mux.Close()
		t.Fatal(err)
	}
	stack := &testStack{registry: registry, mux: mux, adapter: adapter}
	t.Cleanup(func() {
		_ = adapter.Close()
		_ = mux.Close()
	})
	return stack
}

func mustBindingRequest(t *testing.T, username string) *stun.Message {
	t.Helper()
	message, err := stun.Build(stun.TransactionID, stun.BindingRequest, stun.NewUsername(username))
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func mustBindingResponse(t *testing.T, request *stun.Message) *stun.Message {
	t.Helper()
	message := stun.MustBuild(stun.BindingSuccess)
	message.TransactionID = request.TransactionID
	message.Encode()
	return message
}

func listenUDP(t *testing.T, family socketmux.AddressFamily) *net.UDPConn {
	t.Helper()
	network := "udp4"
	address := netip.MustParseAddr("127.0.0.1")
	if family == socketmux.IPv6 {
		network = "udp6"
		address = netip.MustParseAddr("::1")
	}
	conn, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, 0)))
	if err != nil {
		if family == socketmux.IPv6 {
			t.Skipf("IPv6 loopback is unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readUDP(t *testing.T, conn *net.UDPConn) ([]byte, netip.AddrPort) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2048)
	n, source, err := conn.ReadFromUDPAddrPort(buffer)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), buffer[:n]...), source
}
