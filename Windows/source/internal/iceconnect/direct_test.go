// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestDirectHostConnectivity(t *testing.T) {
	tests := []struct {
		name     string
		networks []icesession.NetworkType
		want     icesession.NetworkType
	}{
		{name: "IPv4", networks: []icesession.NetworkType{icesession.NetworkUDP4}, want: icesession.NetworkUDP4},
		{name: "IPv6", networks: []icesession.NetworkType{icesession.NetworkUDP6}, want: icesession.NetworkUDP6},
		{name: "dual-stack", networks: []icesession.NetworkType{icesession.NetworkUDP4, icesession.NetworkUDP6}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			aStack, bStack, aAttempt, bAttempt, sessionID := newAttemptPair(t, test.networks...)
			aConnected, bConnected := connectPair(t, aAttempt, bAttempt)
			a := aConnected.Result()
			b := bConnected.Result()
			assertMirroredDirectPair(t, a, b, sessionID)
			if test.want != 0 && (a.Network != test.want || b.Network != test.want) {
				t.Fatalf("selected networks A=%d B=%d want=%d", a.Network, b.Network, test.want)
			}
			if a.AdapterRequestsAfter <= a.AdapterRequestsBefore || b.AdapterRequestsAfter <= b.AdapterRequestsBefore {
				t.Fatal("Irvio adapters observed no connectivity checks")
			}
			waitForPairEvent(t, aAttempt)
			waitForPairEvent(t, bAttempt)
			if _, err := aAttempt.Connect(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
				t.Fatalf("connected attempt was reusable: %v", err)
			}
			current, err := aConnected.CurrentResult()
			if err != nil {
				t.Fatal(err)
			}
			if current.LocalEndpoint != a.LocalEndpoint || current.RemoteEndpoint != a.RemoteEndpoint ||
				current.Network != a.Network || !current.Nominated {
				t.Fatalf("current selected-pair snapshot differs from the live pair: initial=%+v current=%+v", a, current)
			}

			if err := aConnected.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := aConnected.CurrentResult(); !errors.Is(err, ErrClosed) {
				t.Fatalf("closed connected session returned a current pair: %v", err)
			}
			if err := bConnected.Close(); err != nil {
				t.Fatal(err)
			}
			assertDataAndPhaseSurvive(t, aStack)
			assertDataAndPhaseSurvive(t, bStack)
		})
	}
}

func waitForPairEvent(t *testing.T, attempt *Attempt) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if attempt.Diagnostics().SelectedPairEvents > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("Pion emitted no selected-pair callback")
		case <-ticker.C:
		}
	}
}

func assertMirroredDirectPair(t *testing.T, a, b Result, sessionID icesession.SessionID) {
	t.Helper()
	if a.SessionID != sessionID || b.SessionID != sessionID || a.Role != icesession.RoleControlling || b.Role != icesession.RoleControlled {
		t.Fatal("session ID or role mismatch")
	}
	if a.SelectedLocalCandidate.Type != ice.CandidateTypeHost || a.SelectedRemoteCandidate.Type != ice.CandidateTypeHost ||
		b.SelectedLocalCandidate.Type != ice.CandidateTypeHost || b.SelectedRemoteCandidate.Type != ice.CandidateTypeHost {
		t.Fatal("selected pair is not HOST to HOST")
	}
	if a.LocalEndpoint != b.RemoteEndpoint || a.RemoteEndpoint != b.LocalEndpoint || a.Network != b.Network {
		t.Fatalf("selected pairs are not mirrors: A=%+v B=%+v", a, b)
	}
	for _, result := range []Result{a, b} {
		if !result.Nominated || result.PairState != ice.CandidatePairStateSucceeded ||
			result.RequestsSent == 0 || result.ResponsesReceived == 0 || result.PairID == 0 {
			t.Fatalf("missing selection/check evidence: %+v", result)
		}
		if result.ApplicationPacketsSent != 0 || result.ApplicationPacketsRecv != 0 ||
			result.ApplicationBytesSent != 0 || result.ApplicationBytesRecv != 0 {
			t.Fatalf("unexpected application traffic: %+v", result)
		}
	}
}

func assertDataAndPhaseSurvive(t *testing.T, stack *testStack) {
	t.Helper()
	if err := stack.engine.Close(); err != nil {
		t.Fatal(err)
	}
	if stats := stack.session.Adapter().Stats(); stats.ActiveUfrags != 0 || stats.ActiveTransactions != 0 {
		t.Fatalf("ICE adapter retained active state after close: %+v", stats)
	}
	var network string
	var address netip.Addr
	var family socketmux.AddressFamily
	if local, ok := stack.mux.LocalAddr(socketmux.IPv4); ok {
		network, address, family = "udp4", netip.MustParseAddr("127.0.0.1"), socketmux.IPv4
		_ = local
	} else {
		network, address, family = "udp6", netip.MustParseAddr("::1"), socketmux.IPv6
	}
	receiver, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, 0)))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	if _, err := stack.mux.Data().WriteTo(context.Background(), []byte("data-path-alive"), receiver.LocalAddr().(*net.UDPAddr).AddrPort()); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	if err := receiver.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	n, source, err := receiver.ReadFromUDPAddrPort(buffer)
	if err != nil || string(buffer[:n]) != "data-path-alive" {
		t.Fatalf("Data path failed after ICE close: n=%d source=%s err=%v", n, source, err)
	}
	local, _ := stack.mux.LocalAddr(family)
	if source.Port() != local.Port() {
		t.Fatalf("Data source port=%d mux=%d", source.Port(), local.Port())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := stack.phases.Acquire(ctx, icesession.ControlPhaseSTUN)
	if err != nil {
		t.Fatalf("reacquire STUN phase: %v", err)
	}
	lease.Release()
}

