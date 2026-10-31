// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestM3ASocketMuxDualStackOwnershipAndIsolation(t *testing.T) {
	probe, err := net.ListenUDP("udp6", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv6Loopback(), 0)))
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	_ = probe.Close()

	classifier := socketmux.ClassifierFunc(func(payload []byte, _ netip.AddrPort, _ socketmux.AddressFamily) socketmux.PacketClass {
		if bytes.HasPrefix(payload, []byte("control:")) {
			return socketmux.Control
		}
		if bytes.HasPrefix(payload, []byte("drop:")) {
			return socketmux.Drop
		}
		return socketmux.Data
	})
	mux, err := socketmux.New(context.Background(), socketmux.Config{
		IPv4Policy:           socketmux.FamilyRequired,
		IPv6Policy:           socketmux.FamilyRequired,
		IPv4Bind:             netip.MustParseAddr("127.0.0.1"),
		IPv6Bind:             netip.IPv6Loopback(),
		ControlQueueCapacity: 2,
		DataQueueCapacity:    2,
		MaxDatagramSize:      1200,
		Classifier:           classifier,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	ipv4, ipv4Active := mux.LocalAddr(socketmux.IPv4)
	ipv6, ipv6Active := mux.LocalAddr(socketmux.IPv6)
	if !ipv4Active || !ipv6Active || ipv4.Port() != ipv6.Port() || ipv4.Port() != mux.Port() {
		t.Fatalf("invalid dual-stack endpoints: IPv4=%s/%v IPv6=%s/%v port=%d", ipv4, ipv4Active, ipv6, ipv6Active, mux.Port())
	}

	ipv4Sender := integrationUDPConn(t, "udp4", netip.MustParseAddr("127.0.0.1"))
	ipv6Sender := integrationUDPConn(t, "udp6", netip.IPv6Loopback())
	integrationSend(t, ipv4Sender, ipv4, "control:in-v4")
	integrationSend(t, ipv4Sender, ipv4, "data:in-v4")
	integrationSend(t, ipv6Sender, ipv6, "control:in-v6")
	integrationSend(t, ipv6Sender, ipv6, "data:in-v6")

	controlInbound := map[string]socketmux.AddressFamily{}
	dataInbound := map[string]socketmux.AddressFamily{}
	for index := 0; index < 2; index++ {
		datagram := integrationReadLogical(t, mux.Control())
		controlInbound[string(datagram.Payload)] = datagram.Family
		datagram = integrationReadLogical(t, mux.Data())
		dataInbound[string(datagram.Payload)] = datagram.Family
	}
	if controlInbound["control:in-v4"] != socketmux.IPv4 || controlInbound["control:in-v6"] != socketmux.IPv6 {
		t.Fatalf("control isolation failed: %v", controlInbound)
	}
	if dataInbound["data:in-v4"] != socketmux.IPv4 || dataInbound["data:in-v6"] != socketmux.IPv6 {
		t.Fatalf("data isolation failed: %v", dataInbound)
	}

	ipv4Receiver := integrationUDPConn(t, "udp4", netip.MustParseAddr("127.0.0.1"))
	ipv6Receiver := integrationUDPConn(t, "udp6", netip.IPv6Loopback())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, outbound := range []struct {
		endpoint *socketmux.Endpoint
		receiver *net.UDPConn
		payload  string
	}{
		{mux.Control(), ipv4Receiver, "control:out-v4"},
		{mux.Data(), ipv4Receiver, "data:out-v4"},
		{mux.Control(), ipv6Receiver, "control:out-v6"},
		{mux.Data(), ipv6Receiver, "data:out-v6"},
	} {
		destination := integrationLocalAddr(t, outbound.receiver)
		if _, err := outbound.endpoint.WriteTo(ctx, []byte(outbound.payload), destination); err != nil {
			t.Fatal(err)
		}
		payload, source := integrationReadPhysical(t, outbound.receiver)
		if string(payload) != outbound.payload || source.Port() != mux.Port() {
			t.Fatalf("outbound payload=%q source=%s mux_port=%d", payload, source, mux.Port())
		}
	}

	for index := 0; index < 5; index++ {
		integrationSend(t, ipv4Sender, ipv4, "control:backpressure")
	}
	integrationSend(t, ipv4Sender, ipv4, "data:still-alive")
	if got := string(integrationReadLogical(t, mux.Data()).Payload); got != "data:still-alive" {
		t.Fatalf("data path stalled by control backpressure: %q", got)
	}
	integrationEventually(t, func() bool { return mux.Stats().ControlOverflowDrops >= 3 })
	_ = integrationReadLogical(t, mux.Control())
	_ = integrationReadLogical(t, mux.Control())

	results := make(chan error, 2)
	ready := make(chan struct{}, 2)
	for _, endpoint := range []*socketmux.Endpoint{mux.Control(), mux.Data()} {
		go func(endpoint *socketmux.Endpoint) {
			ready <- struct{}{}
			_, err := endpoint.Read(context.Background())
			results <- err
		}(endpoint)
	}
	<-ready
	<-ready
	if err := mux.Close(); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		if err := <-results; !errors.Is(err, socketmux.ErrClosed) {
			t.Fatalf("blocked logical read error = %v", err)
		}
	}
	if err := mux.Close(); err != nil {
		t.Fatal(err)
	}
}

func integrationUDPConn(t *testing.T, network string, address netip.Addr) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, 0)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func integrationLocalAddr(t *testing.T, conn *net.UDPConn) netip.AddrPort {
	t.Helper()
	address, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("local address type = %T", conn.LocalAddr())
	}
	return address.AddrPort()
}

func integrationSend(t *testing.T, conn *net.UDPConn, destination netip.AddrPort, payload string) {
	t.Helper()
	if _, err := conn.WriteToUDPAddrPort([]byte(payload), destination); err != nil {
		t.Fatal(err)
	}
}

func integrationReadLogical(t *testing.T, endpoint *socketmux.Endpoint) socketmux.Datagram {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	datagram, err := endpoint.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return datagram
}

func integrationReadPhysical(t *testing.T, conn *net.UDPConn) ([]byte, netip.AddrPort) {
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

func integrationEventually(t *testing.T, condition func() bool) {
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
