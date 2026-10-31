// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnellease

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type testRevoker struct {
	mu        sync.Mutex
	fail      bool
	failRenew bool
	revoked   []Lease
	renewed   [][2]Lease
}

func (r *testRevoker) RenewLease(_ context.Context, previous, renewed Lease) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renewed = append(r.renewed, [2]Lease{previous, renewed})
	if r.failRenew {
		return errors.New("controlled resource renewal failure")
	}
	return nil
}

func (r *testRevoker) RevokeLease(_ context.Context, lease Lease, _ RevocationReason) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revoked = append(r.revoked, lease)
	if r.fail {
		return errors.New("controlled cleanup failure")
	}
	return nil
}

func managedLifecycle(t *testing.T) (*Lifecycle, *time.Time, *testRevoker) {
	t.Helper()
	now := time.Unix(2000, 0).UTC()
	config := DefaultConfig(peer.ID("exit"))
	config.IPv4Pool = netip.MustParsePrefix("10.99.0.0/29")
	config.IPv6Pool = netip.MustParsePrefix("fd99::/124")
	config.TTL = time.Minute
	config.Now = func() time.Time { return now }
	random := make([]byte, 4096)
	for index := range random {
		random[index] = byte(index + 1)
	}
	config.Random = bytes.NewReader(random)
	allocator, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	revoker := new(testRevoker)
	lifecycleConfig := DefaultLifecycleConfig(allocator, revoker)
	lifecycleConfig.Renewer = revoker
	lifecycleConfig.Now = func() time.Time { return now }
	lifecycleConfig.SweepInterval = time.Hour
	lifecycleConfig.RetryDelay = time.Second
	lifecycle, err := NewLifecycle(lifecycleConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		revoker.mu.Lock()
		revoker.fail = false
		revoker.mu.Unlock()
		_ = lifecycle.Close(context.Background())
	})
	return lifecycle, &now, revoker
}

func TestLifecycleShutdownRetainsFailedLeaseForRetry(t *testing.T) {
	l, _, r := managedLifecycle(t)
	lease, err := l.Allocate(peer.ID("client"))
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.fail = true
	r.mu.Unlock()
	if err := l.Close(context.Background()); err == nil {
		t.Fatal("failed cleanup reported success")
	}
	status := l.Status()
	if len(status) != 1 || status[0].State != LeaseQuarantined || status[0].Lease.ID != lease.ID {
		t.Fatal("failed lease ownership lost", status)
	}
	if len(l.config.Allocator.Active()) != 1 {
		t.Fatal("failed cleanup released allocator ownership")
	}
	if _, err := l.Allocate(peer.ID("new-client")); !errors.Is(err, ErrClosed) {
		t.Fatal("shutdown reopened admissions", err)
	}
	r.mu.Lock()
	r.fail = false
	r.mu.Unlock()
	if err := l.Close(context.Background()); err != nil {
		t.Fatal("retry could not finish cleanup", err)
	}
	if len(l.Status()) != 0 || l.Stats().RevocationRetries != 1 {
		t.Fatal("quarantine was not drained", l.Status(), l.Stats())
	}
	if err := l.Close(context.Background()); err != nil {
		t.Fatal("completed close not idempotent", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.revoked) != 2 {
		t.Fatal("unexpected cleanup count", len(r.revoked))
	}
}

func TestLifecycleShutdownDeadlineRetainsInFlightActivation(t *testing.T) {
	l, _, r := managedLifecycle(t)
	started, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	l.config.Activator = ResourceActivatorFunc(func(context.Context, Lease) error {
		close(started)
		<-release
		return nil
	})
	allocated := make(chan error, 1)
	go func() { _, err := l.Allocate(peer.ID("client")); allocated <- err }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- l.Close(ctx) }()
	select {
	case err := <-closed:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("shutdown did not respect caller deadline", err)
		}
	case <-time.After(time.Second):
		unblock.Do(func() { close(release) })
		<-closed
		t.Fatal("shutdown blocked past deadline on activation")
	}
	r.mu.Lock()
	n := len(r.revoked)
	r.mu.Unlock()
	if n != 0 || len(l.Status()) != 1 {
		t.Fatal("cleanup raced unfinished activation")
	}
	unblock.Do(func() { close(release) })
	<-allocated
	if err := l.Close(context.Background()); err != nil || len(l.Status()) != 0 {
		t.Fatal("cleanup did not resume after activation joined", err)
	}
}

