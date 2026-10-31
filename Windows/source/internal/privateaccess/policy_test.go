// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitaccess"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/libp2p/go-libp2p/core/peer"
)

func grant(t *testing.T, store *Store, client peer.ID) {
	t.Helper()
	now := time.Now()
	invite, err := store.CreateInvitation(now, time.Hour)
	must(t, err)
	must(t, store.Reserve(invite.ID, invite.Secret, client, now))
	must(t, store.Approve(invite.ID, "test client", now))
}

func privateLifecycle(t *testing.T, policy *Policy, activator tunnellease.ResourceActivator, renewer tunnellease.ResourceRenewer, revoker tunnellease.ResourceRevoker) *tunnellease.Lifecycle {
	t.Helper()
	config := tunnellease.DefaultConfig(identity(t))
	config.IPv4Pool = netip.MustParsePrefix("10.240.0.0/24")
	allocator, err := tunnellease.New(config)
	must(t, err)
	lc := tunnellease.DefaultLifecycleConfig(allocator, revoker)
	lc.RequireSessionToken, lc.Admission = true, policy
	lc.Activator, lc.Renewer = activator, renewer
	lifecycle, err := tunnellease.NewLifecycle(lc)
	must(t, err)
	t.Cleanup(func() { policy.Close(); _ = lifecycle.Close(context.Background()) })
	return lifecycle
}

func revision(t *testing.T, policy *Policy, client peer.ID) uint64 {
	t.Helper()
	permit, err := policy.Acquire(context.Background(), client)
	must(t, err)
	defer permit.Release()
	return permit.Revision
}

func TestRepeatedApprovalPreservesLiveGrant(t *testing.T) {
	store, _, _ := fixture(t)
	client := identity(t)
	grant(t, store, client)
	policy, err := NewPolicy(store)
	must(t, err)
	defer policy.Close()
	permit, err := policy.Acquire(context.Background(), client)
	must(t, err)
	permit.Release()
	grant(t, store, client)
	next, err := policy.Acquire(context.Background(), client)
	must(t, err)
	next.Release()
	if next.Revision != permit.Revision || permit.Context.Err() != nil {
		t.Fatal("repeat approval invalidated an existing authorization")
	}
	must(t, policy.Revoke(context.Background(), client, func(context.Context, peer.ID) error { return nil }))
	if permit.Context.Err() == nil {
		t.Fatal("revoke did not cancel grant")
	}
	grant(t, store, client)
	if current := revision(t, policy, client); current <= permit.Revision {
		t.Fatal("explicit regrant reused revoked revision")
	}
}

func TestPolicyLeaseAdmissionAndRegrant(t *testing.T) {
	store, _, _ := fixture(t)
	client, stranger := identity(t), identity(t)
	grant(t, store, client)
	policy, err := NewPolicy(store)
	must(t, err)
	var cleaned atomic.Int32
	lifecycle := privateLifecycle(t, policy, nil, nil, tunnellease.ResourceRevokerFunc(func(context.Context, tunnellease.Lease, tunnellease.RevocationReason) error {
		cleaned.Add(1)
		return nil
	}))
	token := tunnellease.SessionToken{1}
	oldRevision := revision(t, policy, client)
	deadline := time.Now().Add(20 * time.Second)
	if _, err := lifecycle.Allocate(stranger); !errors.Is(err, exitaccess.ErrDenied) {
		t.Fatalf("unknown identity: %v", err)
	}
	if _, err := lifecycle.AllocateForSession(client, token); !errors.Is(err, exitaccess.ErrDenied) {
		t.Fatalf("legacy allocation bypass: %v", err)
	}
	if _, err := lifecycle.AllocateProvisionalForSession(client, token, deadline); !errors.Is(err, exitaccess.ErrDenied) {
		t.Fatalf("missing revision bypass: %v", err)
	}
	lease, err := lifecycle.AllocateAuthorizedProvisional(client, token, deadline, oldRevision)
	must(t, err)
	lease, err = lifecycle.ActivateSession(client, lease.ID, lease.Generation, token)
	must(t, err)
	lease, err = lifecycle.RenewSession(client, lease.ID, lease.Generation, token)
	must(t, err)
	permit, err := policy.Acquire(context.Background(), client)
	must(t, err)
	permit.Release()
	must(t, policy.Revoke(context.Background(), client, lifecycle.RevokeClient))
	if permit.Context.Err() == nil || cleaned.Load() != 1 || len(lifecycle.Status()) != 0 {
		t.Fatal("revoke left active state")
	}
	checks := []func() error{
		func() error {
			_, err := lifecycle.ActivateSession(client, lease.ID, lease.Generation, token)
			return err
		},
		func() error { _, err := lifecycle.RenewSession(client, lease.ID, lease.Generation, token); return err },
		func() error { _, err := lifecycle.LookupSession(client, lease.ID, lease.Generation, token); return err },
		func() error {
			_, err := lifecycle.MutateAndRenewSession(context.Background(), client, lease.ID, lease.Generation, token, func(context.Context, tunnellease.Lease) error { t.Error("revoked mutation ran"); return nil })
			return err
		},
	}
	for _, check := range checks {
		if err := check(); !errors.Is(err, exitaccess.ErrDenied) {
			t.Fatalf("post-revoke bypass: %v", err)
		}
	}
	grant(t, store, client)
	if _, err := lifecycle.AllocateAuthorizedProvisional(client, token, deadline, oldRevision); !errors.Is(err, exitaccess.ErrDenied) {
		t.Fatalf("old PREPARE survived regrant: %v", err)
	}
	_, err = lifecycle.AllocateAuthorizedProvisional(client, tunnellease.SessionToken{2}, deadline, revision(t, policy, client))
	must(t, err)
}

