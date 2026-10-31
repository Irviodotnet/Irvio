// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package transportgen

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

type fakeStateSource struct {
	current networkstate.State
	changes chan networkstate.State
	errors  chan error
	mu      sync.Mutex
	closed  bool
}

func newFakeStateSource(generation networkstate.Generation) *fakeStateSource {
	return &fakeStateSource{
		current: loopbackState(generation),
		changes: make(chan networkstate.State, 8),
		errors:  make(chan error, 1),
	}
}

func (s *fakeStateSource) Current() networkstate.State        { return s.current }
func (s *fakeStateSource) Changes() <-chan networkstate.State { return s.changes }
func (s *fakeStateSource) Errors() <-chan error               { return s.errors }
func (s *fakeStateSource) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

type fakeTransport struct {
	id     networkstate.Generation
	mu     sync.Mutex
	stale  bool
	closed bool
}

func (t *fakeTransport) ID() networkstate.Generation { return t.id }
func (t *fakeTransport) MarkStale() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stale || t.closed {
		return false
	}
	t.stale = true
	return true
}
func (t *fakeTransport) Close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return nil
}
func (t *fakeTransport) lifecycle() (bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stale, t.closed
}

func TestManagerMakeBeforeBreakActivation(t *testing.T) {
	source := newFakeStateSource(1)
	var transports sync.Map
	migrationGate := make(chan struct{})
	manager := newTestManager(t, source,
		func(_ context.Context, state networkstate.State) (Transport, error) {
			transport := &fakeTransport{id: state.Generation}
			transports.Store(state.Generation, transport)
			return transport, nil
		},
		func(ctx context.Context, transport Transport) error {
			if transport.ID() != 2 {
				return nil
			}
			select {
			case <-migrationGate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})

	waitManagerEvent(t, manager.Events(), ManagerActivated, 1)
	source.changes <- loopbackState(2)
	waitManagerEvent(t, manager.Events(), ManagerPreparing, 2)
	transportOne := loadFakeTransport(t, &transports, 1)
	stale, closed := transportOne.lifecycle()
	if !stale || closed {
		t.Fatalf("old transport was not retained for handover: stale=%t closed=%t", stale, closed)
	}
	close(migrationGate)
	waitManagerEvent(t, manager.Events(), ManagerActivated, 2)
	_, closed = transportOne.lifecycle()
	if !closed || manager.Current().ID() != 2 {
		t.Fatal("new transport was not atomically promoted before old transport closed")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerDiscardsLateStaleCompletion(t *testing.T) {
	source := newFakeStateSource(1)
	var transports sync.Map
	releaseTwo := make(chan struct{})
	manager := newTestManager(t, source,
		func(_ context.Context, state networkstate.State) (Transport, error) {
			if state.Generation == 2 {
				<-releaseTwo
			}
			transport := &fakeTransport{id: state.Generation}
			transports.Store(state.Generation, transport)
			return transport, nil
		},
		func(context.Context, Transport) error { return nil })
	waitManagerEvent(t, manager.Events(), ManagerActivated, 1)
	source.changes <- loopbackState(2)
	waitManagerEvent(t, manager.Events(), ManagerPreparing, 2)
	source.changes <- loopbackState(3)
	waitManagerEvent(t, manager.Events(), ManagerActivated, 3)
	close(releaseTwo)
	waitManagerEvent(t, manager.Events(), ManagerStaleDiscarded, 2)
	stale, closed := loadFakeTransport(t, &transports, 2).lifecycle()
	if !stale || !closed || manager.Current().ID() != 3 {
		t.Fatal("late generation overwrote the current transport")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func loadFakeTransport(t *testing.T, transports *sync.Map, generation networkstate.Generation) *fakeTransport {
	t.Helper()
	value, ok := transports.Load(generation)
	if !ok {
		t.Fatalf("transport generation %d was not built", generation)
	}
	return value.(*fakeTransport)
}

func TestManagerSurfacesMonitorFailureAndClosesCurrent(t *testing.T) {
	source := newFakeStateSource(1)
	transport := &fakeTransport{id: 1}
	manager := newTestManager(t, source,
		func(context.Context, networkstate.State) (Transport, error) { return transport, nil },
		func(context.Context, Transport) error { return nil })
	waitManagerEvent(t, manager.Events(), ManagerActivated, 1)
	want := errors.New("netlink subscription failed")
	source.errors <- want
	waitManagerEvent(t, manager.Events(), ManagerMonitorFailed, 0)
	select {
	case <-manager.done:
	case <-time.After(time.Second):
		t.Fatal("manager did not terminate after monitor failure")
	}
	_, closed := transport.lifecycle()
	if !closed {
		t.Fatal("manager retained transport after monitor failure")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerCloseBeforeStart(t *testing.T) {
	source := newFakeStateSource(1)
	manager, err := NewManager(ManagerConfig{
		Source: source, Factory: FactoryFunc(func(context.Context, networkstate.State) (Transport, error) {
			return nil, errors.New("unexpected build")
		}), Migrator: MigratorFunc(func(context.Context, Transport) error { return nil }),
		EventCapacity: 4, BuildTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("start after close error = %v", err)
	}
	if _, ok := <-manager.Events(); ok {
		t.Fatal("event channel remained open")
	}
}

func newTestManager(
	t *testing.T,
	source StateSource,
	factory FactoryFunc,
	migrator MigratorFunc,
) *Manager {
	t.Helper()
	manager, err := NewManager(ManagerConfig{
		Source: source, Factory: factory, Migrator: migrator,
		EventCapacity: 32, BuildTimeout: time.Second, MaxConcurrentBuilds: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func waitManagerEvent(
	t *testing.T,
	events <-chan ManagerEvent,
	wantType ManagerEventType,
	wantGeneration networkstate.Generation,
) ManagerEvent {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("manager event channel closed")
			}
			if event.Type == wantType && (wantGeneration == 0 || event.Generation == wantGeneration) {
				return event
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for manager event %d generation %d", wantType, wantGeneration)
		}
	}
}
