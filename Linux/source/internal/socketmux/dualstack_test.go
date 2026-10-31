// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"context"
	"net"
	"net/netip"
	"testing"
)

func TestSocketMuxDualStackPortZeroUsesCommonPort(t *testing.T) {
	requireIPv6(t)
	mux := newTestMux(t, dualStackConfig())
	ipv4, ipv4Active := mux.LocalAddr(IPv4)
	ipv6, ipv6Active := mux.LocalAddr(IPv6)
	if !ipv4Active || !ipv6Active {
		t.Fatalf("active families: IPv4=%v IPv6=%v", ipv4Active, ipv6Active)
	}
	if ipv4.Port() != ipv6.Port() || ipv4.Port() != mux.Port() {
		t.Fatalf("port mismatch: IPv4=%d IPv6=%d mux=%d", ipv4.Port(), ipv6.Port(), mux.Port())
	}
	if mux.ipv4 == nil || mux.ipv6 == nil || mux.ipv4.conn == mux.ipv6.conn {
		t.Fatal("mux does not own exactly one distinct socket per active family")
	}

	ipv4Sender := listenTestUDP(t, IPv4)
	ipv6Sender := listenTestUDP(t, IPv6)
	sendInbound(t, ipv4Sender, ipv4, []byte("control:v4"))
	sendInbound(t, ipv4Sender, ipv4, []byte("data:v4"))
	sendInbound(t, ipv6Sender, ipv6, []byte("control:v6"))
	sendInbound(t, ipv6Sender, ipv6, []byte("data:v6"))
	controlSeen := make(map[string]AddressFamily)
	dataSeen := make(map[string]AddressFamily)
	for index := 0; index < 2; index++ {
		datagram := readLogical(t, mux.Control())
		controlSeen[string(datagram.Payload)] = datagram.Family
		datagram = readLogical(t, mux.Data())
		dataSeen[string(datagram.Payload)] = datagram.Family
	}
	if controlSeen["control:v4"] != IPv4 || controlSeen["control:v6"] != IPv6 {
		t.Fatalf("control deliveries = %v", controlSeen)
	}
	if dataSeen["data:v4"] != IPv4 || dataSeen["data:v6"] != IPv6 {
		t.Fatalf("data deliveries = %v", dataSeen)
	}
}

func TestSocketMuxDualStackFixedPort(t *testing.T) {
	requireIPv6(t)
	first, err := New(context.Background(), dualStackConfig())
	if err != nil {
		t.Fatal(err)
	}
	port := first.Port()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	config := dualStackConfig()
	config.Port = int(port)
	mux := newTestMux(t, config)
	ipv4, _ := mux.LocalAddr(IPv4)
	ipv6, _ := mux.LocalAddr(IPv6)
	if ipv4.Port() != port || ipv6.Port() != port {
		t.Fatalf("fixed dual-stack ports: IPv4=%d IPv6=%d want=%d", ipv4.Port(), ipv6.Port(), port)
	}
}

func TestSocketMuxOptionalIPv6CanDegrade(t *testing.T) {
	requireIPv6(t)
	occupied, err := net.ListenUDP("udp6", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv6Loopback(), 0)))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	config := dualStackConfig()
	config.IPv6Policy = FamilyOptional
	config.Port = int(localAddrPort(t, occupied).Port())
	mux := newTestMux(t, config)
	if _, ok := mux.LocalAddr(IPv4); !ok {
		t.Fatal("required IPv4 family is inactive")
	}
	if _, ok := mux.LocalAddr(IPv6); ok {
		t.Fatal("occupied optional IPv6 family did not degrade")
	}
}
