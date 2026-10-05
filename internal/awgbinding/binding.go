// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package awgbinding validates and fences signed Irvio-to-AWG key bindings.
package awgbinding

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/awg"
	"hydra.local/hydra/internal/protocol"
)

// Binding is an authenticated public AWG key claim from the expected PeerID.
type Binding struct {
	PeerID                  peer.ID
	PublicKey               awg.Key
	KeyEpoch                uint64
	AnnouncementEpoch       [protocol.SessionEpochSize]byte
	AnnouncementSequence    uint64
	AnnouncementIncarnation uint64
	DescriptorID            [32]byte
	ExpiresAt               time.Time
}

// Verify authenticates the signed announcement and extracts its AWG binding.
func Verify(envelope []byte, expected peer.ID, now time.Time) (Binding, error) {
	record, err := protocol.VerifySignedAnnouncementRecord(envelope, expected, now)
	if err != nil {
		return Binding{}, err
	}
	announcement := record.Announcement
	if len(announcement.AwgPublicKey) != len(awg.Key{}) || announcement.AwgKeyEpoch == 0 {
		return Binding{}, ErrMissingBinding
	}
	var result Binding
	result.PeerID = expected
	copy(result.PublicKey[:], announcement.AwgPublicKey)
	copy(result.AnnouncementEpoch[:], announcement.SessionEpoch)
	result.KeyEpoch = announcement.AwgKeyEpoch
	result.AnnouncementIncarnation = announcement.Incarnation
	result.AnnouncementSequence = announcement.Sequence
	result.DescriptorID = record.DescriptorID
	result.ExpiresAt = time.Unix(announcement.ExpiresAtUnix, 0).UTC()
	if err := result.Validate(now); err != nil {
		return Binding{}, err
	}
	return result, nil
}

// Validate checks binding structure; unsigned input never proves identity.
func (b Binding) Validate(now time.Time) error {
	if b.PeerID == "" || b.PeerID.Validate() != nil || b.PublicKey == (awg.Key{}) || b.KeyEpoch == 0 ||
		b.AnnouncementEpoch == ([protocol.SessionEpochSize]byte{}) || b.AnnouncementIncarnation == 0 ||
		b.AnnouncementSequence == 0 || b.DescriptorID == ([32]byte{}) ||
		b.ExpiresAt.IsZero() || !b.ExpiresAt.After(now) {
		return ErrInvalidBinding
	}
	return nil
}

// Registry fences attempts against each peer's latest verified binding.
type Registry struct {
	mu       sync.RWMutex
	bindings map[peer.ID]Binding
}

// NewRegistry creates a process-local fence; PeerDirectory owns persistent rollback protection.
func NewRegistry() *Registry {
	return &Registry{bindings: make(map[peer.ID]Binding)}
}

// Commit rejects rollback, conflicting descriptor versions, and key changes without a newer epoch.
func (r *Registry) Commit(binding Binding, now time.Time) error {
	if r == nil {
		return ErrInvalidBinding
	}
	if err := binding.Validate(now); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.bindings[binding.PeerID]
	if !exists {
		r.bindings[binding.PeerID] = binding
		return nil
	}
	if binding.KeyEpoch < current.KeyEpoch {
		return ErrRollback
	}
	if binding.KeyEpoch == current.KeyEpoch && binding.PublicKey != current.PublicKey {
		return ErrConflict
	}
	switch {
	case binding.AnnouncementIncarnation < current.AnnouncementIncarnation:
		return ErrRollback
	case binding.AnnouncementIncarnation > current.AnnouncementIncarnation:
	case binding.AnnouncementSequence < current.AnnouncementSequence:
		return ErrRollback
	case binding.AnnouncementSequence == current.AnnouncementSequence:
		if binding.DescriptorID != current.DescriptorID {
			return ErrConflict
		}
		return nil
	}
	r.bindings[binding.PeerID] = binding
	return nil
}

// AuthorizePromotion rejects results using a superseded binding.
func (r *Registry) AuthorizePromotion(remote peer.ID, publicKey awg.Key, keyEpoch uint64, now time.Time) error {
	if r == nil || remote == "" || publicKey == (awg.Key{}) || keyEpoch == 0 {
		return ErrInvalidBinding
	}
	r.mu.RLock()
	binding, exists := r.bindings[remote]
	r.mu.RUnlock()
	if !exists {
		return ErrMissingBinding
	}
	if err := binding.Validate(now); err != nil {
		return err
	}
	if binding.KeyEpoch != keyEpoch || binding.PublicKey != publicKey {
		return fmt.Errorf("AWG binding promotion rejected: %w", ErrStaleAttempt)
	}
	return nil
}

// Current returns a copy of the current verified public binding.
func (r *Registry) Current(remote peer.ID) (Binding, bool) {
	if r == nil {
		return Binding{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	binding, exists := r.bindings[remote]
	return binding, exists
}

// Errors are intentionally typed so runtime policy never parses log strings.
var (
	ErrInvalidBinding = errors.New("invalid AWG key binding")
	ErrMissingBinding = errors.New("missing authenticated AWG key binding")
	ErrRollback       = errors.New("AWG key binding rollback")
	ErrConflict       = errors.New("conflicting AWG key at the same epoch")
	ErrStaleAttempt   = errors.New("stale AWG key connection attempt")
)
