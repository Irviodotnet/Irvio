// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/privateaccess"
)

func TestAccessInvitationCancelRacesRedemption(t *testing.T) {
	d := privateLifecycleController(t)
	defer d.Close(context.Background())
	for i := 0; i < 20; i++ {
		link, err := d.createPrivateInvitation(context.Background(), time.Hour, true)
		if err != nil {
			t.Fatal(err)
		}
		token, err := privateaccess.DecodeInvitation(link, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		client := privateLifecyclePeer(t)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.privateStore.Reserve(token.Invitation.ID, token.Invitation.Secret, client, time.Now())
		}()
		if err := d.CancelPrivateInvitation(context.Background(), token.Invitation.ID); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		state, err := d.privateStore.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if state.Authorized(client) {
			t.Fatal("deleting invitation left raced grant alive")
		}
	}
}

func TestPrivateManagementOfflineInvitationApprovalAndRevoke(t *testing.T) {
	d := privateLifecycleController(t)
	defer d.Close(context.Background())
	ctx := context.Background()
	link, err := d.CreatePrivateInvitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := privateaccess.DecodeInvitation(link, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	client := privateLifecyclePeer(t)
	if err := d.ApprovePrivateInvitation(ctx, token.Invitation.ID, "friend"); err == nil {
		t.Fatal("approved unreserved invitation")
	}
	if err := d.privateStore.Reserve(token.Invitation.ID, token.Invitation.Secret, client, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := d.ApprovePrivateInvitation(ctx, token.Invitation.ID, "friend"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreatePrivateInvitation(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	view, err := d.PrivateManagement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.HostPeerID != token.Host.String() || len(view.Members) != 1 || !view.Members[0].Allowed || len(view.Invitations) != 2 {
		t.Fatal("incorrect management view or consumed enrollment receipt lost")
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token.Invitation.Secret) || strings.Contains(string(encoded), hex.EncodeToString(token.Locator[:])) || strings.Contains(string(encoded), privateaccess.InvitationPrefix) {
		t.Fatal("management view leaked invitation or locator material")
	}
	view.Members[0].Allowed = false
	state, err := d.privateStore.Snapshot()
	if err != nil || !state.Authorized(client) {
		t.Fatal("view mutated authoritative grants")
	}
	if err := d.RevokePrivateMember(ctx, client); err != nil {
		t.Fatal(err)
	}
	state, err = d.privateStore.Snapshot()
	if err != nil || state.Authorized(client) {
		t.Fatal("offline revoke did not persist denial")
	}
	if !d.idleWithoutIntent() || d.daemon != nil || d.recoveryPending {
		t.Fatal("management enabled network participation")
	}
}

func TestPrivateManagementCancellationAndGuards(t *testing.T) {
	d := privateLifecycleController(t)
	ctx := context.Background()
	link, err := d.CreatePrivateInvitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := privateaccess.DecodeInvitation(link, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CancelPrivateInvitation(ctx, token.Invitation.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.privateStore.Reserve(token.Invitation.ID, token.Invitation.Secret, privateLifecyclePeer(t), time.Now()); err == nil {
		t.Fatal("canceled invitation reused")
	}
	for _, lifetime := range []time.Duration{0, 25 * time.Hour} {
		if _, err := d.CreatePrivateInvitation(ctx, lifetime); err == nil {
			t.Fatal("invalid lifetime accepted")
		}
	}
	id := privateLifecyclePeer(t)
	d.desiredPrivate = privateProductIntent{Target: id}
	if err := d.RemovePrivateTarget(ctx, id); err == nil {
		t.Fatal("active target removal accepted")
	}
	d.teardownPending = true
	if _, err := d.CreatePrivateInvitation(ctx, time.Hour); err == nil {
		t.Fatal("mutation during teardown accepted")
	}
	d.teardownPending = false
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.PrivateManagement(canceled); err == nil {
		t.Fatal("canceled command accepted")
	}
	if err := d.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PrivateManagement(ctx); err == nil {
		t.Fatal("closed controller reopened profile")
	}
}

func TestPrivateProductImportRemainsOfflineAndRedacted(t *testing.T) {
	host := privateLifecycleController(t)
	client := privateLifecycleController(t)
	defer host.Close(context.Background())
	defer client.Close(context.Background())
	link, err := host.CreatePrivateInvitation(context.Background(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := privateaccess.DecodeInvitation(link, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ImportPrivateInvitation(context.Background(), link, "friend"); err != nil {
		t.Fatal(err)
	}
	view, err := client.PrivateManagement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Enrollments) != 1 || view.Enrollments[0].HostPeerID != token.Host.String() || len(view.Targets) != 0 {
		t.Fatal("import bypassed enrollment approval")
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{link, token.Invitation.Secret, hex.EncodeToString(token.Locator[:])} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("import status exposed secret material")
		}
	}
	if !client.idleWithoutIntent() || client.daemon != nil {
		t.Fatal("import enabled network participation")
	}
	if err := client.CancelPrivateEnrollment(context.Background(), token.Host); err != nil {
		t.Fatal(err)
	}
	view, err = client.PrivateManagement(context.Background())
	if err != nil || len(view.Enrollments) != 0 {
		t.Fatal("canceled import retained")
	}
}
