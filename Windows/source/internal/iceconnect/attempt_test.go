// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/icesession"
)

func TestRemoteDescriptionIsImmutableAndSessionBound(t *testing.T) {
	local := newTestStack(t, icesession.NetworkUDP4)
	remote := newTestStack(t, icesession.NetworkUDP4)
	localPeer, remotePeer := newTestPeer(t), newTestPeer(t)
	id, _ := icesession.NewSessionID()
	description := remote.session.LocalDescription()
	original := description.Clone()
	config := DefaultConfig()
	config.Validation.AllowLoopback = true
	attempt := mustAttempt(t, local.session, controllingBinding(id, localPeer, remotePeer), RemoteState{
		SessionID: id, PeerID: remotePeer, Role: icesession.RoleControlled, Description: description,
	}, config)
	defer attempt.Close()
	description.Credentials.Pwd = "AAAAAAAAAAAAAAAAAAAAAA"
	description.Candidates[0].Port++
	if got := attempt.RemoteDescription(); !reflect.DeepEqual(got, original) {
		t.Fatalf("remote description mutated through caller alias: got=%+v want=%+v", got, original)
	}

	otherID, _ := icesession.NewSessionID()
	secondLocal := newTestStack(t, icesession.NetworkUDP4)
	bindPrepared(t, secondLocal.session, controllingBinding(id, localPeer, remotePeer))
	_, err := New(secondLocal.session, controllingBinding(id, localPeer, remotePeer), RemoteState{
		SessionID: otherID, PeerID: remotePeer, Role: icesession.RoleControlled, Description: original,
	}, config)
	if !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("cross-session description error=%v", err)
	}
}

func TestPreparedSessionMustMatchAuthenticatedBinding(t *testing.T) {
	local := newTestStack(t, icesession.NetworkUDP4)
	remote := newTestStack(t, icesession.NetworkUDP4)
	localPeer, remotePeer := newTestPeer(t), newTestPeer(t)
	id, _ := icesession.NewSessionID()
	binding := controllingBinding(id, localPeer, remotePeer)
	state := RemoteState{id, remotePeer, icesession.RoleControlled, remote.session.LocalDescription()}
	config := DefaultConfig()
	config.Validation.AllowLoopback = true
	if _, err := New(local.session, binding, state, config); !errors.Is(err, icesession.ErrSessionNotBound) {
		t.Fatalf("unbound prepared session error=%v", err)
	}

	otherID, _ := icesession.NewSessionID()
	bound := controllingBinding(otherID, localPeer, remotePeer)
	bindPrepared(t, local.session, bound)
	if _, err := New(local.session, binding, state, config); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("mismatched prepared session error=%v", err)
	}
	if err := local.session.BindConnectivity(icesession.ConnectivityBinding{
		SessionID: id, Initiator: localPeer, Responder: remotePeer,
		LocalPeer: localPeer, RemotePeer: remotePeer, Purpose: icesession.PurposeExit,
		LocalRole: icesession.RoleControlling, RemoteRole: icesession.RoleControlled,
	}); !errors.Is(err, icesession.ErrSessionAlreadyBound) {
		t.Fatalf("duplicate signaling binding error=%v", err)
	}
}

