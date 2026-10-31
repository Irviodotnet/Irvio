// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package networkstate

import (
	"net/netip"
	"testing"
)

func TestSnapshotNormalizationIsDeterministic(t *testing.T) {
	t.Parallel()
	routes := []Route{
		{Family: FamilyIPv6, LinkIndex: 7, Gateway: netip.MustParseAddr("2001:db8:1::1"), Priority: 20},
		{Family: FamilyIPv4, LinkIndex: 4, Gateway: netip.MustParseAddr("192.0.2.1"), Priority: 10},
		{Family: FamilyIPv4, LinkIndex: 4, Gateway: netip.MustParseAddr("192.0.2.1"), Priority: 10},
	}
	links := []Link{
		{Index: 99, Name: "irrelevant", Up: true, MTU: 9000, Addresses: []netip.Prefix{netip.MustParsePrefix("198.51.100.2/24")}},
		{Index: 7, Name: "v6", Up: true, MTU: 1480, Addresses: []netip.Prefix{
			netip.MustParsePrefix("2001:db8:1::2/64"),
			netip.MustParsePrefix("fe80::2/64"),
		}},
		{Index: 4, Name: "v4", Up: true, MTU: 1500, Addresses: []netip.Prefix{
			netip.MustParsePrefix("192.0.2.2/24"),
			netip.MustParsePrefix("192.0.2.2/24"),
		}},
	}

	first, err := Normalize(routes, links)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Normalize([]Route{routes[2], routes[0], routes[1]}, []Link{links[2], links[0], links[1]})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(second) {
		t.Fatalf("order changed normalized snapshot:\n%+v\n%+v", first, second)
	}
	if len(first.Routes) != 2 || len(first.Links) != 2 {
		t.Fatalf("unexpected normalized sizes: routes=%d links=%d", len(first.Routes), len(first.Links))
	}
	if got := first.Links[0].Addresses; len(got) != 1 || got[0].Addr() != netip.MustParseAddr("192.0.2.2") {
		t.Fatalf("host address was not preserved: %v", got)
	}
	if got := first.Links[1].Addresses; len(got) != 1 || got[0].Addr().IsLinkLocalUnicast() {
		t.Fatalf("link-local address was not excluded: %v", got)
	}
}

func TestSnapshotSemanticFields(t *testing.T) {
	t.Parallel()
	base := mustSnapshot(t,
		[]Route{{Family: FamilyIPv4, LinkIndex: 4, Gateway: netip.MustParseAddr("192.0.2.1")}},
		[]Link{{Index: 4, Name: "eth0", Up: true, MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24")}}},
	)
	tests := []struct {
		name  string
		route Route
		link  Link
	}{
		{name: "gateway", route: Route{Family: FamilyIPv4, LinkIndex: 4, Gateway: netip.MustParseAddr("192.0.2.254")}, link: Link{Index: 4, Name: "eth0", Up: true, MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24")}}},
		{name: "preferred source", route: Route{Family: FamilyIPv4, LinkIndex: 4, Gateway: netip.MustParseAddr("192.0.2.1"), PreferredSource: netip.MustParseAddr("192.0.2.3")}, link: Link{Index: 4, Name: "eth0", Up: true, MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24"), netip.MustParsePrefix("192.0.2.3/24")}}},
		{name: "address", route: Route{Family: FamilyIPv4, LinkIndex: 4, Gateway: netip.MustParseAddr("192.0.2.1")}, link: Link{Index: 4, Name: "eth0", Up: true, MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.3/24")}}},
		{name: "link state", route: Route{Family: FamilyIPv4, LinkIndex: 4, Gateway: netip.MustParseAddr("192.0.2.1")}, link: Link{Index: 4, Name: "eth0", Up: false, MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24")}}},
		{name: "mtu", route: Route{Family: FamilyIPv4, LinkIndex: 4, Gateway: netip.MustParseAddr("192.0.2.1")}, link: Link{Index: 4, Name: "eth0", Up: true, MTU: 1400, Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := mustSnapshot(t, []Route{tt.route}, []Link{tt.link})
			if base.Equal(changed) {
				t.Fatal("semantic change compared equal")
			}
		})
	}
}

func TestSnapshotCloneIsIndependent(t *testing.T) {
	t.Parallel()
	original := mustSnapshot(t,
		[]Route{{Family: FamilyIPv6, LinkIndex: 7, Gateway: netip.MustParseAddr("2001:db8::1")}},
		[]Link{{Index: 7, Name: "v6", Up: true, MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8::2/64")}}},
	)
	clone := original.Clone()
	clone.Links[0].Addresses[0] = netip.MustParsePrefix("2001:db8::3/64")
	if original.Links[0].Addresses[0] == clone.Links[0].Addresses[0] {
		t.Fatal("clone shared address storage")
	}
}

func mustSnapshot(t *testing.T, routes []Route, links []Link) Snapshot {
	t.Helper()
	snapshot, err := Normalize(routes, links)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
