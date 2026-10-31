// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"sync"
	"time"
)

type PrivateOwnerPublisher interface {
	ProvidePrivateOwner(context.Context, [32]byte) error
}

type privatePublicationResult struct {
	locator [32]byte
	err     error
}

// runPrivatePublication bounds work and cancels advertisements whose registrations cease to be live.
func runPrivatePublication(ctx context.Context, snapshot func() [][32]byte, publisher PrivateOwnerPublisher, poll, refresh time.Duration) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	running := make(map[[32]byte]context.CancelFunc)
	next := make(map[[32]byte]time.Time)
	completed := make(chan privatePublicationResult, 4)
	var workers sync.WaitGroup
	defer func() {
		for _, cancel := range running {
			cancel()
		}
		workers.Wait()
	}()
	reconcile := func() {
		if ctx.Err() != nil {
			return
		}
		live := make(map[[32]byte]struct{})
		for _, locator := range snapshot() {
			if locator != ([32]byte{}) && len(live) < 1024 {
				live[locator] = struct{}{}
			}
		}
		for locator, cancel := range running {
			if _, exists := live[locator]; !exists {
				cancel()
			}
		}
		for locator := range next {
			if _, exists := live[locator]; !exists {
				delete(next, locator)
			}
		}
		now := time.Now()
		for len(running) < 4 {
			var chosen [32]byte
			var earliest time.Time
			for locator := range live {
				if _, busy := running[locator]; busy || next[locator].After(now) {
					continue
				}
				if chosen == ([32]byte{}) || next[locator].Before(earliest) {
					chosen, earliest = locator, next[locator]
				}
			}
			if chosen == ([32]byte{}) {
				break
			}
			workCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			running[chosen] = cancel
			workers.Add(1)
			go func(locator [32]byte) {
				defer workers.Done()
				err := publisher.ProvidePrivateOwner(workCtx, locator)
				cancel()
				select {
				case completed <- privatePublicationResult{locator, err}:
				case <-ctx.Done():
				}
			}(chosen)
		}
	}
	reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		case result := <-completed:
			delete(running, result.locator)
			delay := refresh
			if result.err != nil {
				delay = 10 * time.Second
			}
			next[result.locator] = time.Now().Add(delay)
			reconcile()
		}
	}
}