func TestLifecycleShutdownSerializesCleanupAndFencesRenew(t *testing.T) {
	l, _, _ := managedLifecycle(t)
	lease, err := l.Allocate(peer.ID("client"))
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	l.config.Revoker = ResourceRevokerFunc(func(context.Context, Lease, RevocationReason) error {
		close(started)
		<-release
		return nil
	})
	first := make(chan error, 1)
	go func() { first <- l.Close(context.Background()) }()
	<-started
	if _, err := l.Renew(lease.ClientPeer, lease.ID, lease.Generation); !errors.Is(err, ErrClosed) {
		t.Fatal("shutdown allowed renewal", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := l.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("second close did not honor deadline", err)
	}
	unblock.Do(func() { close(release) })
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := l.Close(context.Background()); err != nil {
		t.Fatal("completed cleanup was repeated", err)
	}
}

func TestLifecycleRenewalKeepsLeaseAndAdvancesGeneration(t *testing.T) {
	lifecycle, now, _ := managedLifecycle(t)
	lease, err := lifecycle.Allocate(peer.ID("client"))
	if err != nil {
		t.Fatal(err)
	}
	if lease.Generation != 1 {
		t.Fatalf("initial generation = %d", lease.Generation)
	}
	*now = now.Add(30 * time.Second)
	renewed, err := lifecycle.Renew(lease.ClientPeer, lease.ID, lease.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ID != lease.ID || renewed.IPv4 != lease.IPv4 || renewed.IPv6 != lease.IPv6 ||
		renewed.Generation != 2 || !renewed.ValidUntil.After(lease.ValidUntil) {
		t.Fatalf("renewal changed identity or address: before=%+v after=%+v", lease, renewed)
	}
	if _, err := lifecycle.Renew(lease.ClientPeer, lease.ID, lease.Generation); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("replayed renewal error = %v", err)
	}
}

func TestLifecycleReconcileDoesNotRevokeInFlightImmediateActivation(t *testing.T) {
	lifecycle, _, resources := managedLifecycle(t)
	started := make(chan struct{})
	continueActivation := make(chan struct{})
	lifecycle.config.Activator = ResourceActivatorFunc(func(_ context.Context, _ Lease) error {
		close(started)
		<-continueActivation
		return nil
	})
	type result struct {
		lease Lease
		err   error
	}
	completed := make(chan result, 1)
	go func() {
		lease, err := lifecycle.Allocate(peer.ID("client"))
		completed <- result{lease: lease, err: err}
	}()
	<-started

	if err := lifecycle.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := lifecycle.Status(); len(status) != 1 || status[0].State != LeaseRenewing {
		t.Fatalf("reconcile revoked an in-flight immediate activation: %+v", status)
	}
	resources.mu.Lock()
	revocations := len(resources.revoked)
	resources.mu.Unlock()
	if revocations != 0 {
		t.Fatalf("in-flight immediate activation was revoked %d times", revocations)
	}

	close(continueActivation)
	allocation := <-completed
	if allocation.err != nil || allocation.lease.ID.IsZero() {
		t.Fatalf("activation result=%+v err=%v", allocation.lease, allocation.err)
	}
	if status := lifecycle.Status(); len(status) != 1 || status[0].State != LeaseActive {
		t.Fatalf("activated lifecycle state=%+v", status)
	}
}

func TestLifecycleMutationFencesExpiryAndRenewsAfterCompletion(t *testing.T) {
	lifecycle, now, revoker := managedLifecycle(t)
	if err := lifecycle.RequireSessionTokens(); err != nil {
		t.Fatal(err)
	}
	token := SessionToken{1}
	lease, err := lifecycle.AllocateForSession(peer.ID("client"), token)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	continueMutation := make(chan struct{})
	type result struct {
		lease Lease
		err   error
	}
	completed := make(chan result, 1)
	go func() {
		renewed, mutationErr := lifecycle.MutateAndRenewSession(
			context.Background(), lease.ClientPeer, lease.ID, lease.Generation, token,
			func(context.Context, Lease) error {
				close(started)
				<-continueMutation
				return nil
			},
		)
		completed <- result{lease: renewed, err: mutationErr}
	}()
	<-started
	*now = lease.ValidUntil.Add(time.Second)
	if err := lifecycle.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := lifecycle.Status(); len(status) != 1 || status[0].State != LeaseRenewing {
		t.Fatalf("expiry revoked an in-flight authenticated mutation: %+v", status)
	}
	close(continueMutation)
	mutation := <-completed
	if mutation.err != nil || mutation.lease.Generation != lease.Generation+1 ||
		!mutation.lease.ValidUntil.After(*now) {
		t.Fatalf("mutation result=%+v err=%v now=%s", mutation.lease, mutation.err, *now)
	}
	revoker.mu.Lock()
	revocations := len(revoker.revoked)
	revoker.mu.Unlock()
	if revocations != 0 {
		t.Fatalf("mutation crossing expiry revoked resources %d times", revocations)
	}
}

func TestLifecycleProvisionalGrantActivatesExactlyOnce(t *testing.T) {
	lifecycle, now, _ := managedLifecycle(t)
	if err := lifecycle.RequireSessionTokens(); err != nil {
		t.Fatal(err)
	}
	activations := 0
	lifecycle.config.Activator = ResourceActivatorFunc(func(_ context.Context, _ Lease) error {
		activations++
		return nil
	})
	token := SessionToken{1}
	lease, err := lifecycle.AllocateProvisionalForSession(peer.ID("client"), token, now.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	status := lifecycle.Status()
	if len(status) != 1 || status[0].State != LeaseAllocated || status[0].ActivationDeadline != now.Add(20*time.Second) {
		t.Fatalf("provisional state=%+v", status)
	}
	if activations != 0 {
		t.Fatalf("resources activated before direct path was ready: %d", activations)
	}
	if _, err := lifecycle.LookupSession(lease.ClientPeer, lease.ID, lease.Generation, token); !errors.Is(err, ErrRevocationPending) {
		t.Fatalf("provisional lease was usable before activation: %v", err)
	}
	activated, err := lifecycle.ActivateSession(lease.ClientPeer, lease.ID, lease.Generation, token)
	if err != nil {
		t.Fatal(err)
	}
	if activated != lease || activations != 1 {
		t.Fatalf("activation result=%+v count=%d", activated, activations)
	}
	if _, err := lifecycle.ActivateSession(lease.ClientPeer, lease.ID, lease.Generation, token); err != nil {
		t.Fatalf("exact activation retry failed: %v", err)
	}
	if activations != 1 {
		t.Fatalf("exact retry activated resources twice: %d", activations)
	}
}

func TestLifecycleProvisionalExpiryRevokesBeforeAddressReuse(t *testing.T) {
	lifecycle, now, resources := managedLifecycle(t)
	if err := lifecycle.RequireSessionTokens(); err != nil {
		t.Fatal(err)
	}
	first, err := lifecycle.AllocateProvisionalForSession(peer.ID("client-a"), SessionToken{1}, now.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(21 * time.Second)
	if err := lifecycle.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(lifecycle.Status()) != 0 {
		t.Fatal("expired provisional grant retained allocator ownership")
	}
	resources.mu.Lock()
	revoked := append([]Lease(nil), resources.revoked...)
	resources.mu.Unlock()
	if len(revoked) != 1 || revoked[0].ID != first.ID {
		t.Fatalf("provisional revocations=%+v", revoked)
	}
	second, err := lifecycle.AllocateForSession(peer.ID("client-b"), SessionToken{2})
	if err != nil {
		t.Fatal(err)
	}
	if second.IPv4 != first.IPv4 || second.IPv6 != first.IPv6 {
		t.Fatalf("address was not reused only after cleanup: first=%+v second=%+v", first, second)
	}
}

func TestLifecycleSamePeerSupersessionRevokesOldResourcesBeforeGenerationAdvance(t *testing.T) {
	lifecycle, _, revoker := managedLifecycle(t)
	lifecycle.config.RequireSessionToken = true
	firstToken := SessionToken{1}
	secondToken := SessionToken{2}
	first, err := lifecycle.AllocateForSession(peer.ID("client"), firstToken)
	if err != nil {
		t.Fatal(err)
	}
	second, err := lifecycle.AllocateForSession(first.ClientPeer, secondToken)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.IPv4 != first.IPv4 || second.IPv6 != first.IPv6 ||
		second.Generation != first.Generation+1 {
		t.Fatalf("supersession changed identity/address or failed to advance generation: before=%+v after=%+v", first, second)
	}
	revoker.mu.Lock()
	revoked := append([]Lease(nil), revoker.revoked...)
	revoker.mu.Unlock()
	if len(revoked) != 1 || revoked[0].Generation != first.Generation {
		t.Fatalf("old session resources were not revoked first: %+v", revoked)
	}
	if second.Session != secondToken {
		t.Fatal("superseding session token was not committed")
	}
	if _, err := lifecycle.RenewSession(first.ClientPeer, first.ID, first.Generation, firstToken); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("old owner renewed superseded lease: %v", err)
	}
	if err := lifecycle.ReleaseSession(context.Background(), first.ClientPeer, first.ID, first.Generation, firstToken); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("old owner released superseding lease: %v", err)
	}
	if stats := lifecycle.Stats(); stats.Superseded != 1 {
		t.Fatalf("superseded count=%d", stats.Superseded)
	}
}

func TestLifecycleReconcileDoesNotRevokeInFlightSupersessionActivation(t *testing.T) {
	lifecycle, _, resources := managedLifecycle(t)
	if err := lifecycle.RequireSessionTokens(); err != nil {
		t.Fatal(err)
	}
	first, err := lifecycle.AllocateForSession(peer.ID("client"), SessionToken{1})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	continueActivation := make(chan struct{})
	lifecycle.config.Activator = ResourceActivatorFunc(func(_ context.Context, _ Lease) error {
		close(started)
		<-continueActivation
		return nil
	})
	type result struct {
		lease Lease
		err   error
	}
	completed := make(chan result, 1)
	go func() {
		lease, allocateErr := lifecycle.AllocateForSession(peer.ID("client"), SessionToken{2})
		completed <- result{lease: lease, err: allocateErr}
	}()
	<-started

	if err := lifecycle.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := lifecycle.Status(); len(status) != 1 || status[0].State != LeaseRenewing {
		t.Fatalf("reconcile revoked an in-flight supersession activation: %+v", status)
	}
	resources.mu.Lock()
	revocations := len(resources.revoked)
	resources.mu.Unlock()
	if revocations != 1 {
		t.Fatalf("supersession and reconcile produced %d revocations, want exactly the old owner", revocations)
	}

	close(continueActivation)
	allocation := <-completed
	if allocation.err != nil || allocation.lease.ID != first.ID || allocation.lease.Generation != first.Generation+1 {
		t.Fatalf("supersession activation result=%+v err=%v first=%+v", allocation.lease, allocation.err, first)
	}
	if status := lifecycle.Status(); len(status) != 1 || status[0].State != LeaseActive {
		t.Fatalf("superseded lifecycle state=%+v", status)
	}
}

func TestLifecycleExpiryRevokesBeforeAddressReuse(t *testing.T) {
	lifecycle, now, revoker := managedLifecycle(t)
	first, err := lifecycle.Allocate(peer.ID("client-a"))
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	if _, err := lifecycle.Allocate(peer.ID("client-b")); err != nil {
		t.Fatal(err)
	}
	if len(lifecycle.Status()) != 2 {
		t.Fatal("managed allocator silently expired an address before revocation")
	}
	if err := lifecycle.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	revoker.mu.Lock()
	count := len(revoker.revoked)
	revoker.mu.Unlock()
	if count != 1 {
		t.Fatalf("revocations = %d", count)
	}
	second, err := lifecycle.Allocate(peer.ID("client-c"))
	if err != nil {
		t.Fatal(err)
	}
	if second.IPv4 != first.IPv4 || second.IPv6 != first.IPv6 {
		t.Fatalf("address was not safely reused after cleanup: first=%s/%s second=%s/%s", first.IPv4, first.IPv6, second.IPv4, second.IPv6)
	}
}

func TestLifecycleCleanupFailureQuarantinesAddress(t *testing.T) {
	lifecycle, now, revoker := managedLifecycle(t)
	first, err := lifecycle.Allocate(peer.ID("client-a"))
	if err != nil {
		t.Fatal(err)
	}
	revoker.mu.Lock()
	revoker.fail = true
	revoker.mu.Unlock()
	*now = now.Add(2 * time.Minute)
	if err := lifecycle.Reconcile(context.Background()); err == nil {
		t.Fatal("cleanup failure was not surfaced")
	}
	status := lifecycle.Status()
	if len(status) != 1 || status[0].State != LeaseQuarantined || !status[0].HasError {
		t.Fatalf("lease not quarantined: %+v", status)
	}
	second, err := lifecycle.Allocate(peer.ID("client-b"))
	if err != nil {
		t.Fatal(err)
	}
	if second.IPv4 == first.IPv4 || second.IPv6 == first.IPv6 {
		t.Fatal("quarantined address was reallocated")
	}
	revoker.mu.Lock()
	revoker.fail = false
	revoker.mu.Unlock()
	*now = now.Add(2 * time.Second)
	if err := lifecycle.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, exists := lifecycle.config.Allocator.Lookup(first.ID); exists {
		t.Fatal("quarantine retry did not finalize address release")
	}
}

func TestLifecycleRenewVsExpiryHasOneWinnerAndIsolatesClients(t *testing.T) {
	lifecycle, now, _ := managedLifecycle(t)
	first, _ := lifecycle.Allocate(peer.ID("client-a"))
	*now = now.Add(30 * time.Second)
	second, _ := lifecycle.Allocate(peer.ID("client-b"))
	third, _ := lifecycle.Allocate(peer.ID("client-c"))
	*now = first.ValidUntil
	start := make(chan struct{})
	var renewed Lease
	var renewErr, expiryErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		renewed, renewErr = lifecycle.Renew(first.ClientPeer, first.ID, first.Generation)
	}()
	go func() {
		defer wait.Done()
		<-start
		expiryErr = lifecycle.Reconcile(context.Background())
	}()
	close(start)
	wait.Wait()
	if renewErr == nil && expiryErr == nil && renewed.ValidUntil.After(*now) {
		// Renewal held the lock first, so expiry observed the extended lease.
	} else if errors.Is(renewErr, ErrLeaseExpired) || errors.Is(renewErr, ErrRevocationPending) || errors.Is(renewErr, ErrUnknownLease) {
		// Expiry acquired the lifecycle lock first and fenced renewal.
	} else {
		t.Fatalf("unexpected race result: renew=%v expiry=%v renewed=%+v", renewErr, expiryErr, renewed)
	}
	active := lifecycle.Status()
	for _, wanted := range []ID{second.ID, third.ID} {
		found := false
		for _, status := range active {
			found = found || status.Lease.ID == wanted
		}
		if !found {
			t.Fatalf("unrelated lease %s was disturbed", wanted)
		}
	}
}