func TestWrongCredentialsAndUnreachableCandidateFail(t *testing.T) {
	for _, mutation := range []struct {
		name   string
		mutate func(*icesession.Description)
	}{
		{name: "password", mutate: func(description *icesession.Description) { description.Credentials.Pwd = "AAAAAAAAAAAAAAAAAAAAAA" }},
		{name: "ufrag", mutate: func(description *icesession.Description) { description.Credentials.Ufrag = "WrongUfrag" }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			aStack := newTestStack(t, icesession.NetworkUDP4)
			bStack := newTestStack(t, icesession.NetworkUDP4)
			aPeer, bPeer := newTestPeer(t), newTestPeer(t)
			id, _ := icesession.NewSessionID()
			remoteA := bStack.session.LocalDescription()
			mutation.mutate(&remoteA)
			config := DefaultConfig()
			config.Validation.AllowLoopback = true
			aAttempt := mustAttempt(t, aStack.session, controllingBinding(id, aPeer, bPeer), RemoteState{id, bPeer, icesession.RoleControlled, remoteA}, config)
			bAttempt := mustAttempt(t, bStack.session, controlledBinding(id, aPeer, bPeer), RemoteState{id, aPeer, icesession.RoleControlling, aStack.session.LocalDescription()}, config)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := aAttempt.LoadRemoteCandidates(ctx); err != nil {
				t.Fatal(err)
			}
			if err := bAttempt.LoadRemoteCandidates(ctx); err != nil {
				t.Fatal(err)
			}
			outcomes := make(chan error, 2)
			go func() { _, err := bAttempt.Connect(ctx); outcomes <- err }()
			waitForState(t, ctx, bAttempt, StateConnecting)
			go func() { _, err := aAttempt.Connect(ctx); outcomes <- err }()
			for range 2 {
				if err := <-outcomes; err == nil {
					t.Fatal("wrong credentials unexpectedly connected")
				}
			}
			if aAttempt.State() != StateFailed || bAttempt.State() != StateFailed {
				t.Fatalf("wrong-credential states A=%d B=%d", aAttempt.State(), bAttempt.State())
			}
			if _, err := aAttempt.Connect(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
				t.Fatalf("failed attempt was reusable: %v", err)
			}
		})
	}
}

func TestUnreachableCandidateHonorsContext(t *testing.T) {
	stack := newTestStack(t, icesession.NetworkUDP4)
	localPeer, remotePeer := newTestPeer(t), newTestPeer(t)
	id, _ := icesession.NewSessionID()
	description := stack.session.LocalDescription()
	description.Credentials = icesession.Credentials{Ufrag: "Remote1", Pwd: "BBBBBBBBBBBBBBBBBBBBBB"}
	description.Candidates[0].Address = netip.MustParseAddr("192.0.2.1")
	description.Candidates[0].Port = 49152
	config := DefaultConfig()
	config.Validation.AllowLoopback = true
	attempt := mustAttempt(t, stack.session, controllingBinding(id, localPeer, remotePeer), RemoteState{id, remotePeer, icesession.RoleControlled, description}, config)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := attempt.LoadRemoteCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := attempt.Connect(ctx); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unreachable candidate error=%v", err)
	}
	if attempt.State() != StateFailed || attempt.Stats().Timeouts != 1 {
		t.Fatalf("unreachable state=%d stats=%+v", attempt.State(), attempt.Stats())
	}
}

func controllingBinding(id icesession.SessionID, initiator, responder peer.ID) Binding {
	return Binding{id, initiator, responder, initiator, responder, icesession.PurposeExit, icesession.RoleControlling, icesession.RoleControlled}
}

func controlledBinding(id icesession.SessionID, initiator, responder peer.ID) Binding {
	return Binding{id, initiator, responder, responder, initiator, icesession.PurposeExit, icesession.RoleControlled, icesession.RoleControlling}
}

func mustAttempt(t *testing.T, local *icesession.PreparedSession, binding Binding, remote RemoteState, config Config) *Attempt {
	t.Helper()
	if _, bound := local.ConnectivityBinding(); !bound {
		bindPrepared(t, local, binding)
	}
	attempt, err := New(local, binding, remote, config)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}
