// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPrivateOwnerVerificationBoundAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := make(chan peer.AddrInfo, privateOwnerLimit)
	for i := 0; i < privateOwnerLimit; i++ {
		stream <- peer.AddrInfo{ID: peer.ID(string(rune('a' + i)))}
	}
	close(stream)
	started := make(chan struct{}, privateOwnerLimit)
	var active atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := verifyProviders(ctx, "self", stream, privateOwnerParallelism,
			func(ctx context.Context, _ peer.AddrInfo) (bool, error) {
				if active.Add(1) > privateOwnerParallelism {
					t.Error("private verification exceeded concurrency bound")
				}
				defer active.Add(-1)
				started <- struct{}{}
				<-ctx.Done()
				return false, ctx.Err()
			})
		done <- err
	}()
	for i := 0; i < privateOwnerParallelism; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("verification workers did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || active.Load() != 0 {
			t.Fatalf("workers not joined after cancellation: %v, active=%d", err, active.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("verification did not honor cancellation")
	}
}

func TestPrivateOwnerInvalidLocatorBeforeNetworkAccess(t *testing.T) {
	node := &Node{}
	if err := node.ProvidePrivateOwner(context.Background(), [32]byte{}); err == nil {
		t.Fatal("empty publication locator accepted")
	}
	if _, err := node.FindPrivateOwners(context.Background(), [32]byte{}); err == nil {
		t.Fatal("empty lookup locator accepted")
	}
}
