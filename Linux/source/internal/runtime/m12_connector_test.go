// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

func TestM12ProvisionalActivationOutlivesSignalingBudget(t *testing.T) {
	now := time.Now().UTC()
	ctx, cancel := context.WithDeadline(context.Background(), now.Add(1500*time.Millisecond))
	defer cancel()

	got := m12ActivationDeadline(ctx, now)
	want := now.Add(m12ProvisionalLifetime)
	if !got.Equal(want) {
		t.Fatalf("activation deadline = %v, want independent provisional deadline %v", got, want)
	}
}
func TestM12RouteFailureIsNotPeerUnavailable(t *testing.T) {
	got := m12ConnectOutcome(fmt.Errorf("prepare delivery: %w", rendezvous.ErrRouteFailed))
	if got == exitselector.OutcomeUnavailable {
		t.Fatal("broker route failure was classified as peer unavailable")
	}
	if got != exitselector.OutcomeRouteFailed {
		t.Fatalf("broker route outcome = %d, want route-scoped failure", got)
	}
	got = m12ConnectOutcome(fmt.Errorf("resolve cached candidate: %w", rendezvous.ErrStale))
	if got == exitselector.OutcomeUnavailable {
		t.Fatal("stale broker locator was classified as peer unavailable")
	}
	if got != exitselector.OutcomeRouteFailed {
		t.Fatalf("stale broker locator outcome = %d, want route-scoped failure", got)
	}
}

func TestM12ControlAddressUsesOnlyLeaseSupportedFamily(t *testing.T) {
	values := [][]byte{
		netip.MustParseAddr("fd77::1").AsSlice(),
		netip.MustParseAddr("10.77.0.1").AsSlice(),
	}
	lease := tunnellease.Lease{IPv4: netip.MustParsePrefix("10.77.0.2/32")}
	address, routes, err := m12ControlAddress(values, 51822, lease)
	if err != nil {
		t.Fatal(err)
	}
	if address != netip.MustParseAddrPort("10.77.0.1:51822") || len(routes) != 1 || routes[0] != netip.MustParsePrefix("10.77.0.1/32") {
		t.Fatalf("control endpoint = %s routes=%v", address, routes)
	}
}

func TestM12ControlAddressRejectsUnsupportedFamilies(t *testing.T) {
	values := [][]byte{netip.MustParseAddr("fd77::1").AsSlice()}
	lease := tunnellease.Lease{IPv4: netip.MustParsePrefix("10.77.0.2/32")}
	if _, _, err := m12ControlAddress(values, 51822, lease); err == nil {
		t.Fatal("IPv6-only control endpoint was accepted for an IPv4-only lease")
	}
}

func TestM12ControlEndpointPreservesAuthenticatedPort(t *testing.T) {
	for _, address := range []string{"10.77.0.1", "fd77::1"} {
		for _, port := range []uint32{1, 443, 51822, 65535} {
			lease := tunnellease.Lease{IPv4: netip.MustParsePrefix("10.77.0.2/32"), IPv6: netip.MustParsePrefix("fd77::2/128")}
			a := netip.MustParseAddr(address)
			endpoint, routes, err := m12ControlAddress([][]byte{a.AsSlice()}, port, lease)
			if err != nil || endpoint != netip.AddrPortFrom(a, uint16(port)) || len(routes) != 1 || routes[0].Addr() != endpoint.Addr() {
				t.Fatalf("endpoint=%v routes=%v error=%v", endpoint, routes, err)
			}
		}
	}
}
