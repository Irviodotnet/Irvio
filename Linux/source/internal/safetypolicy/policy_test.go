// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package safetypolicy

import (
	"slices"
	"testing"
)

func TestV0CanonicalPortsAreExactAndAsymmetric(t *testing.T) {
	wantTCP := []uint16{19, 25, 135, 137, 138, 139, 445}
	wantUDP := []uint16{19, 135, 137, 138, 139, 1900, 11211}
	if got := Ports(V0, TransportTCP); !slices.Equal(got, wantTCP) {
		t.Fatalf("TCP ports=%v, want %v", got, wantTCP)
	}
	if got := Ports(V0, TransportUDP); !slices.Equal(got, wantUDP) {
		t.Fatalf("UDP ports=%v, want %v", got, wantUDP)
	}

	for _, test := range []struct {
		transport Transport
		port      uint16
		blocked   bool
	}{
		{TransportTCP, 25, true},
		{TransportUDP, 25, false},
		{TransportTCP, 445, true},
		{TransportUDP, 445, false},
		{TransportUDP, 1900, true},
		{TransportTCP, 1900, false},
		{TransportUDP, 11211, true},
		{TransportTCP, 11211, false},
	} {
		if got := Blocks(V0, test.transport, test.port); got != test.blocked {
			t.Errorf("Blocks(%v, %d)=%v, want %v", test.transport, test.port, got, test.blocked)
		}
	}
}

func TestV0RangeBoundariesDoNotOverblock(t *testing.T) {
	for _, transport := range []Transport{TransportTCP, TransportUDP} {
		for _, port := range []uint16{136, 140} {
			if Blocks(V0, transport, port) {
				t.Errorf("transport=%v neighbor port %d blocked", transport, port)
			}
		}
		for _, port := range []uint16{137, 138, 139} {
			if !Blocks(V0, transport, port) {
				t.Errorf("transport=%v policy port %d allowed", transport, port)
			}
		}
	}
}

func TestV0ReturnsDefensiveCopies(t *testing.T) {
	ports := Ports(V0, TransportTCP)
	ports[0] = 65535
	if !Blocks(V0, TransportTCP, 19) || Blocks(V0, TransportTCP, 65535) {
		t.Fatal("caller mutation changed canonical policy")
	}
}
