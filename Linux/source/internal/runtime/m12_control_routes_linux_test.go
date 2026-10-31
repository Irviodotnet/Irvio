// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

func TestM12DualRoleControlRoutesBindEachLeaseToItsLocalSource(t *testing.T) {
	upstream := tunnellease.Lease{IPv4: netip.MustParsePrefix("10.77.0.2/32")}
	downstream := tunnellease.Lease{ID: tunnellease.ID{1}, IPv4: netip.MustParsePrefix("10.88.0.2/32")}
	plane := &exitDataPlane{
		config: ExitServerConfig{TUNAddresses: []netip.Prefix{netip.MustParsePrefix("10.88.0.1/24")}},
		resources: map[tunnellease.ID]*activeExitResource{
			downstream.ID: {lease: downstream},
		},
		upstream: &dualRoleUpstream{
			lease: upstream, controlDst: []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")},
		},
	}
	routes, err := plane.tunnelControlRoutesLocked(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[netip.Prefix]netip.Addr{
		netip.MustParsePrefix("10.77.0.1/32"): netip.MustParseAddr("10.77.0.2"),
		netip.MustParsePrefix("10.88.0.2/32"): netip.MustParseAddr("10.88.0.1"),
	}
	if len(routes) != len(want) {
		t.Fatalf("routes=%+v", routes)
	}
	for _, route := range routes {
		if source, ok := want[route.Destination]; !ok || source != route.Source {
			t.Fatalf("route=%+v want=%v", route, want)
		}
	}
}
