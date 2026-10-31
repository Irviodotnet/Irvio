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
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/protocol"
)

func TestAvailableCountriesProtocolReturnsOnlyLocalLiveCountryCodes(t *testing.T) {
	serverHost, serverKey := newRendezvousHost(t)
	exitHost, exitKey := newRendezvousHost(t)
	clientHost, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, clientHost, serverHost)
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	registry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: 41,
		Signer: serverKey, Resolver: fixedCountryResolver{country: "NL"},
		ControlAddresses: []string{hostControlAddress(t, serverHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := NewRegistrationID(nil)
	token, _ := NewChannelToken(nil)
	if _, err := registry.Register(context.Background(), exitHost.ID(), id, token,
		testSignedExit(t, exitKey, exitHost.ID(), time.Now(), 1, 1, true),
		mustObservedAddress("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{Host: serverHost, Registry: registry, StreamTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	client, err := NewClient(clientHost, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	countries, err := client.ListLocalAvailableCountries(context.Background(), serverHost.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(countries, []string{"NL"}) {
		t.Fatalf("countries=%v, want [NL]", countries)
	}
	connectRendezvousHosts(t, exitHost, serverHost)
	exitClient, err := NewClient(exitHost, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	countries, err = exitClient.ListLocalAvailableCountries(context.Background(), serverHost.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(countries) != 0 {
		t.Fatalf("sole Exit sees its own country: %v", countries)
	}
	if got := registry.Countries(); !slices.Equal(got, []string{"NL"}) {
		t.Fatalf("requester filtering changed global country index: %v", got)
	}
	// Another identity on the same observed IP must make the country usable.
	otherHost, otherKey := newRendezvousHost(t)
	otherID, _ := NewRegistrationID(nil)
	otherToken, _ := NewChannelToken(nil)
	registration, err := registry.Register(context.Background(), otherHost.ID(), otherID, otherToken,
		testSignedExit(t, otherKey, otherHost.ID(), time.Now(), 1, 1, true),
		mustObservedAddress("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	countries, err = exitClient.ListLocalAvailableCountries(context.Background(), serverHost.ID())
	if err != nil || !slices.Equal(countries, []string{"NL"}) {
		t.Fatalf("same-IP different PeerID countries=%v, error=%v", countries, err)
	}
	if _, err := registry.Update(context.Background(), registration.Owner,
		testSignedExit(t, otherKey, otherHost.ID(), time.Now(), 1, 2, false)); err != nil {
		t.Fatal(err)
	}
	countries, err = exitClient.ListLocalAvailableCountries(context.Background(), serverHost.ID())
	if err != nil || len(countries) != 0 {
		t.Fatalf("non-accepting alternative countries=%v, error=%v", countries, err)
	}
}

func TestRegistryCountriesUsesExistingFreshAcceptingObservedIndex(t *testing.T) {
	serverHost, serverKey := newRendezvousHost(t)
	base := time.Unix(1_900_000_000, 0).UTC()
	clock := base
	config := DefaultConfig()
	config.Now = func() time.Time { return clock }
	config.ObservedIPPolicy.AllowDocumentation = true
	config.RegistrationStale = time.Minute
	config.RegistrationExpiry = 2 * time.Minute
	registry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: 42,
		Signer: serverKey, Resolver: fixedCountryResolver{country: "NL"},
		ControlAddresses: []string{hostControlAddress(t, serverHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	exitKey, _, _ := crypto.GenerateEd25519Key(nil)
	exitID, _ := peer.IDFromPrivateKey(exitKey)
	id, _ := NewRegistrationID(nil)
	token, _ := NewChannelToken(nil)
	owner, err := registry.Register(context.Background(), exitID, id, token,
		testSignedExit(t, exitKey, exitID, base, 1, 1, true), mustObservedAddress("198.51.100.10"))
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Countries(); !slices.Equal(got, []string{"NL"}) {
		t.Fatalf("fresh countries=%v", got)
	}
	if _, err := registry.Update(context.Background(), owner.Owner,
		testSignedExit(t, exitKey, exitID, base, 1, 2, false)); err != nil {
		t.Fatal(err)
	}
	if got := registry.Countries(); len(got) != 0 {
		t.Fatalf("non-accepting countries=%v", got)
	}
	clock = base.Add(config.RegistrationStale + time.Nanosecond)
	if _, err := registry.Update(context.Background(), owner.Owner,
		testSignedExit(t, exitKey, exitID, base, 1, 3, true)); err != nil {
		t.Fatal(err)
	}
	if got := registry.Countries(); len(got) != 0 {
		t.Fatalf("stale countries=%v", got)
	}
	clock = base.Add(config.RegistrationExpiry + time.Nanosecond)
	if got := registry.Countries(); len(got) != 0 {
		t.Fatalf("expired countries=%v", got)
	}
	if _, exists := registry.Lookup(exitID); exists {
		t.Fatal("expired registration remained live")
	}
}

func TestRegistryCountriesOmitsUnknownAndClosedRegistrations(t *testing.T) {
	base := time.Unix(1_900_000_000, 0).UTC()
	registry, _ := testRegistry(t, &base, fixedCountryResolver{err: ErrCountryDegraded})
	exitKey, exitID := testIdentity(t)
	id, _ := NewRegistrationID(nil)
	token, _ := NewChannelToken(nil)
	registration, err := registry.Register(context.Background(), exitID, id, token,
		testSignedExit(t, exitKey, exitID, base, 1, 1, true), mustObservedAddress("198.51.100.11"))
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Countries(); len(got) != 0 {
		t.Fatalf("unknown country leaked into index: %v", got)
	}
	if !registry.ChannelClosed(registration.Owner) {
		t.Fatal("owning channel close was not applied")
	}
	if got := registry.Countries(); len(got) != 0 {
		t.Fatalf("closed registration remained indexed: %v", got)
	}
}

func TestRegistryCountriesTreatsExitOwnersEqually(t *testing.T) {
	base := time.Unix(1_900_000_000, 0).UTC()
	registry, _ := testRegistry(t, &base, fixedCountryResolver{country: "NL"})
	for index := 0; index < 2; index++ {
		exitKey, exitID := testIdentity(t)
		id, _ := NewRegistrationID(nil)
		token, _ := NewChannelToken(nil)
		if _, err := registry.Register(context.Background(), exitID, id, token,
			testSignedExit(t, exitKey, exitID, base, 1, uint64(index+1), true),
			mustObservedAddress("198.51.100.12")); err != nil {
			t.Fatal(err)
		}
	}
	if got := registry.Countries(); !slices.Equal(got, []string{"NL"}) {
		t.Fatalf("owner-neutral countries=%v, want [NL]", got)
	}
	registry.Close()
	if got := registry.Countries(); len(got) != 0 {
		t.Fatalf("closed registry countries=%v", got)
	}
}

func TestRegistryCountriesRejectsInfrastructureWithoutExitCapability(t *testing.T) {
	base := time.Unix(1_900_000_000, 0).UTC()
	registry, _ := testRegistry(t, &base, fixedCountryResolver{country: "DE"})
	key, id := testIdentity(t)
	registrationID, _ := NewRegistrationID(nil)
	token, _ := NewChannelToken(nil)
	_, err := registry.Register(context.Background(), id, registrationID, token,
		testSignedCapabilities(t, key, id, base, 1, 1, true, protocol.CapDiscovery),
		mustObservedAddress("198.51.100.13"))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-Exit registration error=%v", err)
	}
	if got := registry.Countries(); len(got) != 0 {
		t.Fatalf("non-Exit infrastructure produced countries=%v", got)
	}
}

type countryListerFixture struct {
	mu        sync.Mutex
	values    map[peer.ID][]string
	errors    map[peer.ID]error
	calls     int
	inFlight  atomic.Int32
	maxFlight atomic.Int32
}

func (f *countryListerFixture) ListLocalAvailableCountries(_ context.Context, server peer.ID) ([]string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	active := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		maximum := f.maxFlight.Load()
		if active <= maximum || f.maxFlight.CompareAndSwap(maximum, active) {
			break
		}
	}
	if err := f.errors[server]; err != nil {
		return nil, err
	}
	return append([]string(nil), f.values[server]...), nil
}

func testPeerID(t *testing.T) peer.ID {
	t.Helper()
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCountryDiscoveryReservesRPCBudgetWithoutPrematureTwoSecondCutoff(t *testing.T) {
	live := testPeerID(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &DiscoveryServerProvider{
		now: time.Now, slots: make(chan struct{}, 1), ctx: ctx, cancel: cancel,
		inflight: make(map[string]*discoveryCall), negative: make(map[string]time.Time), refresh: make(map[string]time.Time),
		lookupOverride: func(ctx context.Context, _ string) ([]peer.ID, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 2*time.Second || time.Until(deadline) > DefaultAvailableCountryTimeout-DefaultAvailableCountryPeerTime {
				t.Fatalf("discovery deadline does not reserve usable discovery/RPC budgets: %v", time.Until(deadline))
			}
			return []peer.ID{live}, nil
		},
	}
	t.Cleanup(func() { _ = provider.Close() })
	aggregator, err := NewCountryAggregator(CountryAggregatorOptions{Local: testPeerID(t), Provider: provider, Client: &countryListerFixture{values: map[peer.ID][]string{live: {"NL"}}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := aggregator.Get(context.Background(), true)
	if err != nil || result.Status != AvailableCountriesReady || !slices.Equal(result.Codes, []string{"NL"}) {
		t.Fatalf("country discovery: %+v %v", result, err)
	}
}

func TestCountryAggregatorUnionStatusesBoundsAndCache(t *testing.T) {
	local := testPeerID(t)
	peers := make([]peer.ID, 12)
	fixture := &countryListerFixture{values: make(map[peer.ID][]string), errors: make(map[peer.ID]error)}
	for index := range peers {
		peers[index] = testPeerID(t)
		fixture.values[peers[index]] = []string{"NL"}
	}
	fixture.values[peers[0]] = []string{"NL", "DE"}
	fixture.values[peers[1]] = []string{"FR", "NL"}
	fixture.errors[peers[2]] = errors.New("old protocol unsupported")
	aggregator, err := NewCountryAggregator(CountryAggregatorOptions{
		Local: local, Client: fixture, Provider: StaticServerProvider{Peers: peers},
		Parallelism: 5, CacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := aggregator.Get(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AvailableCountriesPartial || !slices.Equal(result.Codes, []string{"DE", "FR", "NL"}) {
		t.Fatalf("result=%+v", result)
	}
	if fixture.calls != MaxAvailableCountryServers {
		t.Fatalf("calls=%d, want %d", fixture.calls, MaxAvailableCountryServers)
	}
	if fixture.maxFlight.Load() > DefaultAvailableCountryParallel {
		t.Fatalf("parallelism=%d", fixture.maxFlight.Load())
	}
	if _, err := aggregator.Get(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if fixture.calls != MaxAvailableCountryServers {
		t.Fatalf("cache issued more calls: %d", fixture.calls)
	}
}

func TestCountryAggregatorEmptyAndUnavailable(t *testing.T) {
	local, remote := testPeerID(t), testPeerID(t)
	tests := []struct {
		name   string
		values []string
		err    error
		want   AvailableCountriesStatus
	}{
		{name: "empty", want: AvailableCountriesEmpty},
		{name: "unavailable", err: errors.New("timeout"), want: AvailableCountriesUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := &countryListerFixture{
				values: map[peer.ID][]string{remote: test.values},
				errors: map[peer.ID]error{remote: test.err},
			}
			aggregator, err := NewCountryAggregator(CountryAggregatorOptions{
				Local: local, Client: fixture, Provider: StaticServerProvider{Peers: []peer.ID{remote}},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := aggregator.Get(context.Background(), false)
			if err != nil || result.Status != test.want || len(result.Codes) != 0 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestCountryAggregatorIncludesLocalRendezvousShard(t *testing.T) {
	local := testPeerID(t)
	fixture := &countryListerFixture{values: map[peer.ID][]string{}, errors: map[peer.ID]error{}}
	aggregator, err := NewCountryAggregator(CountryAggregatorOptions{
		Local: local, Client: fixture, Provider: StaticServerProvider{},
		LocalSource: func() ([]string, bool) { return []string{"NL"}, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := aggregator.Get(context.Background(), false)
	if err != nil || result.Status != AvailableCountriesReady || !slices.Equal(result.Codes, []string{"NL"}) {
		t.Fatalf("local result=%+v err=%v", result, err)
	}
}

func mustObservedAddress(value string) netip.Addr {
	return netip.MustParseAddr(value)
}
