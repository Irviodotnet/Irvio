// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package connsupervisor

import (
	"errors"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/icesession"
)

func TestActiveRegistrySupersedesOnlyWithNewerGeneration(t *testing.T) {
	registry, err := NewActiveRegistry(1)
	if err != nil {
		t.Fatal(err)
	}
	key := ConnectionKey{RemotePeer: testPeer(t), Purpose: icesession.PurposeExit}
	first := &fakeSession{}
	if err := registry.Promote(key, 1, first); err != nil {
		t.Fatal(err)
	}
	stale := &fakeSession{}
	if err := registry.Promote(key, 1, stale); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("stale promotion error = %v", err)
	}
	if !stale.closed.Load() || first.closed.Load() {
		t.Fatal("stale promotion affected current session incorrectly")
	}
	second := &fakeSession{}
	if err := registry.Promote(key, 2, second); err != nil {
		t.Fatal(err)
	}
	if !first.closed.Load() || second.closed.Load() || registry.Len() != 1 {
		t.Fatal("newer generation did not supersede atomically")
	}
	if err := registry.Remove(key, 1); err != nil || second.closed.Load() {
		t.Fatal("stale remove closed current generation")
	}
	if err := registry.Close(); err != nil || !second.closed.Load() || registry.Len() != 0 {
		t.Fatal("registry close did not release current session")
	}
}