func TestRolePeerAndFamilyBindingValidation(t *testing.T) {
	localPeer, remotePeer := newTestPeer(t), newTestPeer(t)
	id, _ := icesession.NewSessionID()
	config := DefaultConfig()
	config.Validation.AllowLoopback = true
	tests := []struct {
		name   string
		change func(*Binding, *RemoteState)
	}{
		{name: "reversed initiator role", change: func(binding *Binding, _ *RemoteState) { binding.LocalRole = icesession.RoleControlled }},
		{name: "wrong remote role", change: func(_ *Binding, remote *RemoteState) { remote.Role = icesession.RoleControlling }},
		{name: "wrong remote peer", change: func(_ *Binding, remote *RemoteState) { remote.PeerID = newTestPeer(t) }},
		{name: "self session", change: func(binding *Binding, remote *RemoteState) {
			binding.Responder, binding.RemotePeer, remote.PeerID = localPeer, localPeer, localPeer
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			local := newTestStack(t, icesession.NetworkUDP4)
			remote := newTestStack(t, icesession.NetworkUDP4)
			binding := controllingBinding(id, localPeer, remotePeer)
			state := RemoteState{id, remotePeer, icesession.RoleControlled, remote.session.LocalDescription()}
			bindPrepared(t, local.session, binding)
			test.change(&binding, &state)
			if _, err := New(local.session, binding, state, config); !errors.Is(err, ErrInvalidBinding) {
				t.Fatalf("binding error=%v", err)
			}
		})
	}

	local := newTestStack(t, icesession.NetworkUDP4)
	remote6 := newTestStack(t, icesession.NetworkUDP6)
	bindPrepared(t, local.session, controllingBinding(id, localPeer, remotePeer))
	if _, err := New(local.session, controllingBinding(id, localPeer, remotePeer), RemoteState{
		id, remotePeer, icesession.RoleControlled, remote6.session.LocalDescription(),
	}, config); !errors.Is(err, ErrNoUsableFamily) {
		t.Fatalf("missing-family error=%v", err)
	}
}

func TestRemoteCandidateLoaderSupportsSRFLXAndRejectsDuplicates(t *testing.T) {
	local := newTestStack(t, icesession.NetworkUDP4)
	remote := newTestStack(t, icesession.NetworkUDP4)
	localPeer, remotePeer := newTestPeer(t), newTestPeer(t)
	id, _ := icesession.NewSessionID()
	description := remote.session.LocalDescription()
	host := description.Candidates[0]
	description.Candidates = append(description.Candidates, icesession.Candidate{
		Type: icesession.CandidateTypeServerReflexive, Network: icesession.NetworkUDP4,
		Address: netip.MustParseAddr("198.51.100.10"), Port: 51001,
		RelatedAddress: host.Address, RelatedPort: host.Port, Foundation: "srflx-test",
		Priority: host.Priority - 1, Component: 1,
	})
	config := DefaultConfig()
	config.Validation.AllowLoopback = true
	attempt := mustAttempt(t, local.session, controllingBinding(id, localPeer, remotePeer), RemoteState{
		id, remotePeer, icesession.RoleControlled, description,
	}, config)
	defer attempt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := attempt.LoadRemoteCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	remoteCandidates, err := local.session.Agent().GetRemoteCandidates()
	if err != nil || len(remoteCandidates) != 2 {
		t.Fatalf("remote candidates=%d err=%v", len(remoteCandidates), err)
	}
	if stats := attempt.Stats(); stats.RemoteCandidatesAdded != 2 || stats.RemoteCandidateSetsLoaded != 1 {
		t.Fatalf("loader stats=%+v", stats)
	}

	duplicateLocal := newTestStack(t, icesession.NetworkUDP4)
	duplicate := remote.session.LocalDescription()
	duplicate.Candidates = append(duplicate.Candidates, duplicate.Candidates[0])
	bindPrepared(t, duplicateLocal.session, controllingBinding(id, localPeer, remotePeer))
	if _, err := New(duplicateLocal.session, controllingBinding(id, localPeer, remotePeer), RemoteState{
		id, remotePeer, icesession.RoleControlled, duplicate,
	}, config); !errors.Is(err, icesession.ErrInvalidDescription) {
		t.Fatalf("duplicate candidate error=%v", err)
	}
}

