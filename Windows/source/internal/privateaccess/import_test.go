// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func importedFixture(t *testing.T) (*Store, crypto.PrivKey, peer.ID, string) {
	t.Helper()
	key, _, err := crypto.GenerateEd25519Key(nil)
	must(t, err)
	id, err := peer.IDFromPrivateKey(key)
	must(t, err)
	dir := privateTestDirectory(t)
	store, err := Open(dir, id)
	must(t, err)
	t.Cleanup(func() { store.Close() })
	return store, key, id, dir
}

func importedLink(t *testing.T, store *Store, key crypto.PrivKey, now time.Time) (InvitationToken, string) {
	t.Helper()
	invite, err := store.CreateInvitation(now, time.Hour)
	must(t, err)
	state, err := store.Snapshot()
	must(t, err)
	id, err := peer.IDFromPrivateKey(key)
	must(t, err)
	locator, err := hex.DecodeString(state.Locator)
	must(t, err)
	token := InvitationToken{Host: id, Invitation: invite}
	copy(token.Locator[:], locator)
	link, err := EncodeInvitation(key, token, now)
	must(t, err)
	return token, link
}

func importedDialer(t *testing.T, store *Store, key crypto.PrivKey) EnrollmentDialer {
	t.Helper()
	server, err := NewEnrollmentServer(store, key)
	must(t, err)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	must(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() { defer workers.Done(); _ = server.Serve(ctx, conn) }()
		}
	}()
	t.Cleanup(func() { cancel(); listener.Close(); <-done; workers.Wait() })
	return func(ctx context.Context, _ InvitationToken) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", listener.Addr().String())
	}
}

func TestImportedEnrollmentTLSRestartAndCancellationFence(t *testing.T) {
	hostStore, hostKey, host, _ := importedFixture(t)
	clientStore, clientKey, client, dir := importedFixture(t)
	token, link := importedLink(t, hostStore, hostKey, time.Now())
	must(t, clientStore.ImportInvitation(link, "friend", time.Now()))
	must(t, clientStore.ImportInvitation(link, "duplicate", time.Now()))
	state, err := clientStore.Snapshot()
	must(t, err)
	if state.Version != 2 || len(state.Targets) != 0 || len(state.Enrollments) != 1 {
		t.Fatal("import granted access or lost dedupe")
	}
	dial := importedDialer(t, hostStore, hostKey)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, err := clientStore.ResumeEnrollment(ctx, host, clientKey, dial)
	must(t, err)
	if status != irviov1.PrivateEnrollmentResponse_PENDING {
		t.Fatal("unapproved enrollment not pending")
	}
	must(t, clientStore.Close())
	clientStore, err = Open(dir, client)
	must(t, err)
	defer clientStore.Close()
	must(t, hostStore.Approve(token.Invitation.ID, "client", time.Now()))
	status, err = clientStore.ResumeEnrollment(ctx, host, clientKey, dial)
	must(t, err)
	if status != irviov1.PrivateEnrollmentResponse_APPROVED {
		t.Fatal("approved status lost across restart")
	}
	state, err = clientStore.Snapshot()
	must(t, err)
	if len(state.Enrollments) != 0 || len(state.Targets) != 1 || state.Targets[0].PeerID != host.String() || state.Targets[0].Name != "friend" {
		t.Fatal("approved target not atomically saved")
	}
	token, link = importedLink(t, hostStore, hostKey, time.Now())
	must(t, clientStore.ImportInvitation(link, "old request", time.Now()))
	must(t, hostStore.Reserve(token.Invitation.ID, token.Invitation.Secret, client, time.Now()))
	must(t, hostStore.Approve(token.Invitation.ID, "client", time.Now()))
	_, err = clientStore.ResumeEnrollment(ctx, host, clientKey, func(ctx context.Context, token InvitationToken) (net.Conn, error) {
		must(t, clientStore.CancelEnrollment(host))
		must(t, clientStore.ImportInvitation(link, "new request", time.Now()))
		return dial(ctx, token)
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatal("late approval resurrected canceled request")
	}
	state, err = clientStore.Snapshot()
	must(t, err)
	if len(state.Enrollments) != 1 || state.Enrollments[0].Name != "new request" || state.Targets[0].Name != "friend" {
		t.Fatal("late response replaced current state")
	}
}

func TestImportedEnrollmentRejectsSelfKeyMismatchAndPreservesExpiredState(t *testing.T) {
	hostStore, hostKey, host, _ := importedFixture(t)
	clientStore, clientKey, client, dir := importedFixture(t)
	_, link := importedLink(t, hostStore, hostKey, time.Now())
	if err := hostStore.ImportInvitation(link, "self", time.Now()); !errors.Is(err, ErrDenied) {
		t.Fatal("self import allowed")
	}
	must(t, clientStore.ImportInvitation(link, "friend", time.Now()))
	noDial := func(context.Context, InvitationToken) (net.Conn, error) {
		t.Fatal("unexpected dial")
		return nil, ErrInvalid
	}
	if _, err := clientStore.ResumeEnrollment(context.Background(), host, hostKey, noDial); !errors.Is(err, ErrDenied) {
		t.Fatal("foreign identity used for enrollment")
	}
	if _, err := clientStore.ResumeEnrollment(context.Background(), identity(t), clientKey, noDial); !errors.Is(err, ErrDenied) {
		t.Fatal("unknown target dialed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := clientStore.ResumeEnrollment(ctx, host, clientKey, importedDialer(t, clientStore, clientKey)); err == nil {
		t.Fatal("carrier substituted a different TLS host")
	}
	unchanged, err := clientStore.Snapshot()
	must(t, err)
	if len(unchanged.Targets) != 0 || len(unchanged.Enrollments) != 1 {
		t.Fatal("untrusted carrier changed enrollment state")
	}
	must(t, clientStore.CancelEnrollment(host))
	past := time.Now().Add(-2 * time.Hour)
	_, expired := importedLink(t, hostStore, hostKey, past)
	if err := clientStore.ImportInvitation(expired, "expired", time.Now()); !errors.Is(err, ErrExpired) {
		t.Fatal("expired new import allowed")
	}
	must(t, clientStore.ImportInvitation(expired, "was valid", past))
	must(t, clientStore.Close())
	reopened, err := Open(dir, client)
	must(t, err)
	defer reopened.Close()
	state, err := reopened.Snapshot()
	must(t, err)
	if len(state.Enrollments) != 1 {
		t.Fatal("expiration corrupted durable pending state")
	}
}
