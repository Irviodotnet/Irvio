// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type privateFinderFunc func(context.Context, [32]byte) ([]peer.ID, error)

func (f privateFinderFunc) FindPrivateOwners(ctx context.Context, locator [32]byte) ([]peer.ID, error) {
	return f(ctx, locator)
}

type privateLookupFunc func(context.Context, peer.ID, peer.ID, [32]byte) (Candidate, error)

func (f privateLookupFunc) LookupPrivate(ctx context.Context, owner, target peer.ID, locator [32]byte) (Candidate, error) {
	return f(ctx, owner, target, locator)
}

func TestPrivateLookupPartialOwnersAndMigration(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	first, owner1 := privateRegistryFixture(t, &now)
	second, owner2 := privateRegistryFixture(t, &now)
	key, target := testIdentity(t)
	locator := [32]byte{9}
	signed := signedPrivateExit(t, key, target, now, 1)
	for _, registry := range []*Registry{first, second} {
		_, err := registry.RegisterPrivate(context.Background(), target, RegistrationID{1}, ChannelToken{1}, signed, netip.MustParseAddr("198.51.100.10"), locator)
		privateMust(t, err)
	}
	var migrated atomic.Bool
	var requests atomic.Int32
	finder := privateFinderFunc(func(context.Context, [32]byte) ([]peer.ID, error) {
		if migrated.Load() {
			return []peer.ID{owner2}, nil
		}
		return []peer.ID{owner1, owner1, owner2}, nil
	})
	client := privateLookupFunc(func(_ context.Context, owner, want peer.ID, got [32]byte) (Candidate, error) {
		requests.Add(1)
		if want != target || got != locator {
			t.Error("lookup changed pinned target")
		}
		if owner == owner1 {
			return Candidate{}, ErrTimeout
		}
		return second.LookupPrivate(want, got)
	})
	lookup, err := NewPrivateLookup(finder, client, target, locator)
	privateMust(t, err)
	result, err := lookup.Lookup(context.Background())
	privateMust(t, err)
	if !result.Partial || len(result.Candidate.Routes) != 1 || result.Candidate.Routes[0].ServerPeer != owner2 || requests.Load() != 2 {
		t.Fatal("partial lookup failed or duplicated owner")
	}
	migrated.Store(true)
	result, err = lookup.Lookup(context.Background())
	privateMust(t, err)
	if result.Partial || requests.Load() != 3 {
		t.Fatal("lookup reused old owners after migration")
	}
	first.Close()
	second.Close()
	_, err = lookup.Lookup(context.Background())
	if !errors.Is(err, ErrPrivateLookupIncomplete) {
		t.Fatalf("stale owners treated as live: %v", err)
	}
}

func TestPrivateLookupRejectsWrongOwnerAndCancellation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	registry, owner := privateRegistryFixture(t, &now)
	_, impostor := testIdentity(t)
	key, target := testIdentity(t)
	locator := [32]byte{8}
	_, err := registry.RegisterPrivate(context.Background(), target, RegistrationID{1}, ChannelToken{1}, signedPrivateExit(t, key, target, now, 1), netip.MustParseAddr("198.51.100.10"), locator)
	privateMust(t, err)
	finder := privateFinderFunc(func(context.Context, [32]byte) ([]peer.ID, error) { return []peer.ID{impostor}, nil })
	client := privateLookupFunc(func(context.Context, peer.ID, peer.ID, [32]byte) (Candidate, error) {
		return registry.LookupPrivate(target, locator)
	})
	lookup, err := NewPrivateLookup(finder, client, target, locator)
	privateMust(t, err)
	if _, err := lookup.Lookup(context.Background()); !errors.Is(err, ErrPrivateLookupIncomplete) {
		t.Fatalf("owner %s receipt replay accepted from impostor: %v", owner, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lookup.Lookup(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
