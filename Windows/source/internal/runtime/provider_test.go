// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"net/netip"
	"testing"
)

func TestCandidateNetworkEpochTracksSignedSessionEpoch(t *testing.T) {
	first := candidateNetworkEpoch([]byte("first authenticated process epoch"))
	if first == 0 || first != candidateNetworkEpoch([]byte("first authenticated process epoch")) {
		t.Fatalf("network epoch is not stable and nonzero: %d", first)
	}
	second := candidateNetworkEpoch([]byte("second authenticated process epoch"))
	if second == first {
		t.Fatalf("distinct signed session epochs collided: %d", first)
	}
}

func TestCandidateNetworkEpochDoesNotDependOnAnnouncementSequence(t *testing.T) {
	session := []byte("stable authenticated process epoch")
	before := candidateNetworkEpoch(session)
	after := candidateNetworkEpoch(session)
	if before != after {
		t.Fatalf("announcement refresh churned NetworkEpoch: %d != %d", before, after)
	}
}

func TestCandidateNetworkEpochCanonicalizesLocators(t *testing.T) {
	session := []byte("stable authenticated process epoch")
	first := candidateNetworkEpoch(session,
		"/ip4/198.51.100.10/tcp/4001",
		"/ip6/2001:db8::10/tcp/4001",
	)
	reordered := candidateNetworkEpoch(session,
		"/ip6/2001:db8::10/tcp/4001",
		"/ip4/198.51.100.10/tcp/4001",
		"/ip4/198.51.100.10/tcp/4001",
	)
	if first != reordered {
		t.Fatalf("locator order or duplication changed NetworkEpoch: %d != %d", first, reordered)
	}
	changed := candidateNetworkEpoch(session, "/ip4/198.51.100.11/tcp/4001")
	if changed == first {
		t.Fatalf("changed remote endpoint retained NetworkEpoch %d", first)
	}
}

func TestLiteralControlEndpointPrefersPublicAddressOverTunnelAddress(t *testing.T) {
	endpoint, ok := literalControlEndpoint([]string{
		"/ip4/10.77.0.1/tcp/4001",
		"/ip4/89.124.71.34/tcp/4001",
	})
	if !ok || endpoint != netip.MustParseAddrPort("89.124.71.34:4001") {
		t.Fatalf("control endpoint = %s, %v", endpoint, ok)
	}
}

func TestLiteralControlEndpointFallsBackToPrivateAddress(t *testing.T) {
	endpoint, ok := literalControlEndpoint([]string{
		"/ip4/127.0.0.1/tcp/4001",
		"/ip4/10.77.0.1/tcp/4001",
	})
	if !ok || endpoint != netip.MustParseAddrPort("10.77.0.1:4001") {
		t.Fatalf("control endpoint = %s, %v", endpoint, ok)
	}
}
