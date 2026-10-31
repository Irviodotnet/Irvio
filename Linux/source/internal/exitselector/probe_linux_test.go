// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package exitselector

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/linuxroute"
)

func TestMarkedTCPProbeAcceptsOnlyIrvioMarkAndLiteralUsableEndpoint(t *testing.T) {
	probe, err := NewMarkedTCPProbe(0)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Mark != linuxroute.DefaultOuterMark {
		t.Fatalf("default probe mark = %#x", probe.Mark)
	}
	if _, err := NewMarkedTCPProbe(linuxroute.DefaultOuterMark + 1); err == nil {
		t.Fatal("CandidateProbe accepted a non-Irvio mark")
	}
	for _, endpoint := range []netip.AddrPort{
		netip.AddrPortFrom(netip.IPv4Unspecified(), 80),
		netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 80),
		netip.AddrPortFrom(netip.MustParseAddr("224.0.0.1"), 80),
		netip.AddrPortFrom(netip.MustParseAddr("::ffff:192.0.2.1"), 80),
	} {
		if _, err := probe.Probe(context.Background(), Candidate{ControlEndpoint: endpoint, NetworkEpoch: 1}); !errors.Is(err, ErrInvalidCandidate) {
			t.Fatalf("invalid literal endpoint %s error = %v", endpoint, err)
		}
	}
}
