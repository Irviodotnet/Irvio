// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package underlay

import (
	"context"
	"errors"
	"net/netip"
	"runtime"
	"sync"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

func configuredNetwork() networkstate.State {
	return networkstate.State{
		Generation: 1,
		Snapshot: networkstate.Snapshot{
			Routes: []networkstate.Route{
				{Family: networkstate.FamilyIPv4, LinkIndex: 2},
				{Family: networkstate.FamilyIPv6, LinkIndex: 2},
			},
			Links: []networkstate.Link{{
				Index: 2, Name: "wan0", Up: true, MTU: 1500,
				Addresses: []netip.Prefix{
					netip.MustParsePrefix("192.0.2.2/24"),
					netip.MustParsePrefix("2001:db8::2/64"),
				},
			}},
		},
	}
}

func TestOracleUsesRealEvidenceAndTracksFamiliesIndependently(t *testing.T) {
	target4 := Target{Endpoint: netip.MustParseAddrPort("198.51.100.10:4000")}
	target6 := Target{Endpoint: netip.MustParseAddrPort("[2001:db8:1::10]:4000")}
	provider := TargetProviderFunc(func(_ context.Context, family networkstate.Family, _ int) ([]Target, error) {
		if family == networkstate.FamilyIPv4 {
			return []Target{target4}, nil
		}
		return []Target{target6}, nil
	})
	prober := ProberFunc(func(_ context.Context, target Target) error {
		if target.Endpoint.Addr().Is4() {
			return nil
		}
		return errors.New("controlled IPv6 blackhole")
	})
	config := DefaultConfig(provider, prober)
	config.FailureThreshold = 1
	oracle, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	state, err := oracle.Evaluate(context.Background(), configuredNetwork())
	if err != nil {
		t.Fatal(err)
	}
	if state.IPv4.State != Reachable || state.IPv4.Target != target4 || state.IPv6.State != Unreachable {
		t.Fatalf("unexpected independent state: %+v", state)
	}
}

func TestOracleOneDeadTargetDoesNotHideReachableUnderlay(t *testing.T) {
	dead := Target{Endpoint: netip.MustParseAddrPort("198.51.100.10:4000")}
	live := Target{Endpoint: netip.MustParseAddrPort("198.51.100.11:4000")}
	provider := TargetProviderFunc(func(_ context.Context, family networkstate.Family, _ int) ([]Target, error) {
		if family != networkstate.FamilyIPv4 {
			return nil, nil
		}
		return []Target{dead, live}, nil
	})
	prober := ProberFunc(func(_ context.Context, target Target) error {
		if target == live {
			return nil
		}
		return errors.New("dead target")
	})
	oracle, _ := New(DefaultConfig(provider, prober))
	state, err := oracle.Evaluate(context.Background(), configuredNetwork())
	if err != nil {
		t.Fatal(err)
	}
	if state.IPv4.State != Reachable || state.IPv4.Target != live {
		t.Fatalf("underlay blamed for one dead peer: %+v", state.IPv4)
	}
}

func TestOracleNoTargetsIsUnknownAndMissingRouteIsUnreachable(t *testing.T) {
	provider := TargetProviderFunc(func(context.Context, networkstate.Family, int) ([]Target, error) {
		return nil, nil
	})
	oracle, _ := New(DefaultConfig(provider, ProberFunc(func(context.Context, Target) error { return nil })))
	state, err := oracle.Evaluate(context.Background(), configuredNetwork())
	if err != nil {
		t.Fatal(err)
	}
	if state.IPv4.State != Unknown || state.IPv6.State != Unknown {
		t.Fatalf("no-target state = %+v", state)
	}
	network := configuredNetwork()
	network.Generation = 2
	network.Snapshot.Routes = nil
	state, err = oracle.Evaluate(context.Background(), network)
	if err != nil {
		t.Fatal(err)
	}
	if state.IPv4.State != Unreachable || state.IPv6.State != Unreachable {
		t.Fatalf("missing-route state = %+v", state)
	}
}

func TestOracleAllTargetsNeedBoundedFailureThreshold(t *testing.T) {
	target := Target{Endpoint: netip.MustParseAddrPort("198.51.100.10:4000")}
	provider := TargetProviderFunc(func(_ context.Context, family networkstate.Family, _ int) ([]Target, error) {
		if family == networkstate.FamilyIPv4 {
			return []Target{target}, nil
		}
		return nil, nil
	})
	config := DefaultConfig(provider, ProberFunc(func(context.Context, Target) error {
		return errors.New("blackhole")
	}))
	config.FailureThreshold = 2
	oracle, _ := New(config)
	first, _ := oracle.Evaluate(context.Background(), configuredNetwork())
	second, _ := oracle.Evaluate(context.Background(), configuredNetwork())
	if first.IPv4.State != Unknown || second.IPv4.State != Unreachable {
		t.Fatalf("failure threshold states = %v, %v", first.IPv4.State, second.IPv4.State)
	}
}

func TestOracleProbeConcurrencyIsBounded(t *testing.T) {
	targets := []Target{
		{Endpoint: netip.MustParseAddrPort("198.51.100.1:4000")},
		{Endpoint: netip.MustParseAddrPort("198.51.100.2:4000")},
		{Endpoint: netip.MustParseAddrPort("198.51.100.3:4000")},
	}
	provider := TargetProviderFunc(func(_ context.Context, family networkstate.Family, _ int) ([]Target, error) {
		if family == networkstate.FamilyIPv4 {
			return targets, nil
		}
		return nil, nil
	})
	var mu sync.Mutex
	active, maximum := 0, 0
	release := make(chan struct{})
	prober := ProberFunc(func(ctx context.Context, _ Target) error {
		mu.Lock()
		active++
		if active > maximum {
			maximum = active
		}
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
		}
		mu.Lock()
		active--
		mu.Unlock()
		return errors.New("blocked")
	})
	config := DefaultConfig(provider, prober)
	config.Parallelism = 2
	oracle, _ := New(config)
	done := make(chan struct{})
	go func() {
		_, _ = oracle.Evaluate(context.Background(), configuredNetwork())
		close(done)
	}()
	for {
		mu.Lock()
		current := active
		mu.Unlock()
		if current == 2 {
			break
		}
		runtime.Gosched()
	}
	close(release)
	<-done
	if maximum != 2 {
		t.Fatalf("maximum concurrency = %d", maximum)
	}
}

