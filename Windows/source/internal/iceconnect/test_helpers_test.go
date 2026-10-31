// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/iceengine"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

type testStack struct {
	mux      *socketmux.Mux
	registry *iceadapter.Registry
	phases   *icesession.PhaseManager
	engine   *iceengine.Engine
	session  *icesession.PreparedSession
}

func newTestStack(t *testing.T, networks ...icesession.NetworkType) *testStack {
	t.Helper()
	registry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = 0
	muxConfig.IPv4Policy = socketmux.FamilyDisabled
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	addresses := make([]netip.Addr, 0, len(networks))
	for _, network := range networks {
		switch network {
		case icesession.NetworkUDP4:
			muxConfig.IPv4Policy = socketmux.FamilyRequired
			muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
			addresses = append(addresses, muxConfig.IPv4Bind)
		case icesession.NetworkUDP6:
			muxConfig.IPv6Policy = socketmux.FamilyRequired
			muxConfig.IPv6Bind = netip.MustParseAddr("::1")
			addresses = append(addresses, muxConfig.IPv6Bind)
		default:
			t.Fatalf("unsupported test network %d", network)
		}
	}
	muxConfig.Classifier = iceadapter.NewClassifier(registry)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		if len(networks) == 1 && networks[0] == icesession.NetworkUDP6 {
			t.Skipf("IPv6 loopback unavailable: %v", err)
		}
		t.Fatal(err)
	}
	phases := icesession.NewPhaseManager()
	engineConfig := iceengine.DefaultConfig(addresses...)
	engineConfig.Session.Validation.AllowLoopback = true
	engineConfig.Session.Logger = muxConfig.Logger
	engineConfig.Logger = muxConfig.Logger
	lease, err := phases.Acquire(context.Background(), icesession.ControlPhaseICE)
	if err != nil {
		_ = mux.Close()
		t.Fatal(err)
	}
	engine, err := iceengine.New(mux, registry, lease, engineConfig)
	if err != nil {
		lease.Release()
		_ = mux.Close()
		t.Fatal(err)
	}
	session, err := engine.Prepare(context.Background())
	if err != nil {
		_ = engine.Close()
		_ = mux.Close()
		t.Fatal(err)
	}
	stack := &testStack{mux: mux, registry: registry, phases: phases, engine: engine, session: session}
	t.Cleanup(func() {
		_ = stack.session.Close()
		_ = stack.engine.Close()
		_ = stack.phases.Close()
		_ = stack.mux.Close()
	})
	return stack
}

func newTestPeer(t *testing.T) peer.ID {
	t.Helper()
	privateKey, _, err := libcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func newAttemptPair(t *testing.T, networks ...icesession.NetworkType) (*testStack, *testStack, *Attempt, *Attempt, icesession.SessionID) {
	t.Helper()
	aStack := newTestStack(t, networks...)
	bStack := newTestStack(t, networks...)
	aPeer := newTestPeer(t)
	bPeer := newTestPeer(t)
	sessionID, err := icesession.NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Validation.AllowLoopback = true
	aBinding := Binding{
		SessionID: sessionID, Initiator: aPeer, Responder: bPeer, LocalPeer: aPeer, RemotePeer: bPeer,
		Purpose: icesession.PurposeExit, LocalRole: icesession.RoleControlling, RemoteRole: icesession.RoleControlled,
	}
	bBinding := Binding{
		SessionID: sessionID, Initiator: aPeer, Responder: bPeer, LocalPeer: bPeer, RemotePeer: aPeer,
		Purpose: icesession.PurposeExit, LocalRole: icesession.RoleControlled, RemoteRole: icesession.RoleControlling,
	}
	bindPrepared(t, aStack.session, aBinding)
	bindPrepared(t, bStack.session, bBinding)
	aAttempt, err := New(aStack.session, aBinding, RemoteState{
		SessionID: sessionID, PeerID: bPeer, Role: icesession.RoleControlled, Description: bStack.session.LocalDescription(),
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	bAttempt, err := New(bStack.session, bBinding, RemoteState{
		SessionID: sessionID, PeerID: aPeer, Role: icesession.RoleControlling, Description: aStack.session.LocalDescription(),
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := aAttempt.LoadRemoteCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if err := bAttempt.LoadRemoteCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	return aStack, bStack, aAttempt, bAttempt, sessionID
}

func bindPrepared(t *testing.T, session *icesession.PreparedSession, binding Binding) {
	t.Helper()
	err := session.BindConnectivity(icesession.ConnectivityBinding{
		SessionID: binding.SessionID, Initiator: binding.Initiator, Responder: binding.Responder,
		LocalPeer: binding.LocalPeer, RemotePeer: binding.RemotePeer, Purpose: binding.Purpose,
		LocalRole: binding.LocalRole, RemoteRole: binding.RemoteRole,
	})
	if err != nil {
		t.Fatal(err)
	}
}

type connectOutcome struct {
	session *ConnectedSession
	err     error
}

func connectPair(t *testing.T, a, b *Attempt) (*ConnectedSession, *ConnectedSession) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	bResult := make(chan connectOutcome, 1)
	go func() {
		session, err := b.Connect(ctx)
		bResult <- connectOutcome{session: session, err: err}
	}()
	waitForState(t, ctx, b, StateConnecting)
	aSession, err := a.Connect(ctx)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	bOutcome := <-bResult
	if bOutcome.err != nil {
		t.Fatalf("Accept failed: %v", bOutcome.err)
	}
	t.Cleanup(func() {
		_ = aSession.Close()
		_ = bOutcome.session.Close()
	})
	return aSession, bOutcome.session
}

func waitForState(t *testing.T, ctx context.Context, attempt *Attempt, wanted State) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if attempt.State() == wanted {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for state %d: %v (current %d)", wanted, ctx.Err(), attempt.State())
		case <-ticker.C:
		}
	}
}
