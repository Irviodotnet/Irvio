// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"testing"
	"time"
)

func TestTrafficClientCountInstalledUniqueAndUnexpired(t *testing.T) {
	now := time.Now()
	p := &exitDataPlane{resources: map[tunnellease.ID]*activeExitResource{
		{1}: {lease: tunnellease.Lease{ClientPeer: "a", ValidUntil: now.Add(time.Minute)}},
		{2}: {lease: tunnellease.Lease{ClientPeer: "a", ValidUntil: now.Add(time.Minute)}},
		{3}: {lease: tunnellease.Lease{ClientPeer: "b", ValidUntil: now.Add(time.Minute)}},
		{4}: {lease: tunnellease.Lease{ClientPeer: "c", ValidUntil: now.Add(time.Minute)}, revoking: true},
		{5}: {lease: tunnellease.Lease{ClientPeer: "d", ValidUntil: now.Add(-time.Second)}},
	}}
	if count, valid := p.connectedClients(now); !valid || count != 2 {
		t.Fatalf("count=%d valid=%v", count, valid)
	}
	p.mu.Lock()
	if _, valid := p.connectedClients(now); valid {
		t.Fatal("sampling must not wait for runtime mutex")
	}
	p.mu.Unlock()
	p.resources = nil
	if count, valid := p.connectedClients(now); !valid || count != 0 {
		t.Fatal("closed resources must report zero")
	}
}
