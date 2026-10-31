// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessionestablish

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type ReplayGuard struct {
	mu      sync.Mutex
	max     int
	ttl     time.Duration
	now     func() time.Time
	entries map[replayKey]*replayEntry
	closed  bool
}

type replayKey struct {
	sender  peer.ID
	attempt AttemptID
	kind    int32
}

type replayEntry struct {
	hash     [HashSize]byte
	response []byte
	err      error
	done     chan struct{}
	expires  time.Time
}

func NewReplayGuard(max int, ttl time.Duration, now func() time.Time) (*ReplayGuard, error) {
	if max < 1 || max > 65_536 || ttl <= 0 || ttl > time.Hour {
		return nil, ErrInvalid
	}
	if now == nil {
		now = time.Now
	}
	return &ReplayGuard{max: max, ttl: ttl, now: now, entries: make(map[replayKey]*replayEntry)}, nil
}

func (g *ReplayGuard) Execute(ctx context.Context, message Verified, operation func(context.Context) ([]byte, error)) ([]byte, error) {
	if ctx == nil || message.Payload == nil || operation == nil {
		return nil, ErrInvalid
	}
	var attempt AttemptID
	copy(attempt[:], message.Payload.AttemptId)
	key := replayKey{sender: message.Signer, attempt: attempt, kind: int32(message.Payload.Kind)}
	now := g.now()

	g.mu.Lock()
	g.expireLocked(now)
	if g.closed {
		g.mu.Unlock()
		return nil, ErrClosed
	}
	if current := g.entries[key]; current != nil {
		if current.hash != message.Hash {
			g.mu.Unlock()
			return nil, ErrConflict
		}
		done := current.done
		g.mu.Unlock()
		select {
		case <-done:
			return append([]byte(nil), current.response...), current.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if len(g.entries) >= g.max {
		g.mu.Unlock()
		return nil, ErrCapacity
	}
	entry := &replayEntry{hash: message.Hash, done: make(chan struct{}), expires: now.Add(g.ttl)}
	g.entries[key] = entry
	g.mu.Unlock()

	response, operationErr := operation(ctx)
	g.mu.Lock()
	entry.response = append([]byte(nil), response...)
	entry.err = operationErr
	close(entry.done)
	g.mu.Unlock()
	return response, operationErr
}

func (g *ReplayGuard) Size() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.expireLocked(g.now())
	return len(g.entries)
}

func (g *ReplayGuard) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

func (g *ReplayGuard) expireLocked(now time.Time) {
	for key, entry := range g.entries {
		select {
		case <-entry.done:
			if !entry.expires.After(now) {
				delete(g.entries, key)
			}
		default:
		}
	}
}

func SameAttempt(a, b Verified) bool {
	return a.Payload != nil && b.Payload != nil &&
		bytes.Equal(a.Payload.AttemptId, b.Payload.AttemptId) &&
		bytes.Equal(a.Payload.OwnerToken, b.Payload.OwnerToken)
}
