// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"crypto/subtle"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// LookupPrivate requires the exact locator and identity; discovery knowledge never grants traffic access.
func (r *Registry) LookupPrivate(subject peer.ID, locator [32]byte) (Candidate, error) {
	if !r.private || subject == "" || locator == ([32]byte{}) {
		return Candidate{}, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Candidate{}, ErrClosed
	}
	now := r.config.Now()
	r.refreshLocked(now)
	current := r.registrations[subject]
	if current == nil || subtle.ConstantTimeCompare(current.PrivateLocator[:], locator[:]) != 1 ||
		current.State != RegistrationFresh || !current.Descriptor.Announcement.AcceptingNewSessions ||
		!time.Unix(current.Descriptor.Announcement.ExpiresAtUnix, 0).After(now) ||
		!current.SignedReceipt.Receipt.ExpiresAt.After(now) {
		return Candidate{}, ErrNotFound
	}
	return Candidate{
		Descriptor: cloneDescriptor(current.Descriptor),
		Receipts:   []SignedReceipt{cloneSignedReceipt(current.SignedReceipt)},
		Routes:     []OwnerRoute{cloneOwnerRoute(current.Route)},
	}, nil
}

// PrivateLocators is a bounded publication snapshot excluding stale/non-accepting registrations.
func (r *Registry) PrivateLocators() [][32]byte {
	if !r.private {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	now := r.config.Now()
	r.refreshLocked(now)
	seen := make(map[[32]byte]struct{})
	for _, value := range r.registrations {
		if value.State == RegistrationFresh && value.Descriptor.Announcement.AcceptingNewSessions &&
			time.Unix(value.Descriptor.Announcement.ExpiresAtUnix, 0).After(now) && value.SignedReceipt.Receipt.ExpiresAt.After(now) {
			seen[value.PrivateLocator] = struct{}{}
		}
	}
	result := make([][32]byte, 0, len(seen))
	for locator := range seen {
		result = append(result, locator)
	}
	return result
}
