// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/underlay"
)

type fakeNetwork struct {
	current networkstate.State
	changes chan networkstate.State
	errors  chan error
}

func (f *fakeNetwork) Current() networkstate.State        { return f.current }
func (f *fakeNetwork) Changes() <-chan networkstate.State { return f.changes }
func (f *fakeNetwork) Errors() <-chan error               { return f.errors }
func (*fakeNetwork) Close() error                         { return nil }

type fakeTransportLifecycle struct {
	mu          sync.Mutex
	activations []networkstate.Generation
	notified    chan networkstate.Generation
}

func (f *fakeTransportLifecycle) Activate(_ context.Context, state networkstate.State) error {
	f.mu.Lock()
	f.activations = append(f.activations, state.Generation)
	f.mu.Unlock()
	if f.notified != nil {
		select {
		case f.notified <- state.Generation:
		default:
		}
	}
	return nil
}

func (*fakeTransportLifecycle) Current() *transportgen.Generation    { return nil }
func (*fakeTransportLifecycle) Commit(networkstate.Generation) error { return nil }
func (*fakeTransportLifecycle) Close() error                         { return nil }

func (f *fakeTransportLifecycle) snapshot() []networkstate.Generation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]networkstate.Generation(nil), f.activations...)
}

type fakeOracle struct {
	mu        sync.Mutex
	state     underlay.State
	block     chan struct{}
	evaluated chan networkstate.Generation
}

func (f *fakeOracle) Evaluate(ctx context.Context, state networkstate.State) (underlay.State, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return underlay.State{}, ctx.Err()
		}
	}
	f.mu.Lock()
	f.state.NetworkGeneration = state.Generation
	result := f.state
	f.mu.Unlock()
	if f.evaluated != nil {
		select {
		case f.evaluated <- state.Generation:
		default:
		}
	}
	return result, nil
}
func (f *fakeOracle) Current() underlay.State { f.mu.Lock(); defer f.mu.Unlock(); return f.state }
func (*fakeOracle) Close() error              { return nil }

type fakeProtection struct {
	mu                       sync.Mutex
	state                    killswitch.State
	generation               uint64
	protectCalls             int
	protectBlock             chan struct{}
	disconnected             chan struct{}
	rejectCanceledDisconnect bool
}

func (f *fakeProtection) Protect(context.Context) (uint64, error) {
	if f.protectBlock != nil {
		<-f.protectBlock
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.protectCalls++
	f.generation++
	f.state = killswitch.StateProtectedConnecting
	return f.generation, nil
}

func TestRuntimeConnectAdoptsPreprotectedProductStartup(t *testing.T) {
	network := &fakeNetwork{current: networkstate.State{Generation: 1}, changes: make(chan networkstate.State), errors: make(chan error)}
	oracle := &fakeOracle{state: underlay.State{NetworkGeneration: 1, IPv4: underlay.FamilyEvidence{State: underlay.Reachable}}}
	protection := &fakeProtection{state: killswitch.StateProtectedConnecting, generation: 7}
	selector := &fakeSelector{events: make(chan exitselector.Event), closed: make(chan struct{})}
	runtime, err := New(Config{Network: network, Underlay: oracle, Protection: protection,
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) { return selector, nil },
		Now:         time.Now, OperationTimeout: time.Second, UnderlayInterval: time.Minute, EventCapacity: 16})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	if err := runtime.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateDiscovering)
	protection.mu.Lock()
	calls, generation := protection.protectCalls, protection.generation
	protection.mu.Unlock()
	if calls != 0 || generation != 7 {
		t.Fatalf("preprotected startup was re-protected: calls=%d generation=%d", calls, generation)
	}
}
func (f *fakeProtection) Transition(g uint64, state killswitch.State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g != f.generation {
		return killswitch.ErrStaleGeneration
	}
	f.state = state
	return nil
}
func (f *fakeProtection) Disconnect(ctx context.Context) error {
	if f.rejectCanceledDisconnect {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation++
	f.state = killswitch.StateVPNOff
	if f.disconnected != nil {
		select {
		case f.disconnected <- struct{}{}:
		default:
		}
	}
	return nil
}
func (f *fakeProtection) Snapshot() killswitch.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return killswitch.Snapshot{State: f.state, Generation: f.generation}
}
func (*fakeProtection) Close(context.Context) error { return nil }

