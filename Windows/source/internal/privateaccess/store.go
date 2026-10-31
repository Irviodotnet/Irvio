// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
)

type Store struct {
	mu sync.Mutex
	storeStorage
	state  State
	closed bool
	failed bool
}

func decode(data []byte) (State, error) {
	if len(data) > MaxBytes {
		return State{}, ErrInvalid
	}
	validator := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueJSON(validator, 0); err != nil {
		return State{}, ErrInvalid
	}
	if _, err := validator.Token(); err != io.EOF {
		return State{}, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, ErrInvalid
	}
	if err := state.validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

func uniqueJSON(decoder *json.Decoder, depth int) error {
	if depth > 8 {
		return ErrInvalid
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] || !schemaKey(name) {
				return ErrInvalid
			}
			seen[name] = true
			if err := uniqueJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := uniqueJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return ErrInvalid
	}
	_, err = decoder.Token()
	return err
}

func schemaKey(name string) bool {
	switch name {
	case "version", "host_peer_id", "locator", "revision", "targets", "members", "invitations", "enrollments", "link",
		"peer_id", "name", "allowed", "id", "secret", "created_at", "expires_at", "client", "state", "auto_approve":
		return true
	}
	return false
}

func (s *Store) Snapshot() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.failed {
		return State{}, ErrClosed
	}
	return s.state.clone(), nil
}

// GrantRevision avoids copying invitations or secrets into the admission path.
func (s *Store) GrantRevision(client peer.ID) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.failed {
		return 0, ErrClosed
	}
	for _, member := range s.state.Members {
		if member.PeerID == client.String() && member.Allowed {
			return member.Revision, nil
		}
	}
	return 0, nil
}

func (s *Store) update(change func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.failed {
		return ErrClosed
	}
	next := s.state.clone()
	next.Revision++
	if err := change(&next); err != nil {
		return err
	}
	if err := next.validate(); err != nil {
		return err
	}
	if err := s.persist(next); err != nil {
		s.failed = true
		return err
	}
	s.state = next
	return nil
}
