// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package networkstate

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"
)

type fakeScanner struct {
	mu       sync.Mutex
	snapshot Snapshot
	err      error
	scans    chan struct{}
}

func TestMonitorMetricOnlyChangePreservesGeneration(t *testing.T) {
	initial := monitorSnapshot(t, "192.0.2.2", 1500, true)
	scanner := &fakeScanner{snapshot: initial}
	m := &Monitor{ctx: context.Background(), scan: scanner, current: State{Generation: 1, Snapshot: initial}, changes: make(chan State, 1)}
	for _, metric := range []int{20101, 101, 20101} {
		next := initial.Clone()
		next.Routes[0].Priority = metric
		scanner.set(next)
		if err := m.rescan(); err != nil {
			t.Fatal(err)
		}
		if got := m.Current(); got.Generation != 1 || !got.Snapshot.Equal(next) {
			t.Fatalf("metric-only update must retain generation and refresh snapshot: %+v", got)
		}
		select {
		case event := <-m.changes:
			t.Fatalf("unexpected migration: %+v", event)
		default:
		}
	}
	next := initial.Clone()
	next.Routes[0].Gateway = netip.MustParseAddr("192.0.2.254")
	scanner.set(next)
	if err := m.rescan(); err != nil {
		t.Fatal(err)
	}
	if m.Current().Generation != 2 {
		t.Fatal("gateway change did not migrate")
	}
}

