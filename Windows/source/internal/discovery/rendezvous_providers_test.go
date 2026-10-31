// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestRendezvousDeadProvidersCannotBlockLivePeers(t *testing.T) {
	stream := make(chan peer.AddrInfo, 6)
	for _, id := range []peer.ID{"dead1", "dead2", "live1", "live2", "live1", "self"} {
		stream <- peer.AddrInfo{ID: id}
	}
	close(stream)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var active atomic.Int32
	values, _ := verifyRendezvousProviders(ctx, "self", stream, func(ctx context.Context, info peer.AddrInfo) (bool, error) {
		if n := active.Add(1); n > 4 {
			t.Errorf("unbounded verification: %d", n)
		}
		defer active.Add(-1)
		if info.ID == "dead1" || info.ID == "dead2" {
			<-ctx.Done()
			return false, ctx.Err()
		}
		return ctx.Err() == nil, nil
	})
	if !slices.Contains(values, peer.ID("live1")) || !slices.Contains(values, peer.ID("live2")) || len(values) != 2 {
		t.Fatalf("dead hints blocked or duplicated live peers: %v", values)
	}
	if active.Load() != 0 {
		t.Fatal("verification workers remain after return")
	}
}

func TestRendezvousVerificationCancellationJoinsWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := verifyRendezvousProviders(ctx, "self", make(chan peer.AddrInfo), func(context.Context, peer.AddrInfo) (bool, error) {
		t.Fatal("canceled lookup must not verify a provider")
		return false, nil
	})
	if len(result) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup: %v %v", result, err)
	}
}

func TestRendezvousVerificationPropagatesPersistenceFailure(t *testing.T) {
	stream := make(chan peer.AddrInfo, 1)
	stream <- peer.AddrInfo{ID: "provider"}
	close(stream)
	want := errors.New("persist verified provider failed")
	_, err := verifyRendezvousProviders(context.Background(), "self", stream, func(context.Context, peer.AddrInfo) (bool, error) { return false, want })
	if !errors.Is(err, want) {
		t.Fatalf("persistence failure lost: %v", err)
	}
}
