// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"errors"
	"github.com/libp2p/go-libp2p/core/peer"
	"testing"
	"time"
)

func TestDistributorRecoveryRegistrationAcceptsPartialDiscovery(t *testing.T) {
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	server, key := newRendezvousHost(t)
	registry, err := NewRegistry(RegistryOptions{Config: config, ServerPeer: server.ID(), ServerIncarnation: 1, Signer: key, Resolver: fixedCountryResolver{country: "NL"}, ControlAddresses: []string{hostControlAddress(t, server)}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{Host: server, Registry: registry, StreamTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	exit, exitKey := newRendezvousHost(t)
	connectRendezvousHosts(t, exit, server)
	signed := testSignedExit(t, exitKey, exit.ID(), time.Now(), 1, 1, true)
	manager, err := NewRegistrationManager(RegistrationManagerOptions{Host: exit, Servers: partialServerProvider{[]peer.ID{server.ID()}}, SignedDescriptor: func() []byte { return signed }, Handler: &echoSignalHandler{seen: make(chan []byte, 1)}, Config: config, OperationTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	manager.ctx = context.Background()
	first := manager.reconcile()
	manager.options.Servers = StaticServerProvider{Peers: []peer.ID{server.ID()}}
	second := manager.reconcile()
	for _, r := range manager.active {
		defer r.Close(context.Background())
	}
	if second != 1 {
		t.Fatalf("control failed: ordinary discovery registration count=%d", second)
	}
	if first != 1 {
		t.Fatalf("live server + partial lookup error gives %d registrations; same server without error gives %d", first, second)
	}
}

func TestDistributorRecoveryFanoutAcceptsPartialDiscovery(t *testing.T) {
	server, key := newRendezvousHost(t)
	config := DefaultConfig()
	registry, err := NewRegistry(RegistryOptions{Config: config, ServerPeer: server.ID(), ServerIncarnation: 1, Signer: key, Resolver: fixedCountryResolver{country: "NL"}, ControlAddresses: []string{hostControlAddress(t, server)}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{Host: server, Registry: registry, AuthorizeSample: func(context.Context, peer.ID) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	clientHost, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, clientHost, server)
	client, err := NewClient(clientHost, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	forwarder, err := NewVerifiedForwarder(client, 16)
	if err != nil {
		t.Fatal(err)
	}
	sampler, err := NewFanoutSampler(FanoutSamplerOptions{Local: clientHost.ID(), Client: client, Provider: partialServerProvider{[]peer.ID{server.ID()}}, Forwarder: forwarder})
	if err != nil {
		t.Fatal(err)
	}
	_, _, firstErr := sampler.SampleRemote(context.Background(), testPeerID(t), "NL", 5, nil)
	firstRPCs := sampler.Snapshot().RPCs
	sampler.provider = StaticServerProvider{Peers: []peer.ID{server.ID()}}
	_, _, secondErr := sampler.SampleRemote(context.Background(), testPeerID(t), "NL", 5, nil)
	if secondErr != nil {
		t.Fatalf("control RPC failed: %v", secondErr)
	}
	if firstErr != nil {
		t.Fatalf("partial discovery dropped reachable sampler: err=%v RPCs=%d; control RPCs=%d", firstErr, firstRPCs, sampler.Snapshot().RPCs)
	}
}

func TestDistributorRecoveryDeadCacheBlocksHealthyReplacement(t *testing.T) {
	now := time.Now()
	old, live := testPeerID(t), testPeerID(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	provider := &DiscoveryServerProvider{now: func() time.Time { return now }, slots: make(chan struct{}, 1), ctx: ctx, cancel: cancel, inflight: make(map[string]*discoveryCall), negative: make(map[string]time.Time), refresh: make(map[string]time.Time), lookupOverride: func(context.Context, string) ([]peer.ID, error) {
		calls++
		if calls == 1 {
			return []peer.ID{old}, nil
		}
		return []peer.ID{live}, nil
	}}
	defer provider.Close()
	fixture := &countryListerFixture{values: map[peer.ID][]string{old: {"NL"}, live: {"NL"}}, errors: make(map[peer.ID]error)}
	aggregator, err := NewCountryAggregator(CountryAggregatorOptions{Local: testPeerID(t), Client: fixture, Provider: provider, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	first, err := aggregator.Get(ctx, true)
	if err != nil || first.Status != AvailableCountriesReady {
		t.Fatalf("baseline: %+v %v", first, err)
	}
	fixture.errors[old] = errors.New("old distributor permanently removed")
	for i := 0; i < 2; i++ {
		now = now.Add(DefaultAvailableCountryTTL)
		_, err = aggregator.Get(ctx, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	final, _ := aggregator.Get(ctx, true)
	if final.Status != AvailableCountriesReady {
		t.Fatalf("replacement unavailable after two normal polling intervals: status=%d lookups=%d", final.Status, calls)
	}
}

func TestDiscoveryFailureFeedbackIsBoundedAndExpires(t *testing.T) {
	now := time.Now()
	provider := &DiscoveryServerProvider{now: func() time.Time { return now }, refresh: make(map[string]time.Time), negative: make(map[string]time.Time)}
	first := testPeerID(t)
	provider.cache = []peer.ID{first}
	provider.refresh[""] = now.Add(time.Hour)
	provider.ReportFailure(first)
	if len(provider.cache) != 0 || len(provider.refresh) != 0 {
		t.Fatal("failure kept stale cache")
	}
	call := &discoveryCall{}
	provider.finishLookup("", call, []peer.ID{first}, nil)
	if len(call.values) != 0 || len(provider.cache) != 0 {
		t.Fatal("in-flight stale lookup resurrected failed server")
	}
	for i := 0; i < 2*MaxRendezvousCache; i++ {
		provider.ReportFailure(testPeerID(t))
	}
	if len(provider.failed) > MaxRendezvousCache {
		t.Fatal("unbounded failure memory")
	}
	provider.ReportFailure(first)
	now = now.Add(failedServerCooldown + time.Second)
	provider.finishLookup("", call, []peer.ID{first}, nil)
	if len(call.values) != 1 {
		t.Fatal("recovered server remains suppressed")
	}
}

func TestDiscoverySynchronousLookupHonorsFailureCooldown(t *testing.T) {
	now := time.Now()
	failed := testPeerID(t)
	healthy := testPeerID(t)
	provider := &DiscoveryServerProvider{
		now: func() time.Time { return now }, slots: make(chan struct{}, 1),
		inflight: make(map[string]*discoveryCall), refresh: make(map[string]time.Time),
		negative: make(map[string]time.Time),
		lookupOverride: func(context.Context, string) ([]peer.ID, error) {
			return []peer.ID{failed, healthy}, nil
		},
	}
	provider.ReportFailure(failed)
	ids, err := provider.Servers(context.Background(), "NL", MaxRendezvousCache)
	if err != nil || len(ids) != 1 || ids[0] != healthy {
		t.Fatalf("synchronous lookup bypassed cooldown: ids=%v err=%v", ids, err)
	}
}

func TestDiscoveryRefreshesPartialCacheWhenCooldownExpires(t *testing.T) {
	now := time.Now()
	failed, healthy := testPeerID(t), testPeerID(t)
	ctx, cancel := context.WithCancel(context.Background())
	refreshed := make(chan struct{}, 2)
	provider := &DiscoveryServerProvider{
		now: func() time.Time { return now }, ctx: ctx, cancel: cancel,
		slots: make(chan struct{}, 1), inflight: make(map[string]*discoveryCall),
		refresh: make(map[string]time.Time), negative: make(map[string]time.Time),
		lookupOverride: func(context.Context, string) ([]peer.ID, error) {
			refreshed <- struct{}{}
			return []peer.ID{failed, healthy}, nil
		},
	}
	defer provider.Close()
	provider.ReportFailure(failed)
	if _, err := provider.Servers(ctx, "NL", MaxRendezvousCache); err != nil {
		t.Fatal(err)
	}
	<-refreshed
	now = now.Add(failedServerCooldown + time.Second)
	if _, err := provider.Servers(ctx, "NL", MaxRendezvousCache); err != nil {
		t.Fatal(err)
	}
	select {
	case <-refreshed:
	case <-time.After(time.Second):
		t.Fatal("healthy cached peer postponed recovered server discovery for the full cache TTL")
	}
}
