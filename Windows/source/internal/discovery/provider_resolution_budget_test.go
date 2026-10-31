// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestProviderResolutionBudgetStartsWithFirstUnresolvedObservation(t *testing.T) {
	s := peer.ID("delayed-provider")
	address := ma.StringCast("/ip4/198.18.4.2/tcp/46054")
	stream := make(chan peer.AddrInfo)
	go func() {
		time.Sleep(providerAddressResolutionBudget + 50*time.Millisecond)
		stream <- peer.AddrInfo{ID: s}
		close(stream)
	}()
	lookups := 0
	consumed := 0
	err := processProviderResults(context.Background(), peer.ID("self"), stream,
		func(peer.ID) []ma.Multiaddr { return nil },
		func(_ context.Context, id peer.ID) (peer.AddrInfo, error) {
			lookups++
			return peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{address}}, nil
		},
		func(candidate peer.AddrInfo) (bool, error) {
			consumed++
			return true, nil
		})
	if err != nil || lookups != 1 || consumed != 1 {
		t.Fatalf("late first provider: lookups=%d consumed=%d err=%v", lookups, consumed, err)
	}
}
