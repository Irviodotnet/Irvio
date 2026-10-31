// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"bytes"
	"context"
	"encoding/hex"
	"net"
	"sync"
	"testing"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

type enrollmentWireCapture struct {
	net.Conn
	mu   sync.Mutex
	wire []byte
}

func (c *enrollmentWireCapture) Write(data []byte) (int, error) {
	n, err := c.Conn.Write(data)
	c.mu.Lock()
	c.wire = append(c.wire, data[:n]...)
	c.mu.Unlock()
	return n, err
}

func (c *enrollmentWireCapture) assertSecretHidden(t *testing.T, secret string) {
	t.Helper()
	raw, err := hex.DecodeString(secret)
	must(t, err)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.wire) == 0 || bytes.Contains(c.wire, raw) || bytes.Contains(c.wire, []byte(secret)) {
		t.Fatal("enrollment carrier is empty or exposes the reusable invitation secret")
	}
}

func TestEnrollmentTLSApprovalAndRevocation(t *testing.T) {
	hostKey, _, err := crypto.GenerateEd25519Key(nil)
	must(t, err)
	host, err := peer.IDFromPrivateKey(hostKey)
	must(t, err)
	directory := privateTestDirectory(t)
	store, err := Open(directory, host)
	must(t, err)
	defer store.Close()
	server, err := NewEnrollmentServer(store, hostKey)
	must(t, err)
	for i := 0; i < 8; i++ {
		server.slots <- struct{}{}
	}
	a, b := net.Pipe()
	if err := server.Serve(context.Background(), a); err != ErrCapacity {
		t.Fatal("enrollment capacity was not enforced")
	}
	b.Close()
	for i := 0; i < 8; i++ {
		<-server.slots
	}
	stalled, remote := net.Pipe()
	defer remote.Close()
	canceled, stop := context.WithCancel(context.Background())
	drained := make(chan error, 1)
	go func() { drained <- server.Serve(canceled, stalled) }()
	stop()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("canceled handshake did not drain")
	}
	now := time.Now()
	invite, err := store.CreateInvitation(now, time.Hour)
	must(t, err)
	state, err := store.Snapshot()
	must(t, err)
	locator, err := hex.DecodeString(state.Locator)
	must(t, err)
	token := InvitationToken{Host: host, Invitation: invite}
	copy(token.Locator[:], locator)
	clientKey, _, err := crypto.GenerateEd25519Key(nil)
	must(t, err)
	client, err := peer.IDFromPrivateKey(clientKey)
	must(t, err)
	otherKey, _, err := crypto.GenerateEd25519Key(nil)
	must(t, err)
	exchange := func(key crypto.PrivKey, value InvitationToken, poll bool) (irviov1.PrivateEnrollmentResponse_Status, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		must(t, err)
		defer listener.Close()
		b, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
		must(t, err)
		defer b.Close()
		a, err := listener.Accept()
		must(t, err)
		done := make(chan error, 1)
		hostWire := &enrollmentWireCapture{Conn: a}
		clientWire := &enrollmentWireCapture{Conn: b}
		go func() { done <- server.Serve(ctx, hostWire) }()
		status, err := Enroll(ctx, clientWire, key, value, poll)
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("enrollment server did not drain")
		}
		hostWire.assertSecretHidden(t, value.Invitation.Secret)
		clientWire.assertSecretHidden(t, value.Invitation.Secret)
		return status, err
	}
	for i := 0; i < 2; i++ {
		status, err := exchange(clientKey, token, false)
		must(t, err)
		if status != irviov1.PrivateEnrollmentResponse_PENDING {
			t.Fatal("reservation not idempotent")
		}
	}
	state, err = store.Snapshot()
	must(t, err)
	if state.Authorized(client) {
		t.Fatal("enrollment bypassed owner approval")
	}
	status, err := exchange(otherKey, token, false)
	must(t, err)
	if status != irviov1.PrivateEnrollmentResponse_DENIED {
		t.Fatal("copied invitation stole pending reservation")
	}
	must(t, store.Approve(invite.ID, "friend", time.Now()))
	status, err = exchange(clientKey, token, true)
	must(t, err)
	if status != irviov1.PrivateEnrollmentResponse_APPROVED {
		t.Fatal("approved client not recognized")
	}
	status, err = exchange(otherKey, token, true)
	must(t, err)
	if status != irviov1.PrivateEnrollmentResponse_DENIED {
		t.Fatal("status exposed another client's approval")
	}
	must(t, store.Revoke(client))
	status, err = exchange(clientKey, token, true)
	must(t, err)
	if status != irviov1.PrivateEnrollmentResponse_DENIED {
		t.Fatal("revoked client remained approved")
	}
	wrong := token
	auto, err := store.CreateAccessInvitation(time.Now(), time.Hour)
	must(t, err)
	autoToken := token
	autoToken.Invitation = auto
	status, err = exchange(clientKey, autoToken, false)
	must(t, err)
	if status != irviov1.PrivateEnrollmentResponse_APPROVED {
		t.Fatal("access invitation did not grant authenticated redeemer")
	}
	status, err = exchange(otherKey, autoToken, false)
	must(t, err)
	if status != irviov1.PrivateEnrollmentResponse_DENIED {
		t.Fatal("consumed access invitation accepted another identity")
	}
	status, err = exchange(clientKey, autoToken, true)
	must(t, err)
	if status != irviov1.PrivateEnrollmentResponse_APPROVED {
		t.Fatal("retry after lost redemption response failed")
	}
	must(t, store.Revoke(client))
	status, err = exchange(clientKey, autoToken, true)
	must(t, err)
	if status != irviov1.PrivateEnrollmentResponse_DENIED {
		t.Fatal("revoked access invitation restored access")
	}
	wrong.Host, err = peer.IDFromPrivateKey(otherKey)
	must(t, err)
	if _, err := exchange(clientKey, wrong, false); err == nil {
		t.Fatal("TLS accepted wrong host identity")
	}
}
