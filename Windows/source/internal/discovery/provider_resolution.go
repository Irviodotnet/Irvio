// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const providerAddressResolutionBudget = 250 * time.Millisecond

// processProviderResults permits later address-bearing results for initially addressless peers.
func processProviderResults(
	ctx context.Context,
	self peer.ID,
	providers <-chan peer.AddrInfo,
	storedAddrs func(peer.ID) []multiaddr.Multiaddr,
	findPeer func(context.Context, peer.ID) (peer.AddrInfo, error),
	consume func(peer.AddrInfo) (bool, error),
) error {
	var resolutionDeadline time.Time
	consumed := make(map[peer.ID]struct{})
	resolutionAttempted := make(map[peer.ID]struct{})
	for provider := range providers {
		if provider.ID == "" || provider.ID == self {
			continue
		}
		if _, done := consumed[provider.ID]; done {
			continue
		}
		provider.Addrs = uniqueProviderAddrs(provider.Addrs, storedAddrs(provider.ID))
		if len(provider.Addrs) == 0 {
			if _, attempted := resolutionAttempted[provider.ID]; !attempted {
				if resolutionDeadline.IsZero() {
					resolutionDeadline = time.Now().Add(providerAddressResolutionBudget)
				}
				if time.Now().Before(resolutionDeadline) {
					resolutionAttempted[provider.ID] = struct{}{}
					resolutionCtx, cancel := context.WithDeadline(ctx, resolutionDeadline)
					resolved, err := findPeer(resolutionCtx, provider.ID)
					cancel()
					if err == nil && resolved.ID == provider.ID {
						provider.Addrs = uniqueProviderAddrs(provider.Addrs, resolved.Addrs)
					}
					provider.Addrs = uniqueProviderAddrs(provider.Addrs, storedAddrs(provider.ID))
				}
			}
		}
		if len(provider.Addrs) == 0 {
			continue
		}
		done, err := consume(provider)
		if err != nil {
			return err
		}
		if done {
			consumed[provider.ID] = struct{}{}
		}
	}
	return nil
}

func uniqueProviderAddrs(groups ...[]multiaddr.Multiaddr) []multiaddr.Multiaddr {
	seen := make(map[string]struct{})
	var result []multiaddr.Multiaddr
	for _, group := range groups {
		for _, address := range group {
			key := string(address.Bytes())
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, address)
		}
	}
	return result
}
