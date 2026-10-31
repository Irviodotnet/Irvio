// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type privatePublisherFunc func(context.Context, [32]byte) error

func (f privatePublisherFunc) ProvidePrivateOwner(ctx context.Context, locator [32]byte) error {
	return f(ctx, locator)
}

func TestPrivatePublicationWithdrawAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	live := make([][32]byte, 12)
	for i := range live {
		live[i][0] = byte(i + 1)
	}
	snapshot := func() [][32]byte {
		mu.Lock()
		defer mu.Unlock()
		return append([][32]byte(nil), live...)
	}
	started := make(chan [32]byte, 12)
	stopped := make(chan [32]byte, 12)
	var active atomic.Int32
	publisher := privatePublisherFunc(func(ctx context.Context, locator [32]byte) error {
		if active.Add(1) > 4 {
			t.Error("publication concurrency exceeded four")
		}
		defer active.Add(-1)
		started <- locator
		<-ctx.Done()
		stopped <- locator
		return ctx.Err()
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPrivatePublication(ctx, snapshot, publisher, time.Millisecond, time.Minute)
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("publication not started")
		}
	}
	mu.Lock()
	live = nil
	mu.Unlock()
	for i := 0; i < 4; i++ {
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("withdrawal did not cancel publication")
		}
	}
	cancel()
	select {
	case <-done:
		if active.Load() != 0 {
			t.Fatal("publisher leaked workers")
		}
	case <-time.After(time.Second):
		t.Fatal("publisher did not stop")
	}
	select {
	case <-started:
		t.Fatal("withdrawn records were scheduled")
	default:
	}
}

func TestPrivatePublicationCoalescesDuplicatesAndRefreshes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	locator := [32]byte{1}
	calls := make(chan time.Time, 16)
	publisher := privatePublisherFunc(func(context.Context, [32]byte) error {
		calls <- time.Now()
		return nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPrivatePublication(ctx, func() [][32]byte { return [][32]byte{locator, locator, {}} }, publisher, time.Millisecond, 30*time.Millisecond)
	}()
	var previous time.Time
	for i := 0; i < 3; i++ {
		select {
		case now := <-calls:
			if !previous.IsZero() && now.Sub(previous) < 30*time.Millisecond {
				t.Error("refresh bypassed coalescing")
			}
			previous = now
		case <-time.After(time.Second):
			t.Fatal("refresh absent")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publisher did not stop")
	}
}
