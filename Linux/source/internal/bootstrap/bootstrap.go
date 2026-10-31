// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package bootstrap parses and connects explicitly configured bootstrap peers.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const MaxPeers = 32

type ConnectPolicy struct {
	MaxConcurrency int
	PeerTimeout    time.Duration
	OverallTimeout time.Duration
}

func DefaultConnectPolicy() ConnectPolicy {
	return ConnectPolicy{
		MaxConcurrency: 4,
		PeerTimeout:    4 * time.Second,
		OverallTimeout: 12 * time.Second,
	}
}

// Parse converts /p2p multiaddresses into deduplicated peer address records.
func Parse(addresses []string) ([]peer.AddrInfo, error) {
	byPeer := make(map[peer.ID]*peer.AddrInfo, len(addresses))
	order := make([]peer.ID, 0, len(addresses))
	for _, raw := range addresses {
		address, err := multiaddr.NewMultiaddr(raw)
		if err != nil {
			return nil, fmt.Errorf("parse bootstrap multiaddr %q: %w", raw, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(address)
		if err != nil {
			return nil, fmt.Errorf("bootstrap multiaddr %q must contain /p2p/PeerID: %w", raw, err)
		}
		stored, exists := byPeer[info.ID]
		if !exists {
			if len(order) == MaxPeers {
				return nil, fmt.Errorf("bootstrap peer count exceeds %d", MaxPeers)
			}
			stored = &peer.AddrInfo{ID: info.ID}
			byPeer[info.ID] = stored
			order = append(order, info.ID)
		}
		for _, address := range info.Addrs {
			duplicate := false
			for _, existing := range stored.Addrs {
				if existing.Equal(address) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				stored.Addrs = append(stored.Addrs, address)
			}
		}
	}
	result := make([]peer.AddrInfo, 0, len(order))
	for _, id := range order {
		result = append(result, *byPeer[id])
	}
	return result, nil
}

// Connect succeeds when any configured expected PeerID authenticates within the bounded attempt.
func Connect(ctx context.Context, h host.Host, peers []peer.AddrInfo, logger *slog.Logger) error {
	return ConnectWithPolicy(ctx, h, peers, logger, DefaultConnectPolicy())
}

func ConnectWithPolicy(ctx context.Context, h host.Host, peers []peer.AddrInfo, logger *slog.Logger, policy ConnectPolicy) error {
	if ctx == nil || h == nil || logger == nil || policy.MaxConcurrency < 1 ||
		policy.MaxConcurrency > MaxPeers || policy.PeerTimeout <= 0 || policy.OverallTimeout <= 0 {
		return fmt.Errorf("invalid bootstrap connect policy")
	}
	if len(peers) == 0 {
		return nil
	}
	unique := make([]peer.AddrInfo, 0, min(len(peers), MaxPeers))
	seen := make(map[peer.ID]struct{}, min(len(peers), MaxPeers))
	for _, info := range peers {
		if info.ID == "" || info.ID.Validate() != nil {
			continue
		}
		if _, duplicate := seen[info.ID]; duplicate {
			continue
		}
		if len(unique) == MaxPeers {
			return fmt.Errorf("bootstrap peer count exceeds %d", MaxPeers)
		}
		seen[info.ID] = struct{}{}
		unique = append(unique, info)
	}
	if len(unique) == 0 {
		return fmt.Errorf("no valid bootstrap peers")
	}

	runContext, cancel := context.WithTimeout(ctx, policy.OverallTimeout)
	defer cancel()
	tasks := make(chan peer.AddrInfo)
	results := make(chan error, len(unique))
	workers := min(policy.MaxConcurrency, len(unique))
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			for info := range tasks {
				if info.ID == h.ID() {
					results <- fmt.Errorf("bootstrap peer %s cannot be self", info.ID)
					continue
				}
				logger.Info("BOOTSTRAP_CONNECT_START", "remote_peer_id", info.ID)
				peerContext, peerCancel := context.WithTimeout(runContext, policy.PeerTimeout)
				err := h.Connect(peerContext, info)
				peerCancel()
				if err != nil {
					logger.Warn("BOOTSTRAP_CONNECT_FAILED", "remote_peer_id", info.ID, "reason", err)
					results <- fmt.Errorf("connect bootstrap peer %s: %w", info.ID, err)
					continue
				}
				logger.Info("BOOTSTRAP_CONNECT_SUCCESS", "remote_peer_id", info.ID)
				results <- nil
			}
		}()
	}
	go func() {
		defer close(tasks)
		for _, info := range unique {
			select {
			case tasks <- info:
			case <-runContext.Done():
				return
			}
		}
	}()
	go func() {
		wait.Wait()
		close(results)
	}()

	succeeded := 0
	failures := make([]error, 0, len(unique))
	for result := range results {
		if result == nil {
			succeeded++
		} else {
			failures = append(failures, result)
		}
	}
	if succeeded != 0 {
		return nil
	}
	if err := runContext.Err(); err != nil {
		failures = append(failures, err)
	}
	return fmt.Errorf("no bootstrap peer reachable: %w", errors.Join(failures...))
}
