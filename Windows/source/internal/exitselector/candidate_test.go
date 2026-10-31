// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/protocol"
)

func selectorPeer(t *testing.T, seed byte) peer.ID {
	t.Helper()
	key, _, err := libcrypto.GenerateEd25519Key(bytes.NewReader(bytes.Repeat([]byte{seed}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func selectorCandidate(t *testing.T, seed byte, now time.Time) Candidate {
	t.Helper()
	return Candidate{
		PeerID: selectorPeer(t, seed), Country: "DE",
		ControlEndpoint: netip.AddrPortFrom(netip.AddrFrom4([4]byte{198, 51, 100, seed}), uint16(20000)+uint16(seed)),
		ProtocolVersion: protocol.ProtocolVersion, Capabilities: protocol.CapExit,
		Accepting: true, AvailableBandwidthMbps: 100, Uptime: UptimeMature,
		NetworkEpoch: uint64(seed), GeneratedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}
}

type sequenceRandom struct {
	mu   sync.Mutex
	next uint64
}

func (r *sequenceRandom) Index(limit int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 {
		return 0, errors.New("invalid limit")
	}
	value := (r.next * 7919) % uint64(limit)
	r.next++
	return int(value), nil
}

type candidatePool []Candidate

func (p candidatePool) EligibleCandidates(context.Context, CountryIntent) ([]Candidate, error) {
	return append([]Candidate(nil), p...), nil
}

func TestSamplingProviderPowerOfKAndPeerIDDeduplication(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	local := selectorPeer(t, 250)
	pool := make(candidatePool, 0, 30)
	for seed := byte(1); seed <= 25; seed++ {
		pool = append(pool, selectorCandidate(t, seed, now))
	}
	pool = append(pool, pool[0])
	wrongCountry := selectorCandidate(t, 40, now)
	wrongCountry.Country = "NL"
	pool = append(pool, wrongCountry)
	notAccepting := selectorCandidate(t, 41, now)
	notAccepting.Accepting = false
	pool = append(pool, notAccepting)
	provider := SamplingProvider{Source: pool, LocalPeer: local, Random: &sequenceRandom{}, Now: func() time.Time { return now }}
	batch, err := provider.GetCandidates(context.Background(), CandidateRequest{Country: "DE", Limit: MaxCandidateBatch, ExcludePeerIDs: []peer.ID{pool[2].PeerID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Candidates) != 10 || batch.AvailablePeerCount != 24 {
		t.Fatalf("batch size/available = %d/%d", len(batch.Candidates), batch.AvailablePeerCount)
	}
	seen := make(map[peer.ID]struct{}, 10)
	for _, candidate := range batch.Candidates {
		if candidate.PeerID == pool[2].PeerID {
			t.Fatal("excluded candidate returned")
		}
		if _, duplicate := seen[candidate.PeerID]; duplicate {
			t.Fatal("candidate PeerID duplicated")
		}
		seen[candidate.PeerID] = struct{}{}
	}
	provider.Source = candidatePool(pool[:4])
	small, err := provider.GetCandidates(context.Background(), CandidateRequest{Country: "DE", Limit: 10})
	if err != nil || len(small.Candidates) != 4 {
		t.Fatalf("K=min(10,N) result=%d err=%v", len(small.Candidates), err)
	}
}

func TestSamplingProviderFixtureVariesUniformChoice(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	pool := make(candidatePool, 20)
	for index := range pool {
		pool[index] = selectorCandidate(t, byte(index+1), now)
	}
	provider := SamplingProvider{Source: pool, LocalPeer: selectorPeer(t, 250), Random: &sequenceRandom{}, Now: func() time.Time { return now }}
	seen := make(map[peer.ID]struct{})
	for range 100 {
		batch, err := provider.GetCandidates(context.Background(), CandidateRequest{Country: "DE", Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		seen[batch.Candidates[0].PeerID] = struct{}{}
	}
	if len(seen) != len(pool) {
		t.Fatalf("deterministic uniform fixture reached %d/%d peers", len(seen), len(pool))
	}
}

type probeFunc func(context.Context, Candidate) (ProbeResult, error)

func (f probeFunc) Probe(ctx context.Context, candidate Candidate) (ProbeResult, error) {
	return f(ctx, candidate)
}

func TestEvaluateBatchFiltersThresholdsAndUsesLatencyTolerance(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	local := selectorPeer(t, 250)
	mature := selectorCandidate(t, 1, now)
	newPeer := selectorCandidate(t, 2, now)
	newPeer.Uptime = UptimeNew
	slower := selectorCandidate(t, 3, now)
	insufficient := selectorCandidate(t, 4, now)
	insufficient.AvailableBandwidthMbps = 10
	expired := selectorCandidate(t, 5, now)
	expired.ExpiresAt = now.Add(-time.Second)
	latencies := map[peer.ID]time.Duration{mature.PeerID: 23 * time.Millisecond, newPeer.PeerID: 21 * time.Millisecond, slower.PeerID: 42 * time.Millisecond}
	selection, err := EvaluateBatch(context.Background(), CandidateBatch{
		Country: "DE", AvailablePeerCount: 6, GeneratedAt: now,
		Candidates: []Candidate{newPeer, slower, insufficient, mature, expired, mature},
	}, probeFunc(func(_ context.Context, candidate Candidate) (ProbeResult, error) {
		return ProbeResult{Reachable: true, Latency: latencies[candidate.PeerID], ObservedAt: now, NetworkEpoch: candidate.NetworkEpoch}, nil
	}), EvaluationConfig{
		LocalPeer: local, Country: "DE", Now: now, MinimumBandwidthMbps: 30,
		ProbeConcurrency: 3, ProbeTimeout: time.Second, LatencyTolerance: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Active == nil || selection.Reserve == nil || selection.Cold == nil {
		t.Fatalf("incomplete selection: %+v", selection)
	}
	if selection.Active.Candidate.PeerID != mature.PeerID || selection.Reserve.Candidate.PeerID != newPeer.PeerID || selection.Cold.Candidate.PeerID != slower.PeerID {
		t.Fatal("latency bucket/maturity ordering is unstable")
	}
}

func TestEvaluateBatchSuppressionIsBoundToNetworkEpochAndExclusionsAreDefensive(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	local := selectorPeer(t, 250)
	candidate := selectorCandidate(t, 1, now)
	evaluate := func(value Candidate, excluded map[peer.ID]struct{}) Selection {
		t.Helper()
		selection, err := EvaluateBatch(context.Background(), CandidateBatch{
			Country: "DE", AvailablePeerCount: 1, GeneratedAt: now, Candidates: []Candidate{value},
		}, probeFunc(func(_ context.Context, probed Candidate) (ProbeResult, error) {
			return ProbeResult{Reachable: true, Latency: time.Millisecond, ObservedAt: now, NetworkEpoch: probed.NetworkEpoch}, nil
		}), EvaluationConfig{
			LocalPeer: local, Country: "DE", Now: now, ProbeConcurrency: 1,
			ProbeTimeout: time.Second, LatencyTolerance: time.Millisecond,
			Suppressed: map[peer.ID]uint64{candidate.PeerID: candidate.NetworkEpoch}, Excluded: excluded,
		})
		if err != nil {
			t.Fatal(err)
		}
		return selection
	}
	if selection := evaluate(candidate, nil); selection.Active != nil {
		t.Fatal("suppressed path epoch remained eligible")
	}
	newPath := candidate
	newPath.NetworkEpoch++
	if selection := evaluate(newPath, nil); selection.Active == nil {
		t.Fatal("new NetworkEpoch did not invalidate path-specific suppression")
	}
	if selection := evaluate(newPath, map[peer.ID]struct{}{candidate.PeerID: {}}); selection.Active != nil {
		t.Fatal("defensively excluded ACTIVE was accepted from provider response")
	}
}

func TestEvaluateBatchRouteSuppressionDoesNotSuppressFreshRouteForSamePeer(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	local := selectorPeer(t, 250)
	candidate := selectorCandidate(t, 1, now)
	candidate.SignalingRouteEpoch = 11
	evaluate := func(value Candidate) Selection {
		t.Helper()
		selection, err := EvaluateBatch(context.Background(), CandidateBatch{
			Country: "DE", AvailablePeerCount: 1, GeneratedAt: now, Candidates: []Candidate{value},
		}, probeFunc(func(_ context.Context, probed Candidate) (ProbeResult, error) {
			return ProbeResult{Reachable: true, Latency: time.Millisecond, ObservedAt: now, NetworkEpoch: probed.NetworkEpoch}, nil
		}), EvaluationConfig{
			LocalPeer: local, Country: "DE", Now: now, ProbeConcurrency: 1,
			ProbeTimeout: time.Second, LatencyTolerance: time.Millisecond,
			SuppressedRoutes: map[peer.ID]map[uint64]struct{}{
				candidate.PeerID: {candidate.SignalingRouteEpoch: {}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return selection
	}
	if selection := evaluate(candidate); selection.Active != nil {
		t.Fatal("known-dead broker route set remained eligible")
	}
	fresh := candidate
	fresh.SignalingRouteEpoch++
	if selection := evaluate(fresh); selection.Active == nil || selection.Active.Candidate.PeerID != candidate.PeerID {
		t.Fatal("fresh broker route suppressed the Exit PeerID")
	}
}

func TestLatencyMetricsPercentilesAreBounded(t *testing.T) {
	metrics := newMetrics(4)
	for _, latency := range []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond, 5 * time.Millisecond} {
		metrics.probeStart()
		metrics.probeDone(latency)
		metrics.connectStart()
		metrics.connectDone(OutcomeConnected, latency)
	}
	metrics.connectStart()
	metrics.connectDone(OutcomeUnavailable, time.Hour)
	stats := metrics.snapshot()
	if stats.ProbeLatencyP50 != 3*time.Millisecond || stats.ProbeLatencyP95 != 5*time.Millisecond || stats.ConnectLatencyP50 != 3*time.Millisecond || stats.ConnectLatencyP95 != 5*time.Millisecond {
		t.Fatalf("bounded latency percentiles = %+v", stats)
	}
}

func TestConnectionOutcomeNeverTreatsErrorAsConnected(t *testing.T) {
	connection := newFakeExitConnection()
	if outcome := classifyOutcome(ConnectResult{Outcome: OutcomeConnected, Connection: connection}, errors.New("late setup failure")); outcome != OutcomeFailed {
		t.Fatalf("connected result with error classified as %d", outcome)
	}
	if outcome := classifyOutcome(ConnectResult{Outcome: OutcomeBusy}, &OutcomeError{Outcome: OutcomeBusy, Err: errors.New("capacity")}); outcome != OutcomeBusy {
		t.Fatalf("BUSY outcome classified as %d", outcome)
	}
}

func TestEvaluateBatchProbeFanoutIsBoundedAndCancellationStopsStorm(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	candidates := make([]Candidate, 10)
	for index := range candidates {
		candidates[index] = selectorCandidate(t, byte(index+1), now)
	}
	var active atomic.Int32
	var maximum atomic.Int32
	started := make(chan struct{}, 10)
	release := make(chan struct{})
	probe := probeFunc(func(ctx context.Context, candidate Candidate) (ProbeResult, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			return ProbeResult{Reachable: true, Latency: time.Millisecond, ObservedAt: now, NetworkEpoch: candidate.NetworkEpoch}, nil
		case <-ctx.Done():
			return ProbeResult{}, ctx.Err()
		}
	})
	finished := make(chan error, 1)
	go func() {
		_, err := EvaluateBatch(context.Background(), CandidateBatch{Country: "DE", AvailablePeerCount: 10, GeneratedAt: now, Candidates: candidates}, probe, EvaluationConfig{
			LocalPeer: selectorPeer(t, 250), Country: "DE", Now: now, ProbeConcurrency: 5, ProbeTimeout: time.Second, LatencyTolerance: time.Millisecond,
		})
		finished <- err
	}()
	for range 5 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("bounded probe wave did not start")
		}
	}
	if maximum.Load() != 5 {
		t.Fatalf("maximum probe concurrency = %d", maximum.Load())
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	blocked := make(chan struct{}, 10)
	finished = make(chan error, 1)
	go func() {
		_, err := EvaluateBatch(ctx, CandidateBatch{Country: "DE", AvailablePeerCount: 10, GeneratedAt: now, Candidates: candidates}, probeFunc(func(ctx context.Context, _ Candidate) (ProbeResult, error) {
			blocked <- struct{}{}
			<-ctx.Done()
			return ProbeResult{}, ctx.Err()
		}), EvaluationConfig{LocalPeer: selectorPeer(t, 249), Country: "DE", Now: now, ProbeConcurrency: 5, ProbeTimeout: time.Minute, LatencyTolerance: time.Millisecond})
		finished <- err
	}()
	for range 5 {
		<-blocked
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled probe storm error = %v", err)
	}
}

func TestJitteredRefreshIsMeaningfullyDistributed(t *testing.T) {
	random := &sequenceRandom{}
	minimum, maximum := 75*time.Second, time.Duration(0)
	for range 1000 {
		delay, err := JitteredRefresh(45*time.Second, 75*time.Second, random)
		if err != nil {
			t.Fatal(err)
		}
		if delay < minimum {
			minimum = delay
		}
		if delay > maximum {
			maximum = delay
		}
	}
	if minimum > 50*time.Second || maximum < 70*time.Second {
		t.Fatalf("refresh distribution range = %s..%s", minimum, maximum)
	}
}

func TestUnderlayFromNetworkStateRequiresUsableDefaultRouteLinkAndAddress(t *testing.T) {
	state := networkstate.State{Generation: 7, Snapshot: networkstate.Snapshot{
		Routes: []networkstate.Route{
			{Family: networkstate.FamilyIPv4, Table: 254, LinkIndex: 2},
			{Family: networkstate.FamilyIPv6, Table: 254, LinkIndex: 3},
		},
		Links: []networkstate.Link{
			{Index: 2, Name: "wan4", Up: true, MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}},
			{Index: 3, Name: "wan6", Up: false, MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8::10/64")}},
		},
	}}
	underlay := UnderlayFromNetworkState(state)
	if underlay.NetworkGeneration != 7 || !underlay.IPv4Usable || underlay.IPv6Usable {
		t.Fatalf("underlay = %+v", underlay)
	}
	state.Snapshot.Links[1].Up = true
	if underlay = UnderlayFromNetworkState(state); !underlay.IPv6Usable {
		t.Fatalf("IPv6 underlay not recognized: %+v", underlay)
	}
}