func TestPolicyRevokeWaitsForActivation(t *testing.T) {
	store, _, _ := fixture(t)
	client := identity(t)
	grant(t, store, client)
	policy, err := NewPolicy(store)
	must(t, err)
	started, proceed := make(chan struct{}), make(chan struct{})
	var live atomic.Bool
	activator := tunnellease.ResourceActivatorFunc(func(ctx context.Context, _ tunnellease.Lease) error {
		close(started)
		select {
		case <-proceed:
			live.Store(true)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	lifecycle := privateLifecycle(t, policy, activator, nil, tunnellease.ResourceRevokerFunc(func(context.Context, tunnellease.Lease, tunnellease.RevocationReason) error {
		live.Store(false)
		return nil
	}))
	token := tunnellease.SessionToken{3}
	lease, err := lifecycle.AllocateAuthorizedProvisional(client, token, time.Now().Add(20*time.Second), revision(t, policy, client))
	must(t, err)
	activated := make(chan error, 1)
	go func() {
		_, err := lifecycle.ActivateSession(client, lease.ID, lease.Generation, token)
		activated <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("activation did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	revoked := make(chan error, 1)
	go func() { revoked <- policy.Revoke(ctx, client, lifecycle.RevokeClient) }()
	select {
	case err := <-revoked:
		t.Fatalf("revoke completed during activation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(proceed)
	must(t, <-activated)
	must(t, <-revoked)
	if live.Load() || len(lifecycle.Status()) != 0 {
		t.Fatal("in-flight activation resurrected revoked resources")
	}
}

func TestPolicyRevokeWaitsForMutationAndFencesRenewal(t *testing.T) {
	store, _, _ := fixture(t)
	client := identity(t)
	grant(t, store, client)
	policy, err := NewPolicy(store)
	must(t, err)
	lifecycle := privateLifecycle(t, policy, nil, nil, tunnellease.ResourceRevokerFunc(func(context.Context, tunnellease.Lease, tunnellease.RevocationReason) error { return nil }))
	token := tunnellease.SessionToken{4}
	lease, err := lifecycle.AllocateAuthorizedProvisional(client, token, time.Now().Add(20*time.Second), revision(t, policy, client))
	must(t, err)
	lease, err = lifecycle.ActivateSession(client, lease.ID, lease.Generation, token)
	must(t, err)
	started, proceed := make(chan struct{}), make(chan struct{})
	mutated := make(chan error, 1)
	go func() {
		_, err := lifecycle.MutateAndRenewSession(context.Background(), client, lease.ID, lease.Generation, token, func(ctx context.Context, _ tunnellease.Lease) error {
			close(started)
			select {
			case <-proceed:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		mutated <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("mutation did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	revoked := make(chan error, 1)
	go func() { revoked <- policy.Revoke(ctx, client, lifecycle.RevokeClient) }()
	select {
	case err := <-revoked:
		t.Fatalf("revoke completed during mutation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(proceed)
	must(t, <-mutated)
	must(t, <-revoked)
	if len(lifecycle.Status()) != 0 {
		t.Fatal("mutation escaped cleanup")
	}
}

func TestPolicyRevocationFailureKeepsDenial(t *testing.T) {
	store, _, _ := fixture(t)
	client := identity(t)
	grant(t, store, client)
	policy, err := NewPolicy(store)
	must(t, err)
	defer policy.Close()
	cleanupErr := errors.New("cleanup failed")
	if err := policy.Revoke(context.Background(), client, func(context.Context, peer.ID) error { return cleanupErr }); !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanup failure hidden: %v", err)
	}
	if permit, err := policy.Acquire(context.Background(), client); err == nil {
		permit.Release()
		t.Fatal("failed cleanup reopened admissions")
	}
}

func TestPolicyRevocationDrainsAsyncWork(t *testing.T) {
	store, _, _ := fixture(t)
	client := identity(t)
	grant(t, store, client)
	policy, err := NewPolicy(store)
	must(t, err)
	defer policy.Close()
	work, err := policy.Begin(context.Background(), client, revision(t, policy, client))
	must(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	revoked := make(chan error, 1)
	go func() { revoked <- policy.Revoke(ctx, client, func(context.Context, peer.ID) error { return nil }) }()
	select {
	case <-work.Context.Done():
	case <-ctx.Done():
		t.Fatal("pending work not cancelled")
	}
	select {
	case err := <-revoked:
		t.Fatalf("revoke returned before drain: %v", err)
	default:
	}
	work.Done()
	work.Done()
	must(t, <-revoked)
	if policy.workCount != 0 || len(policy.work) != 0 {
		t.Fatal("work bookkeeping leaked")
	}
}

func TestPolicyDrainTimeoutIsNotSuccess(t *testing.T) {
	store, _, _ := fixture(t)
	client := identity(t)
	grant(t, store, client)
	policy, err := NewPolicy(store)
	must(t, err)
	defer policy.Close()
	work, err := policy.Begin(context.Background(), client, revision(t, policy, client))
	must(t, err)
	defer work.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = policy.Revoke(ctx, client, func(context.Context, peer.ID) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("incomplete drain reported success: %v", err)
	}
	if permit, err := policy.Acquire(context.Background(), client); err == nil {
		permit.Release()
		t.Fatal("drain timeout reopened access")
	}
}
