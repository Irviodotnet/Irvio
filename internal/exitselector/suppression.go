// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type suppression struct {
	epoch uint64
	until time.Time
	order uint64
}

type suppressions struct {
	entries map[peer.ID]suppression
	next    uint64
	limit   int
}

type routeSuppressionKey struct {
	peer  peer.ID
	epoch uint64
}

type routeSuppressions struct {
	entries map[routeSuppressionKey]suppression
	next    uint64
	limit   int
}

func newSuppressions(limit int) *suppressions {
	return &suppressions{entries: make(map[peer.ID]suppression, limit), limit: limit}
}

func (s *suppressions) add(candidate Candidate, until time.Time) {
	s.next++
	s.entries[candidate.PeerID] = suppression{epoch: candidate.NetworkEpoch, until: until, order: s.next}
	for len(s.entries) > s.limit {
		var oldestPeer peer.ID
		var oldest uint64 = ^uint64(0)
		for id, value := range s.entries {
			if value.order < oldest {
				oldestPeer, oldest = id, value.order
			}
		}
		delete(s.entries, oldestPeer)
	}
}

func (s *suppressions) epochs(now time.Time) map[peer.ID]uint64 {
	result := make(map[peer.ID]uint64, len(s.entries))
	for id, value := range s.entries {
		if !value.until.After(now) {
			delete(s.entries, id)
			continue
		}
		result[id] = value.epoch
	}
	return result
}

func (s *suppressions) clear() { clear(s.entries) }

func newRouteSuppressions(limit int) *routeSuppressions {
	return &routeSuppressions{entries: make(map[routeSuppressionKey]suppression, limit), limit: limit}
}

func (s *routeSuppressions) add(candidate Candidate, until time.Time) {
	if candidate.SignalingRouteEpoch == 0 {
		return
	}
	s.next++
	key := routeSuppressionKey{peer: candidate.PeerID, epoch: candidate.SignalingRouteEpoch}
	s.entries[key] = suppression{until: until, order: s.next}
	for len(s.entries) > s.limit {
		var oldestKey routeSuppressionKey
		var oldest uint64 = ^uint64(0)
		for entry, value := range s.entries {
			if value.order < oldest {
				oldestKey, oldest = entry, value.order
			}
		}
		delete(s.entries, oldestKey)
	}
}

func (s *routeSuppressions) epochs(now time.Time) map[peer.ID]map[uint64]struct{} {
	result := make(map[peer.ID]map[uint64]struct{})
	for key, value := range s.entries {
		if !value.until.After(now) {
			delete(s.entries, key)
			continue
		}
		if result[key.peer] == nil {
			result[key.peer] = make(map[uint64]struct{})
		}
		result[key.peer][key.epoch] = struct{}{}
	}
	return result
}

func (s *routeSuppressions) clear() { clear(s.entries) }
