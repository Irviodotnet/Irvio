// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const rendezvousVerificationParallelism = 4
const rendezvousVerificationTimeout = 1500 * time.Millisecond

// Authenticate hints concurrently so stale peers cannot exhaust the discovery deadline.
func verifyRendezvousProviders(ctx context.Context, self peer.ID, providers <-chan peer.AddrInfo,
	verify func(context.Context, peer.AddrInfo) (bool, error),
) ([]peer.ID, error) {
	workCtx, stop := context.WithCancel(ctx)
	defer stop()
	var mu sync.Mutex
	var workers sync.WaitGroup
	type peerAttempt struct {
		mu        sync.Mutex
		verified  bool
		addresses map[string]struct{}
	}
	seen := make(map[peer.ID]*peerAttempt)
	var result []peer.ID
	var failure error
	for i := 0; i < rendezvousVerificationParallelism; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				var provider peer.AddrInfo
				select {
				case <-workCtx.Done():
					return
				case candidate, open := <-providers:
					if !open {
						return
					}
					provider = candidate
				}
				if provider.ID == "" || provider.ID == self {
					continue
				}
				mu.Lock()
				entry := seen[provider.ID]
				if entry == nil {
					entry = &peerAttempt{addresses: make(map[string]struct{})}
					seen[provider.ID] = entry
				}
				mu.Unlock()
				var material strings.Builder
				for _, address := range provider.Addrs {
					material.WriteString(string(address.Bytes()))
					material.WriteByte(0)
				}
				entry.mu.Lock()
				_, duplicate := entry.addresses[material.String()]
				if entry.verified || duplicate || workCtx.Err() != nil {
					entry.mu.Unlock()
					continue
				}
				entry.addresses[material.String()] = struct{}{}
				peerCtx, cancel := context.WithTimeout(workCtx, rendezvousVerificationTimeout)
				ok, err := verify(peerCtx, provider)
				if peerCtx.Err() != nil && errors.Is(err, peerCtx.Err()) {
					ok, err = false, nil
				}
				cancel()
				entry.verified = ok
				entry.mu.Unlock()
				mu.Lock()
				if err != nil && failure == nil {
					failure = err
					stop()
				}
				if ok {
					result = append(result, provider.ID)
				}
				mu.Unlock()
			}
		}()
	}
	workers.Wait()
	if failure != nil {
		return result, failure
	}
	return result, ctx.Err()
}