func TestLifecycleRenewalUpdatesResourceGenerationBeforeExpiryRevocation(t *testing.T) {
	lifecycle, now, resources := managedLifecycle(t)
	lease, err := lifecycle.Allocate(peer.ID("client"))
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(30 * time.Second)
	renewed, err := lifecycle.Renew(lease.ClientPeer, lease.ID, lease.Generation)
	if err != nil {
		t.Fatal(err)
	}
	resources.mu.Lock()
	renewals := append([][2]Lease(nil), resources.renewed...)
	resources.mu.Unlock()
	if len(renewals) != 1 || renewals[0][0].Generation != lease.Generation || renewals[0][1].Generation != renewed.Generation {
		t.Fatalf("resource renewal snapshots=%+v", renewals)
	}
	*now = renewed.ValidUntil
	if err := lifecycle.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	resources.mu.Lock()
	revoked := append([]Lease(nil), resources.revoked...)
	resources.mu.Unlock()
	if len(revoked) != 1 || revoked[0].Generation != renewed.Generation {
		t.Fatalf("expiry revoked stale resource generation: %+v want=%d", revoked, renewed.Generation)
	}
}

func TestLifecycleResourceRenewalFailureQuarantinesAddress(t *testing.T) {
	lifecycle, now, resources := managedLifecycle(t)
	lease, err := lifecycle.Allocate(peer.ID("client-a"))
	if err != nil {
		t.Fatal(err)
	}
	resources.mu.Lock()
	resources.failRenew = true
	resources.mu.Unlock()
	*now = now.Add(30 * time.Second)
	if _, err := lifecycle.Renew(lease.ClientPeer, lease.ID, lease.Generation); err == nil {
		t.Fatal("resource renewal failure was hidden")
	}
	status := lifecycle.Status()
	if len(status) != 1 || status[0].State != LeaseQuarantined || !status[0].HasError {
		t.Fatalf("renewal failure did not quarantine ownership: %+v", status)
	}
	other, err := lifecycle.Allocate(peer.ID("client-b"))
	if err != nil {
		t.Fatal(err)
	}
	if other.IPv4 == lease.IPv4 || other.IPv6 == lease.IPv6 {
		t.Fatal("renewal-quarantined address was reallocated")
	}
}
