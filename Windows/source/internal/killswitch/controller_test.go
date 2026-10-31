// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
)

type fakeBackend struct {
	mu        sync.Mutex
	install   *fakeInstallation
	result    EnsureResult
	ensureErr error
}

func (b *fakeBackend) Ensure(context.Context, Policy) (Installation, EnsureResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ensureErr != nil {
		return nil, 0, b.ensureErr
	}
	return b.install, b.result, nil
}

func (b *fakeBackend) Inspect(context.Context, Policy) (KernelSnapshot, error) {
	return b.install.Snapshot(context.Background())
}

type fakeInstallation struct {
	mu           sync.Mutex
	snapshot     KernelSnapshot
	snapshotErr  error
	reconcileErr error
	removeErr    error
	removed      int
	reconciled   int
}

type fakeResource struct {
	mu       sync.Mutex
	closeErr error
	closed   int
}

func (r *fakeResource) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed++
	return r.closeErr
}

func (i *fakeInstallation) Snapshot(context.Context) (KernelSnapshot, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.snapshot, i.snapshotErr
}

func (i *fakeInstallation) Reconcile(context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.reconciled++
	return i.reconcileErr
}

func (i *fakeInstallation) Remove(context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.removed++
	return i.removeErr
}

func validFake() (*fakeBackend, *fakeInstallation) {
	installation := &fakeInstallation{snapshot: KernelSnapshot{TableName: DefaultTableName, Version: PolicyVersion, Installed: true, Complete: true}}
	return &fakeBackend{install: installation, result: EnsureInstalled}, installation
}

func testConfig(backend Backend) Config {
	return Config{Backend: backend, Policy: Policy{TUNName: "hydra0", BypassMark: 0x48594452}}
}

func TestControllerProtectsBeforeTunnelStatesAndRemovesLast(t *testing.T) {
	backend, installation := validFake()
	controller, err := New(testConfig(backend))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := controller.Protect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := controller.Snapshot(); snapshot.State != StateProtectedConnecting || !snapshot.State.Protected() || snapshot.Generation != generation {
		t.Fatalf("protected snapshot=%+v", snapshot)
	}
	resource := &fakeResource{}
	if err := controller.Activate(context.Background(), generation, func(context.Context) (io.Closer, error) { return resource, nil }); err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{StateConnected, StateSamePeerRecovery, StateNetworkMigration, StateNoExit} {
		if err := controller.Transition(generation, state); err != nil {
			t.Fatalf("transition %v: %v", state, err)
		}
	}
	if err := controller.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if controller.Snapshot().State != StateVPNOff || installation.removed != 1 || resource.closed != 1 {
		t.Fatalf("snapshot=%+v removed=%d resource closed=%d", controller.Snapshot(), installation.removed, resource.closed)
	}
}

func TestControllerFailuresRemainFailClosedAfterCommit(t *testing.T) {
	backend, installation := validFake()
	controller, err := New(testConfig(backend))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := controller.Protect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	teardownErr := errors.New("injected tunnel teardown failure")
	resource := &fakeResource{closeErr: teardownErr}
	if err := controller.Activate(context.Background(), generation, func(context.Context) (io.Closer, error) { return resource, nil }); err != nil {
		t.Fatal(err)
	}
	if err := controller.Disconnect(context.Background()); !errors.Is(err, teardownErr) {
		t.Fatalf("disconnect error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.State != StateNoExit || !snapshot.State.Protected() {
		t.Fatalf("unsafe state=%+v", snapshot)
	}
	if installation.removed != 0 {
		t.Fatal("protection removed after tunnel teardown failure")
	}
	if err := controller.Transition(generation, StateConnected); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale transition error=%v", err)
	}
}

func TestControllerRemovalFailureNeverReportsVPNOff(t *testing.T) {
	backend, installation := validFake()
	installation.removeErr = errors.New("injected nftables removal failure")
	controller, err := New(testConfig(backend))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Protect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Disconnect(context.Background()); !errors.Is(err, installation.removeErr) {
		t.Fatalf("disconnect error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.State != StateUnprotecting || !snapshot.State.Protected() {
		t.Fatalf("unsafe state=%+v", snapshot)
	}
}

func TestControllerClosesStaleActivation(t *testing.T) {
	backend, _ := validFake()
	controller, err := New(testConfig(backend))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := controller.Protect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeResource{}
	if err := controller.Activate(context.Background(), generation+1, func(context.Context) (io.Closer, error) { return resource, nil }); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("activation error=%v", err)
	}
	if resource.closed != 0 {
		t.Fatal("builder must not run for an already-stale generation")
	}
}

