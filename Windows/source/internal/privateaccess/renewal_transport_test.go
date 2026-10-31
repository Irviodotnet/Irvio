// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/sessioncontrol"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPrivateRevocationFencesInFlightNoiseRenewal(t *testing.T) {
	hostKey, _, err := crypto.GenerateEd25519Key(nil)
	must(t, err)
	hostID, err := peer.IDFromPrivateKey(hostKey)
	must(t, err)
	clientKey, _, err := crypto.GenerateEd25519Key(nil)
	must(t, err)
	clientID, err := peer.IDFromPrivateKey(clientKey)
	must(t, err)
	dir := privateTestDirectory(t)
	store, err := Open(dir, hostID)
	must(t, err)
	defer store.Close()
	grant(t, store, clientID)
	policy, err := NewPolicy(store)
	must(t, err)
	defer policy.Close()
	config := tunnellease.DefaultConfig(hostID)
	config.IPv4Pool = netip.MustParsePrefix("10.240.0.0/24")
	allocator, err := tunnellease.New(config)
	must(t, err)
	entered, proceed := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(proceed) })
	var revoked atomic.Int32
	lc := tunnellease.DefaultLifecycleConfig(allocator, tunnellease.ResourceRevokerFunc(func(context.Context, tunnellease.Lease, tunnellease.RevocationReason) error {
		revoked.Add(1)
		return nil
	}))
	lc.RequireSessionToken, lc.Admission = true, policy
	lc.Renewer = tunnellease.ResourceRenewerFunc(func(ctx context.Context, _, _ tunnellease.Lease) error {
		close(entered)
		select {
		case <-proceed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	lifecycle, err := tunnellease.NewLifecycle(lc)
	must(t, err)
	defer lifecycle.Close(context.Background())
	lease, err := lifecycle.AllocateAuthorizedProvisional(clientID, tunnellease.SessionToken{1}, time.Now().Add(time.Minute), revision(t, policy, clientID))
	must(t, err)
	lease, err = lifecycle.ActivateSession(clientID, lease.ID, lease.Generation, lease.Session)
	must(t, err)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	must(t, err)
	server, err := sessioncontrol.NewServer(listener, hostKey, sessioncontrol.LifecycleAuthority{Lifecycle: lifecycle}, 2*time.Second)
	must(t, err)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := sessioncontrol.Dial(ctx, listener.Addr().String(), clientKey, hostID, lease, (&net.Dialer{}).DialContext, 2*time.Second)
	must(t, err)
	defer client.Close()
	defer unblock.Do(func() { close(proceed) })
	requestID, err := sessioncontrol.NewRequestID()
	must(t, err)
	renewed := make(chan error, 1)
	go func() { _, err := client.RenewWithRequestID(ctx, lease, requestID); renewed <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("renewal did not reach resource boundary")
	}
	done := make(chan error, 1)
	go func() { done <- policy.Revoke(ctx, clientID, lifecycle.RevokeClient) }()
	select {
	case err := <-done:
		t.Fatalf("revocation completed while renewal could still commit: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	unblock.Do(func() { close(proceed) })
	select {
	case <-renewed:
	case <-ctx.Done():
		t.Fatal("renewal did not finish")
	}
	select {
	case err := <-done:
		must(t, err)
	case <-ctx.Done():
		t.Fatal("revocation did not finish")
	}
	if revoked.Load() != 1 || len(lifecycle.Status()) != 0 {
		t.Fatal("in-flight renewal left a revoked lease/resource")
	}
	if _, err := client.RenewWithRequestID(ctx, lease, requestID); !errors.Is(err, sessioncontrol.ErrRemote) {
		t.Fatalf("expected explicit remote denial of cached renewal after revocation, got %v", err)
	}
}
