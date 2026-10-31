// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestWarmDiscoveryReuseExpiryAndFailure(t *testing.T) {
	node := distributedPrivateNode(t)
	now := time.Now()
	newProvider := func() *DiscoveryServerProvider {
		p, err := NewDiscoveryServerProvider(node, 1)
		if err != nil {
			t.Fatal(err)
		}
		p.now = func() time.Time { return now }
		t.Cleanup(func() { p.Close() })
		return p
	}
	source := newProvider()
	id, failed := testPeerID(t), testPeerID(t)
	addr := ma.StringCast("/ip4/127.0.0.1/tcp/12345")
	for _, remote := range []peer.ID{id, failed} {
		node.Host().Peerstore().AddAddr(remote, addr, time.Minute)
	}
	source.cache = []peer.ID{id, failed}
	source.failed = map[peer.ID]time.Time{failed: now.Add(time.Minute)}
	warm := source.WarmDiscovery()
	if len(warm.peers) != 1 || warm.peers[0].ID != id {
		t.Fatal("failed peer exported")
	}
	source.cache[0] = failed
	if warm.peers[0].ID != id {
		t.Fatal("snapshot aliases source")
	}
	for _, test := range []struct {
		name   string
		mutate func(*WarmDiscovery)
	}{
		{"expired", func(w *WarmDiscovery) { w.expires = now }},
		{"future", func(w *WarmDiscovery) { w.expires = now.Add(time.Minute) }},
		{"other identity", func(w *WarmDiscovery) { w.local = failed }},
		{"oversize", func(w *WarmDiscovery) { w.peers = make([]peer.AddrInfo, MaxRendezvousCache+1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := *warm
			test.mutate(&copy)
			p := newProvider()
			p.RestoreWarmDiscovery(&copy)
			if len(p.cache) != 0 {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
	p := newProvider()
	p.RestoreWarmDiscovery(warm)
	values, err := p.Servers(context.Background(), "", 5)
	if err != nil || len(values) != 1 || values[0] != id {
		t.Fatal(values, err)
	}
	p.ReportFailure(id)
	p.RestoreWarmDiscovery(warm)
	if len(p.cache) != 0 {
		t.Fatal("handoff bypassed failure suppression")
	}
	p.lookupOverride = func(context.Context, string) ([]peer.ID, error) { return []peer.ID{failed}, nil }
	values, err = p.Servers(context.Background(), "NL", 5)
	if err != nil || len(values) != 1 || values[0] != failed {
		t.Fatal("normal discovery fallback failed", values, err)
	}
}

func TestWarmDiscoveryCountryRefresh(t *testing.T) {
	node := distributedPrivateNode(t)
	p, err := NewDiscoveryServerProvider(node, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	id := testPeerID(t)
	p.RestoreWarmDiscovery(&WarmDiscovery{local: node.PeerID(), expires: time.Now().Add(discoveryWarmTTL), peers: []peer.AddrInfo{{ID: id, Addrs: []ma.Multiaddr{ma.StringCast("/ip4/127.0.0.1/tcp/12345")}}}})
	started := make(chan struct{})
	p.lookupOverride = func(ctx context.Context, country string) ([]peer.ID, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	values, err := p.Servers(context.Background(), "NL", 5)
	if err != nil || len(values) != 1 || values[0] != id {
		t.Fatal("warm cache blocked on refresh", values, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("normal country refresh did not run")
	}
}