func TestTransportEquivalentRetainsRouteOrderingAndTies(t *testing.T) {
	a := monitorSnapshot(t, "192.0.2.2", 1500, true)
	a.Routes[0].Priority = 100
	b := a.Routes[0]
	b.Gateway = netip.MustParseAddr("192.0.2.254")
	b.Priority = 600
	a.Routes = append(a.Routes, b)
	for _, tc := range []struct {
		name          string
		first, second int
		want          bool
	}{
		{"same preference", 101, 601, true},
		{"different winner", 20100, 600, false},
		{"new tie", 600, 600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := a.Clone()
			next.Routes[0].Priority, next.Routes[1].Priority = tc.first, tc.second
			next, err := Normalize(next.Routes, next.Links)
			if err != nil {
				t.Fatal(err)
			}
			if got := a.transportEquivalent(next); got != tc.want {
				t.Fatalf("equivalent=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestMonitorNativeIdentityChangeAdvancesGeneration(t *testing.T) {
	initial := monitorSnapshot(t, "192.0.2.2", 1500, true)
	initial.Links[0].NativeID = 400
	scan := &fakeScanner{snapshot: initial}
	m := &Monitor{ctx: context.Background(), scan: scan, current: State{Generation: 1, Snapshot: initial}, changes: make(chan State, 1)}
	next := initial.Clone()
	next.Links[0].NativeID = 401
	scan.set(next)
	if err := m.rescan(); err != nil {
		t.Fatal(err)
	}
	if m.Current().Generation != 2 {
		t.Fatal("reused interface index did not advance generation")
	}
	if _, err := Normalize(initial.Routes, []Link{initial.Links[0], next.Links[0]}); err == nil {
		t.Fatal("conflicting native identities accepted")
	}
}

func (s *fakeScanner) Scan(context.Context) (Snapshot, error) {
	s.mu.Lock()
	snapshot, err := s.snapshot.Clone(), s.err
	s.mu.Unlock()
	select {
	case s.scans <- struct{}{}:
	default:
	}
	return snapshot, err
}

func (s *fakeScanner) set(snapshot Snapshot) {
	s.mu.Lock()
	s.snapshot = snapshot.Clone()
	s.mu.Unlock()
}

type fakeSubscription struct {
	events chan struct{}
	errors chan error
}

type failingCloseSubscription struct {
	*fakeSubscription
	err error
}

func (s failingCloseSubscription) Close() error { return s.err }

func TestMonitorInitialScanPreservesCleanupFailure(t *testing.T) {
	scanErr, closeErr := errors.New("scan failed"), errors.New("cancel failed")
	sub := failingCloseSubscription{fakeSubscription: &fakeSubscription{}, err: closeErr}
	_, err := newMonitor(Config{}, &fakeScanner{err: scanErr}, func() (subscription, error) { return sub, nil })
	if !errors.Is(err, scanErr) || !errors.Is(err, closeErr) {
		t.Fatalf("startup lost scan or cleanup failure: %v", err)
	}
}

func (s *fakeSubscription) Events() <-chan struct{} { return s.events }
func (s *fakeSubscription) Errors() <-chan error    { return s.errors }
func (s *fakeSubscription) Close() error            { return nil }
func (s *fakeSubscription) notify()                 { s.events <- struct{}{} }

func TestMonitorSemanticGenerations(t *testing.T) {
	initial := monitorSnapshot(t, "192.0.2.2", 1500, true)
	scanner := &fakeScanner{snapshot: initial, scans: make(chan struct{}, 32)}
	sub := &fakeSubscription{events: make(chan struct{}, 32), errors: make(chan error, 1)}
	monitor, err := newMonitor(Config{Debounce: time.Millisecond}, scanner, func() (subscription, error) { return sub, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = monitor.Close() })
	<-scanner.scans

	if got := monitor.Current().Generation; got != 1 {
		t.Fatalf("initial generation = %d", got)
	}
	sub.notify()
	waitForScan(t, scanner.scans)
	select {
	case changed := <-monitor.Changes():
		t.Fatalf("no-op event changed generation: %+v", changed)
	default:
	}

	changed := monitorSnapshot(t, "192.0.2.3", 1500, true)
	scanner.set(changed)
	for range 8 {
		sub.notify()
	}
	state := waitForState(t, monitor.Changes())
	if state.Generation != 2 || !state.Snapshot.Equal(changed) {
		t.Fatalf("unexpected semantic state: %+v", state)
	}

	newest := monitorSnapshot(t, "192.0.2.3", 1400, false)
	scanner.set(newest)
	sub.notify()
	waitForScan(t, scanner.scans)
	last := waitForState(t, monitor.Changes())
	if last.Generation != 3 || !last.Snapshot.Equal(newest) {
		t.Fatalf("slow consumer did not receive latest state: %+v", last)
	}
}

func TestMonitorIgnoresUnrelatedInterface(t *testing.T) {
	initial := monitorSnapshot(t, "192.0.2.2", 1500, true)
	scanner := &fakeScanner{snapshot: initial, scans: make(chan struct{}, 8)}
	sub := &fakeSubscription{events: make(chan struct{}, 8), errors: make(chan error, 1)}
	monitor, err := newMonitor(Config{Debounce: time.Millisecond}, scanner, func() (subscription, error) { return sub, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = monitor.Close() })
	<-scanner.scans

	withUnrelated, err := Normalize(initial.Routes, append(initial.Links, Link{
		Index: 99, Name: "docker-like", Up: false, MTU: 9000,
		Addresses: []netip.Prefix{netip.MustParsePrefix("198.51.100.2/24")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	scanner.set(withUnrelated)
	sub.notify()
	waitForScan(t, scanner.scans)
	if got := monitor.Current().Generation; got != 1 {
		t.Fatalf("irrelevant interface changed generation to %d", got)
	}
}

func TestMonitorSurfacesSubscriptionFailure(t *testing.T) {
	scanner := &fakeScanner{snapshot: monitorSnapshot(t, "192.0.2.2", 1500, true), scans: make(chan struct{}, 4)}
	sub := &fakeSubscription{events: make(chan struct{}, 1), errors: make(chan error, 1)}
	monitor, err := newMonitor(Config{Debounce: time.Millisecond}, scanner, func() (subscription, error) { return sub, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = monitor.Close() })
	want := errors.New("subscription failed")
	sub.errors <- want
	select {
	case got := <-monitor.Errors():
		if !errors.Is(got, want) {
			t.Fatalf("unexpected monitor error: %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("monitor did not surface subscription failure")
	}
}

func TestMonitorContinuesAfterOptionalErrorChannelCloses(t *testing.T) {
	initial := monitorSnapshot(t, "192.0.2.2", 1500, true)
	scanner := &fakeScanner{snapshot: initial, scans: make(chan struct{}, 4)}
	sub := &fakeSubscription{events: make(chan struct{}, 1), errors: make(chan error)}
	monitor, err := newMonitor(Config{Debounce: time.Millisecond}, scanner, func() (subscription, error) { return sub, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = monitor.Close() })
	<-scanner.scans
	close(sub.errors)
	changed := monitorSnapshot(t, "192.0.2.3", 1500, true)
	scanner.set(changed)
	sub.notify()
	state := waitForState(t, monitor.Changes())
	if state.Generation != 2 || !state.Snapshot.Equal(changed) {
		t.Fatalf("monitor stopped after optional error channel closed: %+v", state)
	}
}

func TestMonitorCloseIsIdempotent(t *testing.T) {
	scanner := &fakeScanner{snapshot: monitorSnapshot(t, "192.0.2.2", 1500, true), scans: make(chan struct{}, 4)}
	sub := &fakeSubscription{events: make(chan struct{}, 1), errors: make(chan error, 1)}
	monitor, err := newMonitor(Config{}, scanner, func() (subscription, error) { return sub, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Close(); err != nil {
		t.Fatal(err)
	}
}

func monitorSnapshot(t *testing.T, address string, mtu int, up bool) Snapshot {
	t.Helper()
	return mustSnapshot(t,
		[]Route{{Family: FamilyIPv4, LinkIndex: 4, Gateway: netip.MustParseAddr("192.0.2.1")}},
		[]Link{{Index: 4, Name: "eth0", Up: up, MTU: mtu, Addresses: []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr(address), 24)}}},
	)
}

func waitForScan(t *testing.T, scans <-chan struct{}) {
	t.Helper()
	select {
	case <-scans:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for authoritative rescan")
	}
}

func waitForState(t *testing.T, states <-chan State) State {
	t.Helper()
	select {
	case state := <-states:
		return state
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for network generation")
		return State{}
	}
}
