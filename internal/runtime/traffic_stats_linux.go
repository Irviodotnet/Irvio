// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"hydra.local/hydra/internal/trafficstats"
)

// TrafficSource captures owners after Start; sampling avoids mutable daemon fields and preserves final counts.
func (d *Daemon) TrafficSource() trafficstats.Source {
	var own, shared, download func() (uint64, error)
	var plane *exitDataPlane
	if d.protection != nil {
		own = d.protection.TrafficReader()
	}
	if d.exitServer != nil {
		plane = d.exitServer.plane
		if reader, ok := plane.firewall.(interface{ TrafficBytes() (uint64, error) }); ok {
			shared = reader.TrafficBytes
		}
		if reader, ok := plane.firewall.(interface{ DownloadBytes() (uint64, error) }); ok {
			download = reader.DownloadBytes
		}
	}
	return func() trafficstats.Snapshot {
		var result trafficstats.Snapshot
		if own != nil {
			bytes, err := own()
			result.MyUpload, result.MyValid = bytes, err == nil
		}
		if shared != nil {
			bytes, err := shared()
			result.SharedUpload, result.SharedValid = bytes, err == nil
		}
		if download != nil {
			bytes, err := download()
			result.MyDownload, result.DownloadValid = bytes, err == nil
		}
		if plane == nil {
			result.ClientsValid = true
		} else {
			result.Clients, result.ClientsValid = plane.connectedClients(time.Now())
		}
		return result
	}
}

// Count unique installed, unexpired, non-revoking clients, not provisional leases or ICE attempts.
func (p *exitDataPlane) connectedClients(now time.Time) (uint32, bool) {
	if !p.mu.TryLock() {
		return 0, false
	}
	defer p.mu.Unlock()
	clients := make(map[peer.ID]struct{}, len(p.resources))
	for _, resource := range p.resources {
		if !resource.revoking && resource.lease.ValidUntil.After(now) {
			clients[resource.lease.ClientPeer] = struct{}{}
		}
	}
	return uint32(len(clients)), true
}