func TestCandidateLoadFailureIsTerminal(t *testing.T) {
	local := newTestStack(t, icesession.NetworkUDP4)
	remote := newTestStack(t, icesession.NetworkUDP4)
	localPeer, remotePeer := newTestPeer(t), newTestPeer(t)
	id, _ := icesession.NewSessionID()
	config := DefaultConfig()
	config.Validation.AllowLoopback = true
	config.CandidateLoadTimeout = 50 * time.Millisecond
	attempt := mustAttempt(t, local.session, controllingBinding(id, localPeer, remotePeer), RemoteState{
		id, remotePeer, icesession.RoleControlled, remote.session.LocalDescription(),
	}, config)
	if err := local.session.Agent().Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := attempt.LoadRemoteCandidates(ctx); err == nil || !errors.Is(err, ErrCandidateLoad) {
		t.Fatalf("candidate load error=%v", err)
	}
	if attempt.State() != StateFailed || attempt.Stats().CandidateLoadFailures != 1 {
		t.Fatalf("state=%d stats=%+v", attempt.State(), attempt.Stats())
	}
	if _, err := attempt.Connect(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("partially loaded session was reusable: %v", err)
	}
}

func TestConcurrentConnectAndCloseLifecycle(t *testing.T) {
	stack := newTestStack(t, icesession.NetworkUDP4)
	remote := newTestStack(t, icesession.NetworkUDP4)
	localPeer, remotePeer := newTestPeer(t), newTestPeer(t)
	id, _ := icesession.NewSessionID()
	description := remote.session.LocalDescription()
	description.Candidates[0].Address = netip.MustParseAddr("192.0.2.20")
	description.Candidates[0].Port = 49152
	config := DefaultConfig()
	config.Validation.AllowLoopback = true
	attempt := mustAttempt(t, stack.session, controllingBinding(id, localPeer, remotePeer), RemoteState{
		id, remotePeer, icesession.RoleControlled, description,
	}, config)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := attempt.LoadRemoteCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			_, err := attempt.Connect(ctx)
			errorsOut <- err
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-errorsOut, <-errorsOut
	if first == nil || second == nil || attempt.Stats().ConnectAttemptsStarted != 1 || attempt.Stats().StateMisuseErrors != 1 {
		t.Fatalf("concurrent errors=%v/%v stats=%+v", first, second, attempt.Stats())
	}
	if err := attempt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := attempt.Close(); err != nil || attempt.State() != StateClosed {
		t.Fatalf("repeated close err=%v state=%d", err, attempt.State())
	}
}

func TestCloseBeforeAndDuringConnect(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		local := newTestStack(t, icesession.NetworkUDP4)
		remote := newTestStack(t, icesession.NetworkUDP4)
		localPeer, remotePeer := newTestPeer(t), newTestPeer(t)
		id, _ := icesession.NewSessionID()
		config := DefaultConfig()
		config.Validation.AllowLoopback = true
		attempt := mustAttempt(t, local.session, controllingBinding(id, localPeer, remotePeer), RemoteState{id, remotePeer, icesession.RoleControlled, remote.session.LocalDescription()}, config)
		if err := attempt.Close(); err != nil {
			t.Fatal(err)
		}
		if err := attempt.LoadRemoteCandidates(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatalf("load after close error=%v", err)
		}
	})

	t.Run("during", func(t *testing.T) {
		local := newTestStack(t, icesession.NetworkUDP4)
		remote := newTestStack(t, icesession.NetworkUDP4)
		localPeer, remotePeer := newTestPeer(t), newTestPeer(t)
		id, _ := icesession.NewSessionID()
		description := remote.session.LocalDescription()
		description.Candidates[0].Address = netip.MustParseAddr("192.0.2.21")
		config := DefaultConfig()
		config.Validation.AllowLoopback = true
		attempt := mustAttempt(t, local.session, controllingBinding(id, localPeer, remotePeer), RemoteState{id, remotePeer, icesession.RoleControlled, description}, config)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := attempt.LoadRemoteCandidates(ctx); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { _, err := attempt.Connect(ctx); result <- err }()
		waitForState(t, ctx, attempt, StateConnecting)
		if err := attempt.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if err == nil {
				t.Fatal("Connect succeeded during close")
			}
		case <-ctx.Done():
			t.Fatal("Connect did not unblock on Close")
		}
	})
}
