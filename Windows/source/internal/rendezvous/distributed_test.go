// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type failingServerProvider struct{ err error }

type partialServerProvider struct{ peers []peer.ID }

func (p partialServerProvider) Servers(context.Context, string, int) ([]peer.ID, error) {
	return p.peers, context.DeadlineExceeded
}

func TestCandidateSelectionUsesPartialDiscoveryWhileRequestIsLive(t *testing.T) {
	live := testPeerID(t)
	p := &CandidateProvider{local: testPeerID(t), servers: partialServerProvider{[]peer.ID{live}}, primary: make(map[string]peer.ID), fallback: make(map[string][]peer.ID)}
	values, err := p.selectServers(context.Background(), "NL")
	if err != nil || !slices.Equal(values, []peer.ID{live}) {
		t.Fatalf("partial discovery discarded: %v %v", values, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.selectServers(ctx, "NL"); err == nil {
		t.Fatal("canceled caller must not start acquisition")
	}
}

func TestUnionKeepsPeersFromPartialDiscovery(t *testing.T) {
	live := testPeerID(t)
	values, err := (UnionServerProvider{partialServerProvider{[]peer.ID{live}}}).Servers(context.Background(), "", 3)
	if err != nil || !slices.Equal(values, []peer.ID{live}) {
		t.Fatalf("partial union discarded: %v %v", values, err)
	}
}

func TestColdCountryDiscoveryPreservesVerifiedPeersOnPartialTimeout(t *testing.T) {
	live := testPeerID(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &DiscoveryServerProvider{
		now: time.Now, slots: make(chan struct{}, 1), ctx: ctx, cancel: cancel,
		inflight: make(map[string]*discoveryCall), negative: make(map[string]time.Time),
		refresh: make(map[string]time.Time),
		lookupOverride: func(context.Context, string) ([]peer.ID, error) {
			return []peer.ID{live}, context.DeadlineExceeded
		},
	}
	t.Cleanup(func() { _ = provider.Close() })
	fixture := &countryListerFixture{values: map[peer.ID][]string{live: {"NL"}}}
	aggregator, err := NewCountryAggregator(CountryAggregatorOptions{
		Local: testPeerID(t), Client: fixture, Provider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := aggregator.Get(context.Background(), true)
	if err != nil || result.Status != AvailableCountriesPartial || !slices.Equal(result.Codes, []string{"NL"}) {
		t.Fatalf("cold partial discovery: result=%+v err=%v", result, err)
	}
	if cached, _ := provider.Snapshot(); cached != 1 {
		t.Fatalf("verified peer was not cached: %d", cached)
	}
}

func TestRendezvousDiscoveryFallbackPreservesPartialTimeoutResults(t *testing.T) {
	live := testPeerID(t)
	calls := 0
	provider := &DiscoveryServerProvider{
		slots: make(chan struct{}, 1),
		lookupOverride: func(_ context.Context, country string) ([]peer.ID, error) {
			calls++
			if country == "NL" {
				return nil, nil
			}
			return []peer.ID{live}, context.DeadlineExceeded
		},
	}
	values, err := provider.lookup(context.Background(), "NL")
	if calls != 2 || !errors.Is(err, context.DeadlineExceeded) || !slices.Equal(values, []peer.ID{live}) {
		t.Fatalf("fallback partial discovery: calls=%d values=%v err=%v", calls, values, err)
	}
}

func (p failingServerProvider) Servers(context.Context, string, int) ([]peer.ID, error) {
	return nil, p.err
}

func TestUnionServerProviderPreservesConfiguredPeersAndDeduplicatesDiscovery(t *testing.T) {
	provider := UnionServerProvider{
		StaticServerProvider{Peers: []peer.ID{"configured", "shared"}},
		StaticServerProvider{Peers: []peer.ID{"shared", "discovered"}},
	}
	values, err := provider.Servers(context.Background(), "DE", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []peer.ID{"configured", "shared", "discovered"}
	if len(values) != len(want) {
		t.Fatalf("servers = %v, want %v", values, want)
	}
	for index := range want {
		if values[index] != want[index] {
			t.Fatalf("servers = %v, want %v", values, want)
		}
	}
}

func TestUnionServerProviderUsesHealthySourceAndReportsTotalFailure(t *testing.T) {
	failure := errors.New("discovery unavailable")
	provider := UnionServerProvider{
		failingServerProvider{err: failure},
		StaticServerProvider{Peers: []peer.ID{"configured"}},
	}
	values, err := provider.Servers(context.Background(), "DE", 3)
	if err != nil || len(values) != 1 || values[0] != "configured" {
		t.Fatalf("healthy configured source: values=%v err=%v", values, err)
	}
	_, err = UnionServerProvider{failingServerProvider{err: failure}}.Servers(context.Background(), "DE", 3)
	if !errors.Is(err, failure) {
		t.Fatalf("total failure = %v, want %v", err, failure)
	}
}

func TestFallbackServerProviderDoesNotBypassHealthyConfiguredIngress(t *testing.T) {
	provider := FallbackServerProvider{
		Primary:  StaticServerProvider{Peers: []peer.ID{"ingress"}},
		Fallback: StaticServerProvider{Peers: []peer.ID{"owner"}},
	}
	values, err := provider.Servers(context.Background(), "DE", 3)
	if err != nil || len(values) != 1 || values[0] != "ingress" {
		t.Fatalf("configured ingress = %v err=%v", values, err)
	}
	provider.Primary = failingServerProvider{err: errors.New("ingress unavailable")}
	values, err = provider.Servers(context.Background(), "DE", 3)
	if err != nil || len(values) != 1 || values[0] != "owner" {
		t.Fatalf("discovered fallback = %v err=%v", values, err)
	}
}

func TestDiscoveryCacheRefreshDoesNotBlockCaller(t *testing.T) {
	oldHost, _ := newRendezvousHost(t)
	newHost, _ := newRendezvousHost(t)
	started := make(chan struct{})
	release := make(chan struct{})
	providerContext, cancel := context.WithCancel(context.Background())
	provider := &DiscoveryServerProvider{
		now: time.Now, slots: make(chan struct{}, 1), ctx: providerContext, cancel: cancel,
		cache: []peer.ID{oldHost.ID()}, inflight: make(map[string]*discoveryCall),
		negative: make(map[string]time.Time), refresh: map[string]time.Time{"DE": time.Now().Add(-time.Second)},
		lookupOverride: func(ctx context.Context, country string) ([]peer.ID, error) {
			if country != "DE" {
				t.Fatalf("country=%q", country)
			}
			close(started)
			select {
			case <-release:
				return []peer.ID{newHost.ID()}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}
	t.Cleanup(func() { _ = provider.Close() })

	values, err := provider.Servers(context.Background(), "DE", 3)
	if err != nil || len(values) != 1 || values[0] != oldHost.ID() {
		t.Fatalf("cached values=%v err=%v", values, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}
	provider.mu.Lock()
	call := provider.inflight["DE"]
	provider.mu.Unlock()
	if call == nil {
		t.Fatal("background refresh is not tracked")
	}
	close(release)
	select {
	case <-call.done:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not finish")
	}
	cached, inFlight := provider.Snapshot()
	if cached != 2 || inFlight != 0 {
		t.Fatalf("cached=%d inflight=%d", cached, inFlight)
	}
}
