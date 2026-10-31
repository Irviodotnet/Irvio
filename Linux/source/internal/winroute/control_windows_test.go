// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func controlAddresses() []netip.Addr {
	return []netip.Addr{netip.MustParseAddr("10.77.0.1"), netip.MustParseAddr("fd77::1")}
}

func TestOwnedControlRoutes(t *testing.T) {
	m, b, j := fakeRoutes(t)
	ctx := context.Background()
	if err := m.EnsureControl(ctx, controlAddresses()); err != nil {
		t.Fatal(err)
	}
	if j.r.Version != 2 || len(b.routes) != 2 {
		t.Fatal("missing versioned host route journal")
	}
	for p := range b.routes {
		if p.Bits() != p.Addr().BitLen() {
			t.Fatal("control route permits a prefix")
		}
	}
	if err := m.EnsureControl(ctx, controlAddresses()); err != nil {
		t.Fatal("idempotence", err)
	}
	if err := m.Ensure(ctx, true, true); !errors.Is(err, ErrRouteConflict) {
		t.Fatal("silent full-route promotion", err)
	}
	if err := m.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.routes) != 0 || j.r.Version != 1 || len(j.r.Prefixes) != 0 {
		t.Fatal("control route residue")
	}
	if err := m.Ensure(ctx, true, true); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureControl(ctx, controlAddresses()); !errors.Is(err, ErrRouteConflict) {
		t.Fatal("mixed route owners", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnedControlRouteValidation(t *testing.T) {
	for _, addresses := range [][]netip.Addr{
		nil, {{}}, {netip.MustParseAddr("0.0.0.0")}, {netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("::ffff:10.77.0.1")}, {netip.MustParseAddr("ff02::1")},
		{netip.MustParseAddr("fd77::1%3")}, {controlAddresses()[0], controlAddresses()[0]},
		{controlAddresses()[0], controlAddresses()[1], netip.MustParseAddr("10.77.0.3")},
	} {
		m, b, j := fakeRoutes(t)
		if err := m.EnsureControl(context.Background(), addresses); err == nil {
			t.Fatalf("accepted %v", addresses)
		}
		if j.writes != 0 || b.adds != 0 {
			t.Fatal("invalid route mutated state")
		}
	}
	for _, p := range []string{"10.77.0.0/24", "0.0.0.0/1", "::/1", "127.0.0.1/32"} {
		if err := validateRouteRecord(routeRecord{Version: 2, Interface: testRouteID, Prefixes: []netip.Prefix{netip.MustParsePrefix(p)}}); err == nil {
			t.Fatal("unsafe v2 route journal", p)
		}
	}
}

func TestOwnedControlRouteRollbackAndRecovery(t *testing.T) {
	for _, collision := range []bool{false, true} {
		m, b, j := fakeRoutes(t)
		b.failAdd, b.collide, b.failDelete = 2, collision, true
		if err := m.EnsureControl(context.Background(), controlAddresses()); !errors.Is(err, errRouteInjected) {
			t.Fatal(err)
		}
		if j.r.Version != 2 || len(j.r.Prefixes) != 1 {
			t.Fatal("rollback lost exact partial ownership", j.r)
		}
		b.failDelete = false
		recovered, err := newTunnelRoutes(testRouteID, b, j)
		if err != nil {
			t.Fatal(err)
		}
		if err := recovered.Remove(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(b.routes) != 0 && !collision {
			t.Fatal("partial owned route residue")
		}
		if collision {
			p := netip.PrefixFrom(controlAddresses()[1], 128)
			if len(b.routes) != 1 || !b.routes[p] {
				t.Fatal("competing route was deleted")
			}
		}
	}
}
