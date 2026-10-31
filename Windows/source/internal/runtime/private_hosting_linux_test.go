// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestDaemonPrivateRevokeUsesLeaseFence(t *testing.T) {
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	key, _, err := crypto.GenerateEd25519Key(nil)
	must(err)
	host, err := peer.IDFromPrivateKey(key)
	must(err)
	clientKey, _, err := crypto.GenerateEd25519Key(nil)
	must(err)
	client, err := peer.IDFromPrivateKey(clientKey)
	must(err)
	dir := t.TempDir()
	must(os.Chmod(dir, 0700))
	store, err := privateaccess.Open(dir, host)
	must(err)
	defer store.Close()
	invite, err := store.CreateInvitation(time.Now(), time.Hour)
	must(err)
	must(store.Reserve(invite.ID, invite.Secret, client, time.Now()))
	must(store.Approve(invite.ID, "friend", time.Now()))
	policy, err := privateaccess.NewPolicy(store)
	must(err)
	defer policy.Close()
	config := tunnellease.DefaultConfig(host)
	config.IPv4Pool = netip.MustParsePrefix("10.240.0.0/24")
	allocator, err := tunnellease.New(config)
	must(err)
	var revoked atomic.Int32
	lc := tunnellease.DefaultLifecycleConfig(allocator, tunnellease.ResourceRevokerFunc(func(context.Context, tunnellease.Lease, tunnellease.RevocationReason) error {
		revoked.Add(1)
		return nil
	}))
	lc.Admission, lc.RequireSessionToken = policy, true
	lifecycle, err := tunnellease.NewLifecycle(lc)
	must(err)
	defer lifecycle.Close(context.Background())
	permit, err := policy.Acquire(context.Background(), client)
	must(err)
	permit.Release()
	token := tunnellease.SessionToken{1}
	lease, err := lifecycle.AllocateAuthorizedProvisional(client, token, time.Now().Add(time.Minute), permit.Revision)
	must(err)
	_, err = lifecycle.ActivateSession(client, lease.ID, lease.Generation, token)
	must(err)
	daemon := &Daemon{privateAdminExit: &ExitServer{config: ExitServerConfig{PrivateAccess: policy}, lifecycle: lifecycle}}
	must(daemon.RevokePrivateMember(context.Background(), client))
	if permit.Context.Err() == nil || revoked.Load() != 1 || len(lifecycle.Status()) != 0 {
		t.Fatal("daemon revoke left authorized lease or resources")
	}
	if _, err := lifecycle.RenewSession(client, lease.ID, lease.Generation, token); err == nil {
		t.Fatal("revoked lease renewed")
	}
	if err := (&Daemon{}).RevokePrivateMember(context.Background(), client); !errors.Is(err, ErrNotStarted) {
		t.Fatal("unstarted daemon accepted revoke")
	}
}

func TestPrivateHostingIdentityAndPolicyOwnership(t *testing.T) {
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	key, _, err := crypto.GenerateEd25519Key(nil)
	must(err)
	id, err := peer.IDFromPrivateKey(key)
	must(err)
	directory := t.TempDir()
	must(os.Chmod(directory, 0700))
	store, err := privateaccess.Open(directory, id)
	must(err)
	defer store.Close()
	wrongKey, _, err := crypto.GenerateEd25519Key(nil)
	must(err)
	if _, err := newPrivateHostingAccess(store, wrongKey); err == nil {
		t.Fatal("foreign hosting identity accepted")
	}
	access, err := newPrivateHostingAccess(store, key)
	must(err)
	defer access.policy.Close()
	client, err := peer.IDFromPrivateKey(wrongKey)
	must(err)
	if _, err := access.policy.Acquire(context.Background(), client); err == nil {
		t.Fatal("unapproved client admitted")
	}
	invite, err := store.CreateInvitation(time.Now(), time.Hour)
	must(err)
	must(store.Reserve(invite.ID, invite.Secret, client, time.Now()))
	must(store.Approve(invite.ID, "friend", time.Now()))
	permit, err := access.policy.Acquire(context.Background(), client)
	must(err)
	permit.Release()
	daemon := &Daemon{privateHosting: access}
	must(daemon.closeResources(context.Background()))
	select {
	case <-permit.Context.Done():
	default:
		t.Fatal("daemon cleanup did not cancel private admission")
	}
	if _, err := access.policy.Acquire(context.Background(), client); err == nil {
		t.Fatal("daemon cleanup left admissions open")
	}
	state, err := store.Snapshot()
	must(err)
	if len(state.Members) != 1 || !state.Members[0].Allowed {
		t.Fatal("runtime cleanup removed durable approval")
	}
	config := DaemonConfig{DataDir: directory, Exit: true, Production: &ProductionConfig{}, PrivateHosting: store,
		M12: M12Config{Enabled: true, SessionControlPort: 51822}}
	if _, err := NewDaemon(config); err != nil {
		t.Fatalf("hosting-only configuration rejected: %v", err)
	}
	for _, mutate := range []func(*DaemonConfig){
		func(c *DaemonConfig) { c.Exit = false },
		func(c *DaemonConfig) { c.Production = nil },
		func(c *DaemonConfig) { c.M12.Enabled = false },
		func(c *DaemonConfig) { c.M12.Serve = true },
		func(c *DaemonConfig) { c.PublicRole.Requested = true },
		func(c *DaemonConfig) { c.Country = "NL" },
		func(c *DaemonConfig) { c.AutoExitCountry = true },
		func(c *DaemonConfig) { c.RequireExitCountry = true },
		func(c *DaemonConfig) { c.ProtectedMode = true },
		func(c *DaemonConfig) { c.ProtectAtStart = true },
		func(c *DaemonConfig) { c.Production = &ProductionConfig{ConnectCountry: "NL"} },
	} {
		invalid := config
		mutate(&invalid)
		if _, err := NewDaemon(invalid); err == nil {
			t.Fatal("contradictory private hosting configuration accepted")
		}
	}
	config.Production = &ProductionConfig{PrivateTarget: client, PrivateLocator: [32]byte{1}}
	config.ProtectedMode = true
	combined, err := NewDaemon(config)
	must(err)
	if err := combined.Connect(context.Background(), "NL"); err != ErrInvalidState {
		t.Fatal("private daemon accepted public connect intent")
	}
	if err := combined.ConnectPrivate(context.Background()); err != ErrNotStarted {
		t.Fatal("private connect before startup accepted")
	}
	config.Exit, config.PrivateHosting = false, nil
	_, err = NewDaemon(config)
	must(err)
	for _, mutate := range []func(*DaemonConfig){
		func(c *DaemonConfig) { c.Production.ConnectCountry = "NL" },
		func(c *DaemonConfig) { c.Production.PrivateLocator = [32]byte{} },
		func(c *DaemonConfig) { c.Production.PrivateTarget = "invalid" },
		func(c *DaemonConfig) { c.Production.PrivateTarget = "" },
		func(c *DaemonConfig) { c.Exit = true },
		func(c *DaemonConfig) { c.M12.Enabled = false },
		func(c *DaemonConfig) { c.M12.Serve = true },
		func(c *DaemonConfig) { c.PublicRole.Requested = true },
	} {
		invalid := config
		copy := *config.Production
		invalid.Production = &copy
		mutate(&invalid)
		if _, err := NewDaemon(invalid); err == nil {
			t.Fatal("contradictory private upstream configuration accepted")
		}
	}
}
