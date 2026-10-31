// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

func TestWindowsProductConnectedClients(t *testing.T) {
	now := time.Now()
	p := &windowsExitDataPlane{ready: true, leases: map[tunnellease.ID]*windowsExitLease{
		{1}: {ready: true, lease: tunnellease.Lease{ClientPeer: "a", ValidUntil: now.Add(time.Minute)}},
		{2}: {ready: true, lease: tunnellease.Lease{ClientPeer: "a", ValidUntil: now.Add(time.Minute)}},
		{3}: {ready: false, lease: tunnellease.Lease{ClientPeer: "b", ValidUntil: now.Add(time.Minute)}},
		{4}: {ready: true, lease: tunnellease.Lease{ClientPeer: "c", ValidUntil: now.Add(-time.Second)}},
	}}
	if got := p.connectedPeers(now); len(got) != 1 || !got["a"] {
		t.Fatalf("clients=%v", got)
	}
	p.mu.Lock()
	if got := p.connectedPeers(now); got != nil {
		t.Error("busy sample must be unavailable")
	}
	p.mu.Unlock()
	p.closing = true
	if got := p.connectedPeers(now); got == nil || len(got) != 0 {
		t.Fatalf("closing clients=%v", got)
	}
	d := &Daemon{}
	sample := d.TrafficSource()()
	if !sample.ClientsValid || sample.Clients != 0 || sample.MyValid || sample.SharedValid || sample.DownloadValid {
		t.Fatalf("unsupported byte counters reported as valid: %+v", sample)
	}
}

func TestWindowsTrafficSourceRetainsLifetimeOwner(t *testing.T) {
	p := &windowsExitDataPlane{}
	d := &Daemon{exitServer: &ExitServer{plane: p}}
	source := d.TrafficSource()
	d.exitServer = nil
	p.closing = true
	p.mu.Lock()
	busy := source()
	p.mu.Unlock()
	if busy.ClientsValid || !busy.MyValid || !busy.DownloadValid || !busy.SharedValid {
		t.Fatalf("byte sampling must not wait for plane lifecycle lock: %+v", busy)
	}
	final := source()
	if !final.ClientsValid || final.Clients != 0 || !final.MyValid || !final.DownloadValid || !final.SharedValid {
		t.Fatalf("final owner unavailable: %+v", final)
	}
}
