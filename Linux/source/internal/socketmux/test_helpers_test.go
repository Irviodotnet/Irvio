// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testClassifier(payload []byte, _ netip.AddrPort, _ AddressFamily) PacketClass {
	switch {
	case bytes.HasPrefix(payload, []byte("control:")):
		return Control
	case bytes.HasPrefix(payload, []byte("drop:")):
		return Drop
	default:
		return Data
	}
}

func ipv4Config() Config {
	return Config{
		IPv4Policy:           FamilyRequired,
		IPv6Policy:           FamilyDisabled,
		IPv4Bind:             netip.MustParseAddr("127.0.0.1"),
		Port:                 0,
		ControlQueueCapacity: 8,
		DataQueueCapacity:    8,
		MaxDatagramSize:      1024,
		Classifier:           ClassifierFunc(testClassifier),
		Logger:               testLogger(),
	}
}

func ipv6Config() Config {
	return Config{
		IPv4Policy:           FamilyDisabled,
		IPv6Policy:           FamilyRequired,
		IPv6Bind:             netip.IPv6Loopback(),
		Port:                 0,
		ControlQueueCapacity: 8,
		DataQueueCapacity:    8,
		MaxDatagramSize:      1024,
		Classifier:           ClassifierFunc(testClassifier),
		Logger:               testLogger(),
	}
}

func dualStackConfig() Config {
	config := ipv4Config()
	config.IPv6Policy = FamilyRequired
	config.IPv6Bind = netip.IPv6Loopback()
	return config
}

func requireIPv6(t *testing.T) {
	t.Helper()
	probe, err := net.ListenUDP("udp6", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv6Loopback(), 0)))
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close IPv6 probe: %v", err)
	}
}

func newTestMux(t *testing.T, config Config) *Mux {
	t.Helper()
	mux, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mux.Close(); err != nil {
			t.Errorf("close mux: %v", err)
		}
	})
	return mux
}

func listenTestUDP(t *testing.T, family AddressFamily) *net.UDPConn {
	t.Helper()
	network := "udp4"
	address := netip.MustParseAddr("127.0.0.1")
	if family == IPv6 {
		network = "udp6"
		address = netip.IPv6Loopback()
	}
	conn, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, 0)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func sendInbound(t *testing.T, conn *net.UDPConn, destination netip.AddrPort, payload []byte) {
	t.Helper()
	if _, err := conn.WriteToUDPAddrPort(payload, destination); err != nil {
		t.Fatal(err)
	}
}

func readLogical(t *testing.T, endpoint *Endpoint) Datagram {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	datagram, err := endpoint.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return datagram
}

func readPhysical(t *testing.T, conn *net.UDPConn) ([]byte, netip.AddrPort) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2048)
	n, source, err := conn.ReadFromUDPAddrPort(buffer)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), buffer[:n]...), source
}

func localAddrPort(t *testing.T, conn *net.UDPConn) netip.AddrPort {
	t.Helper()
	address, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("local address type = %T", conn.LocalAddr())
	}
	return address.AddrPort()
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for !condition() {
		runtime.Gosched()
		select {
		case <-ctx.Done():
			t.Fatal("condition did not become true before deadline")
		default:
		}
	}
}
