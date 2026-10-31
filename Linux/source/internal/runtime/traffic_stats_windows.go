// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"time"

	"github.com/Irviodotnet/Irvio/internal/trafficstats"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Capture the lifetime owner so generation changes and teardown retain final plaintext totals.
func (d *Daemon) TrafficSource() trafficstats.Source {
	var plane *windowsExitDataPlane
	if d.exitServer != nil {
		plane, _ = d.exitServer.plane.(*windowsExitDataPlane)
	}
	return func() trafficstats.Snapshot {
		clients := plane.connectedPeers(time.Now())
		result := trafficstats.Snapshot{Clients: uint32(len(clients)), ClientsValid: clients != nil}
		if plane != nil {
			bytes := plane.traffic.Snapshot()
			result.MyUpload, result.MyDownload, result.SharedUpload = bytes.MyUpload, bytes.MyDownload, bytes.SharedUpload
			result.MyValid, result.DownloadValid, result.SharedValid = true, true, true
		}
		return result
	}
}

func (d *Daemon) PrivateConnectedPeers() map[peer.ID]bool {
	if d.exitServer == nil {
		return map[peer.ID]bool{}
	}
	plane, ok := d.exitServer.plane.(*windowsExitDataPlane)
	if !ok {
		return nil
	}
	return plane.connectedPeers(time.Now())
}

func (p *windowsExitDataPlane) connectedPeers(now time.Time) map[peer.ID]bool {
	if p == nil {
		return map[peer.ID]bool{}
	}
	if !p.mu.TryLock() {
		return nil
	}
	defer p.mu.Unlock()
	clients := make(map[peer.ID]bool)
	if p.closing || !p.ready {
		return clients
	}
	for _, resource := range p.leases {
		if resource.ready && resource.lease.ValidUntil.After(now) {
			clients[resource.lease.ClientPeer] = true
		}
	}
	return clients
}
