// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

const discoveryWarmTTL = 30 * time.Second

// WarmDiscovery carries bounded, authenticated infrastructure hints, never Exit candidates or sessions.
type WarmDiscovery struct {
	local   peer.ID
	expires time.Time
	peers   []peer.AddrInfo
}

func (p *DiscoveryServerProvider) WarmDiscovery() *WarmDiscovery {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.node == nil {
		return nil
	}
	now := p.now()
	warm := &WarmDiscovery{local: p.node.PeerID(), expires: now.Add(discoveryWarmTTL)}
	for _, id := range p.cache {
		if p.failed[id].After(now) {
			continue
		}
		addrs := p.node.Host().Peerstore().Addrs(id)
		if len(addrs) > 16 {
			addrs = addrs[:16]
		}
		if len(addrs) != 0 {
			warm.peers = append(warm.peers, peer.AddrInfo{ID: id, Addrs: append([]ma.Multiaddr(nil), addrs...)})
		}
	}
	return warm
}

// RestoreWarmDiscovery is only for a new owner; normal refresh and failure suppression remain authoritative.
func (p *DiscoveryServerProvider) RestoreWarmDiscovery(warm *WarmDiscovery) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if warm == nil || p.closed || p.node == nil || warm.local != p.node.PeerID() ||
		!warm.expires.After(now) || warm.expires.After(now.Add(discoveryWarmTTL)) ||
		len(p.cache) != 0 || len(p.inflight) != 0 || len(p.failed) != 0 || len(warm.peers) > MaxRendezvousCache {
		return
	}
	for _, info := range warm.peers {
		if info.ID == "" || info.ID == warm.local || len(info.Addrs) == 0 || containsPeer(p.cache, info.ID) {
			continue
		}
		p.node.Host().Peerstore().AddAddrs(info.ID, info.Addrs, warm.expires.Sub(now))
		p.cache = append(p.cache, info.ID)
	}
	// Country lookups still refresh in the background while using the previously verified server set.
	p.refresh[""] = warm.expires
}
