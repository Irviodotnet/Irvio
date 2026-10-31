// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/underlay"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestRuntimePrivateTargetSurvivesRecovery(t *testing.T) {
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	target := exitselector.Target{Kind: exitselector.TargetPrivatePeer, PeerID: id}
	network := &fakeNetwork{current: networkstate.State{Generation: 1}, changes: make(chan networkstate.State), errors: make(chan error)}
	oracle := &fakeOracle{state: underlay.State{NetworkGeneration: 1, IPv4: underlay.FamilyEvidence{State: underlay.Reachable}}}
	protection := new(fakeProtection)
	selector := &fakeSelector{events: make(chan exitselector.Event, 8), closed: make(chan struct{})}
	received := make(chan exitselector.Target, 1)
	config := Config{Network: network, Underlay: oracle, Protection: protection, Now: time.Now,
		OperationTimeout: time.Second, EventCapacity: 32, UnderlayInterval: time.Minute,
		NewTargetSelector: func(value exitselector.Target, _ exitselector.UnderlayState) (Selector, error) {
			received <- value
			return selector, nil
		},
	}
	runtime, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	for _, invalid := range []exitselector.Target{
		{}, {Kind: 99}, {Kind: exitselector.TargetPrivatePeer},
		{Kind: exitselector.TargetPrivatePeer, PeerID: "invalid"},
		{Kind: exitselector.TargetPrivatePeer, PeerID: id, Country: "NL"},
		{Kind: exitselector.TargetPublicCountry, PeerID: id, Country: "NL"},
	} {
		if err := runtime.ConnectTarget(context.Background(), invalid); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	if protection.Snapshot().State != killswitch.StateVPNOff {
		t.Fatal("invalid intent changed protection")
	}
	if err := runtime.ConnectTarget(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateDiscovering)
	select {
	case got := <-received:
		if got != target {
			t.Fatal("factory target changed")
		}
	case <-time.After(time.Second):
		t.Fatal("typed factory was not invoked")
	}
	for _, step := range []struct {
		event exitselector.EventType
		state State
	}{
		{exitselector.EventActivePromoted, StateConnected},
		{exitselector.EventSamePeerRecoveryStarted, StateSamePeerRecovery},
		{exitselector.EventNoExitAvailable, StateNoExit},
		{exitselector.EventSamePeerRecovered, StateConnected},
	} {
		selector.events <- exitselector.Event{Type: step.event}
		waitRuntimeState(t, runtime, step.state)
		status := runtime.Status()
		if status.Target != target || status.Country != "" || !status.ProtectedMode.Protected() {
			t.Fatal("private intent or fail-closed state lost")
		}
	}
	if err := runtime.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, runtime, StateIdle)
	if status := runtime.Status(); status.Target != (exitselector.Target{}) || status.ProtectedMode != killswitch.StateVPNOff {
		t.Fatal("disconnect retained private intent or protection")
	}
	config.NewSelector = func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) { return nil, nil }
	if _, err := New(config); err == nil {
		t.Fatal("ambiguous selector factories accepted")
	}
	if validRuntimeTarget(target, false) {
		t.Fatal("legacy country factory accepted private intent")
	}
}
