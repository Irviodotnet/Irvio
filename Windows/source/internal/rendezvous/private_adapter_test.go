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
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPrivateCandidateProviderFencesCache(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	registry, owner := privateRegistryFixture(t, &now)
	key, target := testIdentity(t)
	locator := [32]byte{13}
	signed := signedPrivateExit(t, key, target, now, 1)
	register := func(id byte) {
		_, err := registry.RegisterPrivate(context.Background(), target, RegistrationID{id}, ChannelToken{id}, signed, netip.MustParseAddr("198.51.100.10"), locator)
		privateMust(t, err)
	}
	register(1)
	finder := privateFinderFunc(func(context.Context, [32]byte) ([]peer.ID, error) { return []peer.ID{owner}, nil })
	lookupClient := privateLookupFunc(func(_ context.Context, _ peer.ID, target peer.ID, locator [32]byte) (Candidate, error) {
		return registry.LookupPrivate(target, locator)
	})
	lookup, err := NewPrivateLookup(finder, lookupClient, target, locator)
	privateMust(t, err)
	host, _ := newRendezvousHost(t)
	client, err := NewClient(host, time.Second)
	privateMust(t, err)
	provider, err := NewPrivateCandidateProvider(host.ID(), lookup, client)
	privateMust(t, err)
	provider.now = func() time.Time { return now }
	request := exitselector.CandidateRequest{Target: exitselector.Target{Kind: exitselector.TargetPrivatePeer, PeerID: target}, Limit: 5}
	get := func() exitselector.Candidate {
		batch, err := provider.GetCandidates(context.Background(), request)
		privateMust(t, err)
		if batch.Target != request.Target || batch.Country != "" || batch.AvailablePeerCount != 1 || len(batch.Candidates) != 1 {
			t.Fatalf("wrong private batch: %+v", batch)
		}
		return batch.Candidates[0]
	}
	first := get()
	if first.Country != "" || first.ExpiresAt.After(now.Add(30*time.Second)) {
		t.Fatal("private candidate escaped bounded country-free contract")
	}
	resolved, err := provider.Resolve(first)
	privateMust(t, err)
	for _, receipt := range resolved.Receipts {
		if first.ExpiresAt.After(receipt.Receipt.ExpiresAt) {
			t.Fatal("cache outlives receipt")
		}
	}
	_, err = provider.BrokerCarrier(first)
	privateMust(t, err)
	probe := MetadataProbe{Provider: provider, Now: provider.now}
	observation, err := probe.Probe(context.Background(), first)
	privateMust(t, err)
	if !observation.Reachable || observation.Kind != exitselector.ProbeBrokerMetadata {
		t.Fatal("private metadata probe invented direct reachability")
	}
	resolved.Descriptor.SignedEnvelope[0] ^= 1
	_, err = provider.BrokerCarrier(first)
	privateMust(t, err)
	changed := first
	changed.Country = "NL"
	if _, err := provider.Resolve(changed); !errors.Is(err, ErrStale) {
		t.Fatal("modified candidate accepted")
	}
	saved := now
	now = first.ExpiresAt
	if _, err := provider.Resolve(first); !errors.Is(err, ErrStale) {
		t.Fatal("expired cache accepted")
	}
	now = saved
	register(2)
	second := get()
	if second.SignalingRouteEpoch == first.SignalingRouteEpoch {
		t.Fatal("registration replacement did not fence route")
	}
	if _, err := provider.Resolve(first); !errors.Is(err, ErrStale) {
		t.Fatal("old registration still cached")
	}
	request.ExcludePeerIDs = []peer.ID{target}
	batch, err := provider.GetCandidates(context.Background(), request)
	privateMust(t, err)
	if len(batch.Candidates) != 0 {
		t.Fatal("excluded private identity returned")
	}
	if _, err := provider.Resolve(second); !errors.Is(err, ErrStale) {
		t.Fatal("exclusion retained route")
	}
	request.ExcludePeerIDs = nil
	second = get()
	registry.Close()
	if _, err := provider.GetCandidates(context.Background(), request); !errors.Is(err, ErrPrivateLookupIncomplete) {
		t.Fatalf("lookup error hidden: %v", err)
	}
	if _, err := provider.Resolve(second); !errors.Is(err, ErrStale) {
		t.Fatal("failed lookup retained route")
	}
	request.Target = exitselector.Target{}
	if _, err := provider.GetCandidates(context.Background(), request); !errors.Is(err, ErrInvalid) {
		t.Fatal("public request accepted")
	}
}
