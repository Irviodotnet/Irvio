// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package connsupervisor

import (
	"errors"
	"fmt"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/icesession"
)

// ConnectionKey identifies one long-lived logical peer relationship.
type ConnectionKey struct {
	RemotePeer peer.ID
	Purpose    icesession.SessionPurpose
}

type registryEntry struct {
	generation SessionGeneration
	session    Session
}

// ActiveRegistry bounds sessions and permits newer-generation make-before-break replacement.
type ActiveRegistry struct {
	mu      sync.Mutex
	max     int
	closed  bool
	entries map[ConnectionKey]registryEntry
}

// NewActiveRegistry creates a bounded in-memory active-session index.
func NewActiveRegistry(max int) (*ActiveRegistry, error) {
	if max <= 0 || max > 65536 {
		return nil, fmt.Errorf("active connection limit must be within 1..65536")
	}
	return &ActiveRegistry{max: max, entries: make(map[ConnectionKey]registryEntry, max)}, nil
}

// Promote atomically installs a newer session and closes the superseded one.
func (r *ActiveRegistry) Promote(key ConnectionKey, generation SessionGeneration, session Session) error {
	if key.RemotePeer == "" || key.RemotePeer.Validate() != nil || key.Purpose != icesession.PurposeExit || generation == 0 || session == nil {
		return ErrInvalidSession
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	previous, exists := r.entries[key]
	if exists && generation <= previous.generation {
		r.mu.Unlock()
		_ = session.Close()
		return fmt.Errorf("%w: stale session generation", ErrInvalidSession)
	}
	if !exists && len(r.entries) >= r.max {
		r.mu.Unlock()
		return ErrTooManyConnections
	}
	r.entries[key] = registryEntry{generation: generation, session: session}
	r.mu.Unlock()
	if exists {
		return previous.session.Close()
	}
	return nil
}

// Remove closes the entry only when its generation is still current.
func (r *ActiveRegistry) Remove(key ConnectionKey, generation SessionGeneration) error {
	r.mu.Lock()
	entry, ok := r.entries[key]
	if ok && entry.generation == generation {
		delete(r.entries, key)
	}
	r.mu.Unlock()
	if ok && entry.generation == generation {
		return entry.session.Close()
	}
	return nil
}

// Len reports the bounded number of active logical relationships.
func (r *ActiveRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// Close closes all current sessions and prevents later promotion.
func (r *ActiveRegistry) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	sessions := make([]Session, 0, len(r.entries))
	for _, entry := range r.entries {
		sessions = append(sessions, entry.session)
	}
	clear(r.entries)
	r.mu.Unlock()
	errs := make([]error, 0, len(sessions))
	for _, session := range sessions {
		errs = append(errs, session.Close())
	}
	return errors.Join(errs...)
}
