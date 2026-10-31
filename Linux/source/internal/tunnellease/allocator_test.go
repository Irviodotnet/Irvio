// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnellease

import (
	"bytes"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func testAllocator(t *testing.T) (*Allocator, *time.Time) {
	t.Helper()
	now := time.Unix(1000, 0).UTC()
	config := DefaultConfig(peer.ID("exit"))
	config.IPv4Pool = netip.MustParsePrefix("10.77.0.0/29")
	config.IPv6Pool = netip.MustParsePrefix("fd77::/124")
	config.Reserved = []netip.Addr{netip.MustParseAddr("10.77.0.1"), netip.MustParseAddr("fd77::1")}
	config.MaxActive = 3
	config.MaxRecent = 2
	config.TTL = time.Minute
	config.Now = func() time.Time { return now }
	random := make([]byte, 256)
	for index := range random {
		random[index] = byte(index + 1)
	}
	config.Random = bytes.NewReader(random)
	allocator, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return allocator, &now
}

func TestAllocatorIssuesUniqueDualStackLeasesAndRejectsWrongPeer(t *testing.T) {
	allocator, _ := testAllocator(t)
	first, err := allocator.Allocate(peer.ID("client-a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := allocator.Allocate(peer.ID("client-b"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.IPv4 == second.IPv4 || first.IPv6 == second.IPv6 {
		t.Fatal("allocator reused active lease identity or address")
	}
	if first.IPv4.Bits() != 32 || first.IPv6.Bits() != 128 {
		t.Fatalf("lease prefixes = %s/%s", first.IPv4, first.IPv6)
	}
	if err := allocator.Release(peer.ID("client-b"), first.ID); !errors.Is(err, ErrWrongPeer) {
		t.Fatalf("wrong-peer release error = %v", err)
	}
	if err := allocator.Release(first.ClientPeer, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := allocator.Release(first.ClientPeer, first.ID); !errors.Is(err, ErrLeaseReleased) {
		t.Fatalf("release replay error = %v", err)
	}
}

func TestAllocatorExpiryAndBoundedCapacity(t *testing.T) {
	allocator, now := testAllocator(t)
	for _, client := range []peer.ID{"a", "b", "c"} {
		if _, err := allocator.Allocate(client); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := allocator.Allocate(peer.ID("d")); !errors.Is(err, ErrExhausted) {
		t.Fatalf("capacity error = %v", err)
	}
	*now = now.Add(2 * time.Minute)
	if active := allocator.Active(); len(active) != 0 {
		t.Fatalf("active after expiry = %d", len(active))
	}
	if _, err := allocator.Allocate(peer.ID("d")); err != nil {
		t.Fatal(err)
	}
}

func TestAllocatorReturnsExistingLeaseForSameClient(t *testing.T) {
	allocator, _ := testAllocator(t)
	first, err := allocator.Allocate(peer.ID("client"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := allocator.Allocate(peer.ID("client"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(allocator.Active()) != 1 {
		t.Fatal("same client did not receive its existing active lease")
	}
}

func TestAllocatorAbandonedLeaseExpiresAndSameClientCanReturn(t *testing.T) {
	allocator, now := testAllocator(t)
	client := peer.ID("abandoned-client")
	first, err := allocator.Allocate(client)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	if active := allocator.Active(); len(active) != 0 {
		t.Fatalf("abandoned lease remained after valid_until: %d", len(active))
	}
	second, err := allocator.Allocate(client)
	if err != nil {
		t.Fatalf("same authenticated client could not reconnect: %v", err)
	}
	if second.ID == first.ID || second.ClientPeer != client || !second.ValidUntil.After(*now) {
		t.Fatal("returning client did not receive a fresh bounded lease generation")
	}
}
