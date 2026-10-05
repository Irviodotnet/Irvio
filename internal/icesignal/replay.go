// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesignal

import (
	"container/list"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/icesession"
)

type replayKey struct {
	peer peer.ID
	id   icesession.SessionID
}

type replayEntry struct {
	key       replayKey
	expiresAt time.Time
	element   *list.Element
}

type replayRegistry struct {
	mu      sync.Mutex
	limit   int
	ttl     time.Duration
	entries map[replayKey]*replayEntry
	order   list.List
}

func newReplayRegistry(limit int, ttl time.Duration) *replayRegistry {
	return &replayRegistry{limit: limit, ttl: ttl, entries: make(map[replayKey]*replayEntry, limit)}
}

func (r *replayRegistry) claim(remote peer.ID, id icesession.SessionID, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked(now)
	key := replayKey{peer: remote, id: id}
	if _, exists := r.entries[key]; exists {
		return ErrReplay
	}
	if len(r.entries) >= r.limit {
		return ErrBusy
	}
	entry := &replayEntry{key: key, expiresAt: now.Add(r.ttl)}
	entry.element = r.order.PushBack(entry)
	r.entries[key] = entry
	return nil
}

func (r *replayRegistry) cleanupLocked(now time.Time) {
	for count := 0; count < 32; count++ {
		oldest := r.order.Front()
		if oldest == nil {
			return
		}
		entry := oldest.Value.(*replayEntry)
		if now.Before(entry.expiresAt) {
			return
		}
		r.removeLocked(entry)
	}
}

func (r *replayRegistry) removeLocked(entry *replayEntry) {
	delete(r.entries, entry.key)
	r.order.Remove(entry.element)
}

func (r *replayRegistry) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked(time.Now())
	return len(r.entries)
}