func TestOracleNewNetworkGenerationDoesNotInheritFailureThreshold(t *testing.T) {
	target := Target{Endpoint: netip.MustParseAddrPort("198.51.100.10:4000")}
	provider := TargetProviderFunc(func(_ context.Context, family networkstate.Family, _ int) ([]Target, error) {
		if family == networkstate.FamilyIPv4 {
			return []Target{target}, nil
		}
		return nil, nil
	})
	config := DefaultConfig(provider, ProberFunc(func(context.Context, Target) error {
		return errors.New("controlled blackhole")
	}))
	config.FailureThreshold = 2
	oracle, _ := New(config)
	first, _ := oracle.Evaluate(context.Background(), configuredNetwork())
	nextNetwork := configuredNetwork()
	nextNetwork.Generation = 2
	second, _ := oracle.Evaluate(context.Background(), nextNetwork)
	if first.IPv4.State != Unknown || first.IPv4.Failures != 1 || second.IPv4.State != Unknown || second.IPv4.Failures != 1 {
		t.Fatalf("network-generation failure evidence leaked: first=%+v second=%+v", first.IPv4, second.IPv4)
	}
}

func TestOracleLateOldGenerationEvaluationCannotOverwriteCurrent(t *testing.T) {
	target := Target{Endpoint: netip.MustParseAddrPort("198.51.100.10:4000")}
	provider := TargetProviderFunc(func(_ context.Context, family networkstate.Family, _ int) ([]Target, error) {
		if family == networkstate.FamilyIPv4 {
			return []Target{target}, nil
		}
		return nil, nil
	})
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	prober := ProberFunc(func(context.Context, Target) error {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 1 {
			close(started)
			<-release
			return errors.New("stale blackhole result")
		}
		return nil
	})
	config := DefaultConfig(provider, prober)
	config.FailureThreshold = 1
	oracle, _ := New(config)
	oldDone := make(chan struct{})
	go func() {
		_, _ = oracle.Evaluate(context.Background(), configuredNetwork())
		close(oldDone)
	}()
	<-started
	currentNetwork := configuredNetwork()
	currentNetwork.Generation = 2
	current, err := oracle.Evaluate(context.Background(), currentNetwork)
	if err != nil || current.IPv4.State != Reachable {
		t.Fatalf("current evaluation=%+v err=%v", current, err)
	}
	close(release)
	<-oldDone
	if stored := oracle.Current(); stored.NetworkGeneration != 2 || stored.IPv4.State != Reachable {
		t.Fatalf("late old evaluation overwrote current state: %+v", stored)
	}
}
