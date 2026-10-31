// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package reachability

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func TestSnapshotReadinessRequiresUnexpiredQuorum(t *testing.T) {
	c, _, request := controllerFixture(t)
	if err := c.Enable(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot()
	if !s.Ready(time.Now()) {
		t.Fatal("verified proof not ready")
	}
	s.State = StateVerifying
	if !s.Ready(time.Now()) {
		t.Fatal("renewing unexpired quorum lost readiness")
	}
	if s.Ready(s.Proof.ExpiresAt) {
		t.Fatal("expired quorum remains ready")
	}
	for _, state := range []State{StateDisabled, StateExpired, StateClosed, StateUnverified} {
		s.State = state
		if s.Ready(time.Now()) {
			t.Fatalf("state %s stayed ready", state)
		}
	}
	s.State = StateVerified
	s.Requested = false
	if s.Ready(time.Now()) {
		t.Fatal("disabled intent stayed ready")
	}
}

type replacementAttestor struct {
	base *fakeAttestor
	dead map[peer.ID]bool
}

func (a replacementAttestor) Request(ctx context.Context, verifier peer.ID, family irviov1.ReachabilityFamily, port uint16, network, epoch uint64) (*irviov1.SignedReachabilityAttestation, error) {
	if a.dead[verifier] {
		return nil, ErrUnreachable
	}
	return a.base.Request(ctx, verifier, family, port, network, epoch)
}

func TestControllerDiscoversReplacementVerifiersAfterFailure(t *testing.T) {
	c, attestor, request := controllerFixture(t)
	dead := make(map[peer.ID]bool)
	for _, id := range request.Verifiers {
		dead[id] = true
	}
	var replacements []peer.ID
	for i := 0; i < DefaultQuorum; i++ {
		key, _, err := crypto.GenerateEd25519Key(nil)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := peer.IDFromPrivateKey(key)
		attestor.keys[id] = key
		replacements = append(replacements, id)
	}
	c.config.Attestor = replacementAttestor{base: attestor, dead: dead}
	c.config.RetryDelay = 10 * time.Millisecond
	var refreshed atomic.Int32
	c.config.RefreshVerifiers = func(context.Context) ([]peer.ID, error) { refreshed.Add(1); return replacements, nil }
	if err := c.Enable(context.Background(), request); err == nil {
		t.Fatal("dead set produced a quorum")
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case snapshot := <-c.Events():
			if !snapshot.Ready(time.Now()) {
				continue
			}
			if refreshed.Load() != 1 || len(snapshot.Proof.Attestations) != DefaultQuorum {
				t.Fatalf("invalid replacement quorum: %+v", snapshot)
			}
			for _, signed := range snapshot.Proof.Attestations {
				value, err := VerifyAttestation(signed, time.Now())
				if err != nil || dead[peer.ID(value.GetVerifierPeerId())] {
					t.Fatalf("old verifier used: %v", err)
				}
			}
			return
		case <-deadline:
			t.Fatal("failed verifier set was never replaced")
		}
	}
}

func TestControllerRefreshCannotReduceQuorum(t *testing.T) {
	c, attestor, request := controllerFixture(t)
	attestor.fail = true
	c.config.RetryDelay = 10 * time.Millisecond
	var calls atomic.Int32
	c.config.RefreshVerifiers = func(context.Context) ([]peer.ID, error) { calls.Add(1); return request.Verifiers[:1], nil }
	if err := c.Enable(context.Background(), request); err == nil {
		t.Fatal("unreachable set succeeded")
	}
	deadline := time.After(time.Second)
	for calls.Load() < 2 {
		select {
		case <-c.Events():
		case <-deadline:
			t.Fatal("retry did not run")
		}
	}
	if c.Snapshot().Ready(time.Now()) {
		t.Fatal("invalid replacement weakened quorum")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.request.Verifiers) != len(request.Verifiers) {
		t.Fatal("invalid replacement changed verifier set")
	}
}
