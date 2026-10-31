// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestAdmissionLimitsPeerAndObservedSourceWithBoundedState(t *testing.T) {
	policy := DefaultAdmissionPolicy()
	policy.MaxKeys = 4
	policy.SourceBurstMultiplier = 1
	policy.GETInterval = time.Second
	policy.GETBurst = 2
	now := time.Unix(100, 0)
	limiter, err := newRequestAdmission(policy, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	address := ma.StringCast("/ip4/198.51.100.7/tcp/4001")
	if !limiter.Allow(peer.ID("peer-a"), address, admitGET) || !limiter.Allow(peer.ID("peer-a"), address, admitGET) {
		t.Fatal("initial burst was rejected")
	}
	if limiter.Allow(peer.ID("peer-a"), address, admitGET) || limiter.Allow(peer.ID("peer-b"), address, admitGET) {
		t.Fatal("PeerID or shared source prefix limit was bypassed")
	}
	now = now.Add(time.Second)
	if !limiter.Allow(peer.ID("peer-a"), address, admitGET) {
		t.Fatal("token did not refill")
	}
	if len(limiter.buckets) > policy.MaxKeys {
		t.Fatalf("admission buckets=%d max=%d", len(limiter.buckets), policy.MaxKeys)
	}
}