type fakeSelector struct {
	current      exitselector.Snapshot
	events       chan exitselector.Event
	closed       chan struct{}
	closeErr     error
	closeStarted chan struct{}
	closeBlock   chan struct{}
}

func (f *fakeSelector) Start(context.Context) error { return nil }
func (f *fakeSelector) UpdateUnderlay(state exitselector.UnderlayState) error {
	f.current.Underlay = state
	return nil
}
func (f *fakeSelector) Current() exitselector.Snapshot    { return f.current }
func (f *fakeSelector) Events() <-chan exitselector.Event { return f.events }
func (f *fakeSelector) Close() error {
	if f.closeStarted != nil {
		select {
		case f.closeStarted <- struct{}{}:
		default:
		}
	}
	if f.closeBlock != nil {
		<-f.closeBlock
	}
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
	return f.closeErr
}

func TestRuntimeDisconnectFinishesAfterCallerExpiresDuringSelectorClose(t *testing.T) {
	network := &fakeNetwork{
		current: networkstate.State{Generation: 1},
		changes: make(chan networkstate.State), errors: make(chan error),
	}
	oracle := &fakeOracle{state: underlay.State{
		NetworkGeneration: 1, IPv4: underlay.FamilyEvidence{State: underlay.Reachable},
	}}
	protection := &fakeProtection{rejectCanceledDisconnect: true}
	closeStarted := make(chan struct{}, 1)
	closeBlock := make(chan struct{})
	selector := &fakeSelector{
		events: make(chan exitselector.Event), closed: make(chan struct{}),
		closeStarted: closeStarted, closeBlock: closeBlock,
	}
	runtime, err := New(Config{
		Network: network, Underlay: oracle, Protection: protection,
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) { return selector, nil },
		Now:         time.Now, OperationTimeout: time.Second, UnderlayInterval: time.Minute, EventCapacity: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateDiscovering)
	request, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- runtime.Disconnect(request) }()
	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("selector close did not start")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("caller error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller deadline did not bound Disconnect")
	}
	close(closeBlock)
	waitRuntimeState(t, runtime, StateIdle)
	if protection.Snapshot().State != killswitch.StateVPNOff {
		t.Fatalf("single disconnect left protection=%v", protection.Snapshot().State)
	}
	closeContext, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := runtime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSerializesProtectionSelectionAndDisconnect(t *testing.T) {
	network := &fakeNetwork{
		current: networkstate.State{Generation: 1},
		changes: make(chan networkstate.State, 2), errors: make(chan error, 1),
	}
	oracle := &fakeOracle{state: underlay.State{
		NetworkGeneration: 1, IPv4: underlay.FamilyEvidence{State: underlay.Reachable},
	}}
	protection := new(fakeProtection)
	candidate := &exitselector.CandidateEvaluation{Candidate: exitselector.Candidate{PeerID: peer.ID("exit")}}
	selector := &fakeSelector{
		current: exitselector.Snapshot{LifecycleGeneration: 3, Active: candidate},
		events:  make(chan exitselector.Event, 4), closed: make(chan struct{}),
	}
	runtime, err := New(Config{
		Core: io.NopCloser(nilReader{}), Network: network, Underlay: oracle, Protection: protection,
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) { return selector, nil },
		Now:         time.Now, OperationTimeout: time.Second, EventCapacity: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateDiscovering)
	selector.events <- exitselector.Event{Type: exitselector.EventActivePromoted}
	waitRuntimeState(t, runtime, StateConnected)
	status := runtime.Status()
	if status.ActivePeer != candidate.Candidate.PeerID || status.ProtectedMode != killswitch.StateConnected {
		t.Fatalf("invalid connected status: %+v", status)
	}
	if err := runtime.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateIdle)
	if protection.Snapshot().State != killswitch.StateVPNOff {
		t.Fatal("runtime did not remove protection last")
	}
	closeContext, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := runtime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeLocalPromotionFailureIsErrorAndRemainsFailClosed(t *testing.T) {
	network := &fakeNetwork{
		current: networkstate.State{Generation: 1},
		changes: make(chan networkstate.State), errors: make(chan error),
	}
	oracle := &fakeOracle{state: underlay.State{
		NetworkGeneration: 1, IPv4: underlay.FamilyEvidence{State: underlay.Reachable},
	}}
	protection := new(fakeProtection)
	selector := &fakeSelector{events: make(chan exitselector.Event, 4), closed: make(chan struct{})}
	runtime, err := New(Config{
		Network: network, Underlay: oracle, Protection: protection,
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) { return selector, nil },
		Now:         time.Now, OperationTimeout: time.Second, UnderlayInterval: time.Minute, EventCapacity: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateDiscovering)
	failure := errors.New("controlled local promotion failure")
	selector.events <- exitselector.Event{Type: exitselector.EventLocalPromotionFailed, Err: failure}
	waitRuntimeState(t, runtime, StateError)
	if status := runtime.Status(); status.State != StateError || status.LastReason != ReasonChildFailure {
		t.Fatalf("local promotion runtime status = %+v", status)
	}
	if state := protection.Snapshot().State; state != killswitch.StateNoExit || !state.Protected() {
		t.Fatalf("local promotion failure exposed WAN: %v", state)
	}
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeActiveFailureImmediatelyReevaluatesMarkedUnderlay(t *testing.T) {
	network := &fakeNetwork{
		current: networkstate.State{Generation: 1},
		changes: make(chan networkstate.State), errors: make(chan error),
	}
	evaluated := make(chan networkstate.Generation, 4)
	oracle := &fakeOracle{state: underlay.State{
		NetworkGeneration: 1, IPv4: underlay.FamilyEvidence{State: underlay.Reachable},
	}, evaluated: evaluated}
	protection := new(fakeProtection)
	candidate := &exitselector.CandidateEvaluation{Candidate: exitselector.Candidate{PeerID: peer.ID("exit")}}
	selector := &fakeSelector{
		current: exitselector.Snapshot{LifecycleGeneration: 3, Active: candidate},
		events:  make(chan exitselector.Event, 4), closed: make(chan struct{}),
	}
	runtime, err := New(Config{
		Network: network, Underlay: oracle, Protection: protection,
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) { return selector, nil },
		Now:         time.Now, OperationTimeout: time.Second, UnderlayInterval: time.Minute, EventCapacity: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-evaluated:
	case <-time.After(time.Second):
		t.Fatal("initial underlay evaluation did not run")
	}
	if err := runtime.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateDiscovering)
	selector.events <- exitselector.Event{Type: exitselector.EventActivePromoted}
	waitRuntimeState(t, runtime, StateConnected)
	selector.events <- exitselector.Event{Type: exitselector.EventActiveDisconnected}
	select {
	case generation := <-evaluated:
		if generation != 1 {
			t.Fatalf("reevaluated NetworkGeneration=%d", generation)
		}
	case <-time.After(time.Second):
		t.Fatal("ACTIVE failure waited for periodic underlay tick")
	}
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeFencesLateUnderlayResultAfterDisconnect(t *testing.T) {
	network := &fakeNetwork{
		current: networkstate.State{Generation: 1},
		changes: make(chan networkstate.State, 2), errors: make(chan error, 1),
	}
	block := make(chan struct{})
	oracle := &fakeOracle{
		state: underlay.State{IPv4: underlay.FamilyEvidence{State: underlay.Reachable}},
		block: block,
	}
	protection := new(fakeProtection)
	selector := &fakeSelector{events: make(chan exitselector.Event), closed: make(chan struct{})}
	runtime, err := New(Config{
		Network: network, Underlay: oracle, Protection: protection,
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) { return selector, nil },
		Now:         time.Now, OperationTimeout: time.Second, EventCapacity: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateDiscovering)
	network.changes <- networkstate.State{Generation: 2}
	waitRuntimeState(t, runtime, StateNetworkMigration)
	if err := runtime.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(block)
	waitRuntimeState(t, runtime, StateIdle)
	if status := runtime.Status(); status.State != StateIdle || status.ActivePeer != "" {
		t.Fatalf("late result resurrected runtime: %+v", status)
	}
	cancel()
	closeContext, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := runtime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDoesNotBuildTransportAfterNetworkReconcileFailure(t *testing.T) {
	network := &fakeNetwork{
		current: networkstate.State{Generation: 1},
		changes: make(chan networkstate.State, 2), errors: make(chan error, 1),
	}
	oracle := &fakeOracle{state: underlay.State{
		NetworkGeneration: 1, IPv4: underlay.FamilyEvidence{State: underlay.Reachable},
	}}
	transport := &fakeTransportLifecycle{notified: make(chan networkstate.Generation, 2)}
	reconcileErr := errors.New("controlled generation route reconciliation failure")
	reconciled := make(chan networkstate.Generation, 1)
	runtime, err := New(Config{
		Network: network, Underlay: oracle, Protection: new(fakeProtection), Transport: transport,
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) {
			return &fakeSelector{events: make(chan exitselector.Event), closed: make(chan struct{})}, nil
		},
		OnNetworkChange: func(_ context.Context, state networkstate.State) error {
			reconciled <- state.Generation
			return reconcileErr
		},
		Now: time.Now, OperationTimeout: time.Second, UnderlayInterval: time.Minute, EventCapacity: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case generation := <-transport.notified:
		if generation != 1 {
			t.Fatalf("initial transport generation=%d", generation)
		}
	case <-time.After(time.Second):
		t.Fatal("initial transport build did not start")
	}
	network.changes <- networkstate.State{Generation: 2}
	select {
	case generation := <-reconciled:
		if generation != 2 {
			t.Fatalf("reconciled generation=%d", generation)
		}
	case <-time.After(time.Second):
		t.Fatal("network reconciliation did not run")
	}
	waitRuntimeState(t, runtime, StateNoExit)
	select {
	case generation := <-transport.notified:
		t.Fatalf("unsafe transport generation %d built after reconciliation failure", generation)
	case <-time.After(50 * time.Millisecond):
	}
	if got := transport.snapshot(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("transport activations=%v", got)
	}
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCleansLateProtectionCompletion(t *testing.T) {
	network := &fakeNetwork{
		current: networkstate.State{Generation: 1},
		changes: make(chan networkstate.State), errors: make(chan error),
	}
	oracle := &fakeOracle{state: underlay.State{IPv4: underlay.FamilyEvidence{State: underlay.Reachable}}}
	block := make(chan struct{})
	protection := &fakeProtection{protectBlock: block, disconnected: make(chan struct{}, 2)}
	runtime, err := New(Config{
		Network: network, Underlay: oracle, Protection: protection,
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) {
			return &fakeSelector{events: make(chan exitselector.Event), closed: make(chan struct{})}, nil
		},
		Now: time.Now, OperationTimeout: time.Second, UnderlayInterval: time.Minute, EventCapacity: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateProtecting)
	if err := runtime.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(block)
	select {
	case <-protection.disconnected:
	case <-time.After(time.Second):
		t.Fatal("initial disconnect did not complete")
	}
	select {
	case <-protection.disconnected:
	case <-time.After(time.Second):
		t.Fatal("stale successful protection was not removed")
	}
	if state := protection.Snapshot().State; state != killswitch.StateVPNOff {
		t.Fatalf("stale protection remains active: %v", state)
	}
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeReevaluatesUnderlayAndBoundsUnknown(t *testing.T) {
	network := &fakeNetwork{
		current: networkstate.State{Generation: 1, Snapshot: networkstate.Snapshot{
			Routes: []networkstate.Route{{Family: networkstate.FamilyIPv4, LinkIndex: 2}},
			Links: []networkstate.Link{{Index: 2, Name: "wan0", Up: true, MTU: 1500,
				Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}}},
		}},
		changes: make(chan networkstate.State), errors: make(chan error),
	}
	evaluated := make(chan networkstate.Generation, 4)
	oracle := &fakeOracle{state: underlay.State{IPv4: underlay.FamilyEvidence{State: underlay.Unknown}}, evaluated: evaluated}
	protection := new(fakeProtection)
	runtime, err := New(Config{
		Network: network, Underlay: oracle, Protection: protection,
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) {
			return &fakeSelector{events: make(chan exitselector.Event), closed: make(chan struct{})}, nil
		},
		Now: time.Now, OperationTimeout: time.Second, UnderlayInterval: 10 * time.Millisecond, EventCapacity: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-evaluated:
		case <-time.After(time.Second):
			t.Fatal("underlay was not periodically reevaluated")
		}
	}
	provisional := selectorUnderlay(oracle.Current(), network.current)
	if !provisional.IPv4Usable {
		t.Fatal("UNKNOWN underlay caused unbounded selector waiting despite configured WAN")
	}
	evidence := oracle.Current()
	evidence.IPv4.State = underlay.Unreachable
	if selectorUnderlay(evidence, network.current).IPv4Usable {
		t.Fatal("UNREACHABLE underlay was exposed as usable")
	}
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func waitRuntimeState(t *testing.T, runtime *Runtime, wanted State) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		if runtime.Status().State == wanted {
			return
		}
		select {
		case <-runtime.Events():
		case <-deadline.C:
			t.Fatalf("runtime state=%d want=%d", runtime.Status().State, wanted)
		}
	}
}

type nilReader struct{}

func (nilReader) Read([]byte) (int, error) { return 0, io.EOF }

func TestRuntimeDisconnectCleanupFailureRemainsProtected(t *testing.T) {
	network := &fakeNetwork{
		current: networkstate.State{Generation: 1},
		changes: make(chan networkstate.State), errors: make(chan error),
	}
	oracle := &fakeOracle{state: underlay.State{
		NetworkGeneration: 1, IPv4: underlay.FamilyEvidence{State: underlay.Reachable},
	}}
	protection := new(fakeProtection)
	cleanupErr := errors.New("controlled Data Plane teardown failure")
	selector := &fakeSelector{events: make(chan exitselector.Event, 1), closed: make(chan struct{}), closeErr: cleanupErr}
	runtime, err := New(Config{
		Network: network, Underlay: oracle, Protection: protection,
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) { return selector, nil },
		Now:         time.Now, OperationTimeout: time.Second, UnderlayInterval: time.Minute, EventCapacity: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateDiscovering)
	if err := runtime.Disconnect(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatalf("disconnect error=%v", err)
	}
	if !protection.Snapshot().State.Protected() || runtime.Status().State != StateNoExit {
		t.Fatalf("cleanup failure exposed WAN: protection=%v runtime=%v", protection.Snapshot().State, runtime.Status().State)
	}
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Close(closeContext); !errors.Is(err, cleanupErr) {
		t.Fatalf("shutdown error=%v", err)
	}
	if !protection.Snapshot().State.Protected() {
		t.Fatal("shutdown cleanup failure removed protection")
	}
}