func TestControllerAdoptsPersistentProtection(t *testing.T) {
	backend, installation := validFake()
	backend.result = EnsureAdopted
	controller, err := New(testConfig(backend))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := controller.Protect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := controller.Snapshot(); !snapshot.Adopted || snapshot.Generation != generation {
		t.Fatalf("adoption snapshot=%+v", snapshot)
	}
	if err := controller.Reconcile(context.Background(), generation); err != nil {
		t.Fatal(err)
	}
	if installation.reconciled != 1 {
		t.Fatalf("reconcile calls=%d", installation.reconciled)
	}
}

func TestControllerInstallFailureDoesNotCommitProtection(t *testing.T) {
	backend, _ := validFake()
	backend.ensureErr = errors.New("injected atomic install failure")
	controller, err := New(testConfig(backend))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Protect(context.Background()); !errors.Is(err, backend.ensureErr) {
		t.Fatalf("protect error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.State != StateVPNOff || snapshot.State.Protected() {
		t.Fatalf("failed installation committed=%+v", snapshot)
	}
}

func TestControllerRejectsConcurrentAndStaleLifecycleOperations(t *testing.T) {
	backend, _ := validFake()
	controller, err := New(testConfig(backend))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := controller.Protect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Protect(context.Background()); !errors.Is(err, ErrAlreadyProtected) {
		t.Fatalf("second protect error=%v", err)
	}
	if err := controller.Transition(generation+1, StateConnected); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale transition error=%v", err)
	}
}

func TestControllerClosesResourceReturnedWithBuildFailure(t *testing.T) {
	backend, _ := validFake()
	controller, err := New(testConfig(backend))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := controller.Protect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeResource{}
	buildErr := errors.New("injected partial tunnel build failure")
	if err := controller.Activate(context.Background(), generation, func(context.Context) (io.Closer, error) {
		return resource, buildErr
	}); !errors.Is(err, buildErr) {
		t.Fatalf("activate error=%v", err)
	}
	if resource.closed != 1 || controller.Snapshot().State != StateNoExit {
		t.Fatalf("resource closes=%d snapshot=%+v", resource.closed, controller.Snapshot())
	}
}

func TestControllerLateActivationCannotResurrectAfterDisconnect(t *testing.T) {
	backend, _ := validFake()
	controller, err := New(testConfig(backend))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := controller.Protect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	resource := &fakeResource{}
	result := make(chan error, 1)
	go func() {
		result <- controller.Activate(context.Background(), generation, func(ctx context.Context) (io.Closer, error) {
			close(started)
			<-ctx.Done()
			return resource, ctx.Err()
		})
	}()
	<-started
	if err := controller.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("late activation error=%v", err)
	}
	if resource.closed != 1 || controller.Snapshot().State != StateVPNOff {
		t.Fatalf("late resource closes=%d snapshot=%+v", resource.closed, controller.Snapshot())
	}
}

func TestControllerCloseIsIdempotentFromOffAndProtectedStates(t *testing.T) {
	for _, protected := range []bool{false, true} {
		backend, installation := validFake()
		controller, err := New(testConfig(backend))
		if err != nil {
			t.Fatal(err)
		}
		if protected {
			if _, err := controller.Protect(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if err := controller.Close(context.Background()); err != nil {
			t.Fatalf("protected=%v first Close: %v", protected, err)
		}
		if err := controller.Close(context.Background()); err != nil {
			t.Fatalf("protected=%v second Close: %v", protected, err)
		}
		if snapshot := controller.Snapshot(); snapshot.State != StateClosed {
			t.Fatalf("protected=%v snapshot=%+v", protected, snapshot)
		}
		wantRemovals := 0
		if protected {
			wantRemovals = 1
		}
		if installation.removed != wantRemovals {
			t.Fatalf("protected=%v removals=%d", protected, installation.removed)
		}
	}
}
