// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

func TestOneActiveProfilePerFamily(t *testing.T) {
	ipv4 := newIPv4Script(scriptOptions{mapping: MappingEndpointIndependent, filtering: FilteringEndpointIndependent})
	exchanger := &gatedExchanger{delegate: ipv4, started: make(chan socketmux.AddressFamily, 64), release: make(chan struct{})}
	profiler, err := New(exchanger, fastProfilerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer profiler.Close()

	first := make(chan error, 1)
	go func() {
		_, profileErr := profiler.Profile(context.Background(), ipv4.primary)
		first <- profileErr
	}()
	if family := waitForFamily(t, exchanger.started); family != socketmux.IPv4 {
		t.Fatalf("started family = %v", family)
	}
	if _, err := profiler.Profile(context.Background(), ipv4.primary); !errors.Is(err, ErrProbeInProgress) {
		t.Fatalf("second same-family profile error = %v", err)
	}
	close(exchanger.release)
	if err := waitForError(t, first); err != nil {
		t.Fatal(err)
	}
	if profiler.Stats().ProfilesRejectedInProgress != 1 {
		t.Fatalf("unexpected stats: %+v", profiler.Stats())
	}
}

func TestIPv4AndIPv6ProfilesRunConcurrently(t *testing.T) {
	ipv4 := newIPv4Script(scriptOptions{mapping: MappingEndpointIndependent, filtering: FilteringEndpointIndependent})
	ipv6 := newIPv6Script(scriptOptions{mapping: MappingAddressDependent, filtering: FilteringAddressDependent})
	delegate := &familyExchanger{ipv4: ipv4, ipv6: ipv6}
	exchanger := &gatedExchanger{delegate: delegate, started: make(chan socketmux.AddressFamily, 64), release: make(chan struct{})}
	profiler, err := New(exchanger, fastProfilerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer profiler.Close()

	type result struct {
		profile Profile
		err     error
	}
	results := make(chan result, 2)
	go func() {
		profile, profileErr := profiler.Profile(context.Background(), ipv4.primary)
		results <- result{profile: profile, err: profileErr}
	}()
	go func() {
		profile, profileErr := profiler.Profile(context.Background(), ipv6.primary)
		results <- result{profile: profile, err: profileErr}
	}()
	families := map[socketmux.AddressFamily]bool{waitForFamily(t, exchanger.started): true, waitForFamily(t, exchanger.started): true}
	if !families[socketmux.IPv4] || !families[socketmux.IPv6] {
		t.Fatalf("concurrent families = %+v", families)
	}
	close(exchanger.release)
	seen := make(map[socketmux.AddressFamily]Profile)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		seen[result.profile.Family] = result.profile
	}
	if seen[socketmux.IPv4].Status != ProfileComplete || seen[socketmux.IPv6].Status != ProfileComplete || seen[socketmux.IPv6].Mapping != MappingAddressDependent {
		t.Fatalf("unexpected concurrent results: %+v", seen)
	}
}

func TestCancelOneFamilyWhileOtherCompletes(t *testing.T) {
	ipv4 := newIPv4Script(scriptOptions{mapping: MappingEndpointIndependent, filtering: FilteringEndpointIndependent})
	ipv6 := newIPv6Script(scriptOptions{mapping: MappingEndpointIndependent, filtering: FilteringEndpointIndependent})
	delegate := &familyExchanger{ipv4: ipv4, ipv6: ipv6}
	exchanger := &gatedExchanger{delegate: delegate, started: make(chan socketmux.AddressFamily, 64), release: make(chan struct{})}
	profiler, err := New(exchanger, fastProfilerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer profiler.Close()

	ipv4Context, cancelIPv4 := context.WithCancel(context.Background())
	ipv4Result := make(chan error, 1)
	ipv6Result := make(chan error, 1)
	go func() {
		_, profileErr := profiler.Profile(ipv4Context, ipv4.primary)
		ipv4Result <- profileErr
	}()
	go func() {
		_, profileErr := profiler.Profile(context.Background(), ipv6.primary)
		ipv6Result <- profileErr
	}()
	waitForBothFamilies(t, exchanger.started)
	cancelIPv4()
	if err := waitForError(t, ipv4Result); !errors.Is(err, context.Canceled) {
		t.Fatalf("IPv4 cancellation error = %v", err)
	}
	close(exchanger.release)
	if err := waitForError(t, ipv6Result); err != nil {
		t.Fatalf("IPv6 profile error = %v", err)
	}
}

func TestCloseCancelsProfilesAndIsIdempotent(t *testing.T) {
	ipv4 := newIPv4Script(scriptOptions{mapping: MappingEndpointIndependent, filtering: FilteringEndpointIndependent})
	exchanger := &gatedExchanger{delegate: ipv4, started: make(chan socketmux.AddressFamily, 64), release: make(chan struct{})}
	profiler, err := New(exchanger, fastProfilerConfig())
	if err != nil {
		t.Fatal(err)
	}
	profileResult := make(chan error, 1)
	go func() {
		_, profileErr := profiler.Profile(context.Background(), ipv4.primary)
		profileResult <- profileErr
	}()
	waitForFamily(t, exchanger.started)
	if err := profiler.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitForError(t, profileResult); !errors.Is(err, ErrClosed) {
		t.Fatalf("profile close error = %v", err)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := profiler.Close(); err != nil {
				t.Errorf("repeat Close: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, err := profiler.Profile(context.Background(), ipv4.primary); !errors.Is(err, ErrClosed) {
		t.Fatalf("profile after close error = %v", err)
	}
}

func TestCloseDuringFilteringLivenessCheck(t *testing.T) {
	script := newIPv4Script(scriptOptions{mapping: MappingEndpointIndependent, filtering: FilteringAddressDependent})
	exchanger := &livenessBlockingExchanger{delegate: script, started: make(chan struct{})}
	profiler, err := New(exchanger, fastProfilerConfig())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, profileErr := profiler.Profile(context.Background(), script.primary)
		result <- profileErr
	}()
	select {
	case <-exchanger.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for liveness check")
	}
	if err := profiler.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitForError(t, result); !errors.Is(err, ErrClosed) {
		t.Fatalf("close during liveness error = %v", err)
	}
}

type livenessBlockingExchanger struct {
	delegate *scriptExchanger
	started  chan struct{}
	once     sync.Once
}

func (e *livenessBlockingExchanger) Exchange(ctx context.Context, request stunprobe.BindingRequest) (stunprobe.BindingResponse, error) {
	e.delegate.mu.Lock()
	isLiveness := request.Destination == e.delegate.primary && !request.ChangeIP && !request.ChangePort && e.delegate.regularPrimary == 1
	e.delegate.mu.Unlock()
	if isLiveness {
		e.once.Do(func() { close(e.started) })
		<-ctx.Done()
		return stunprobe.BindingResponse{}, ctx.Err()
	}
	return e.delegate.Exchange(ctx, request)
}

func waitForBothFamilies(t *testing.T, started <-chan socketmux.AddressFamily) {
	t.Helper()
	seen := make(map[socketmux.AddressFamily]bool)
	for len(seen) < 2 {
		seen[waitForFamily(t, started)] = true
	}
}

func waitForFamily(t *testing.T, started <-chan socketmux.AddressFamily) socketmux.AddressFamily {
	t.Helper()
	select {
	case family := <-started:
		return family
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for profile exchange")
		return 0
	}
}

func waitForError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for profile result")
		return nil
	}
}
