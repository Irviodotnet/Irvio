// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesignal

import (
	"errors"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/icesession"
)

func TestReplayRegistryBoundAndExpiry(t *testing.T) {
	registry := newReplayRegistry(2, time.Minute)
	remote := testPeerID(t)
	base := time.Now()
	first, _ := icesession.NewSessionID()
	second, _ := icesession.NewSessionID()
	third, _ := icesession.NewSessionID()
	if err := registry.claim(remote, first, base); err != nil {
		t.Fatal(err)
	}
	if err := registry.claim(remote, first, base); err == nil {
		t.Fatal("duplicate replay claim accepted")
	}
	if err := registry.claim(remote, second, base); err != nil {
		t.Fatal(err)
	}
	if err := registry.claim(remote, third, base); !errors.Is(err, ErrBusy) {
		t.Fatalf("full replay registry error = %v", err)
	}
	if len(registry.entries) != 2 {
		t.Fatalf("replay entries = %d", len(registry.entries))
	}
	registry.mu.Lock()
	registry.cleanupLocked(base.Add(2 * time.Minute))
	count := len(registry.entries)
	registry.mu.Unlock()
	if count != 0 {
		t.Fatalf("expired replay entries = %d", count)
	}
	if err := registry.claim(remote, third, base.Add(2*time.Minute)); err != nil {
		t.Fatalf("claim after expiry: %v", err)
	}
}

func TestSessionIDsAreRandomSizedAndNonzero(t *testing.T) {
	seen := make(map[icesession.SessionID]struct{}, 1024)
	for range 1024 {
		id, err := icesession.NewSessionID()
		if err != nil {
			t.Fatal(err)
		}
		if id == (icesession.SessionID{}) || len(id) != icesession.SessionIDSize {
			t.Fatal("invalid session ID")
		}
		if _, duplicate := seen[id]; duplicate {
			t.Fatal("duplicate random session ID")
		}
		seen[id] = struct{}{}
	}
}
