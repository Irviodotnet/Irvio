// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import "fmt"

// ConnectedSession retains Pion lifecycle without exposing ice.Conn for user traffic.
type ConnectedSession struct {
	attempt *Attempt
	result  Result
}

// Result returns the immutable selected-pair snapshot.
func (s *ConnectedSession) Result() Result {
	if s == nil {
		return Result{}
	}
	result := s.result
	result.RemoteCandidates = append([]CandidateSnapshot(nil), s.result.RemoteCandidates...)
	return result
}

// CurrentResult snapshots the selected pair without changing the initial immutable Result.
func (s *ConnectedSession) CurrentResult() (Result, error) {
	if s == nil || s.attempt == nil {
		return Result{}, ErrClosed
	}
	s.attempt.mu.Lock()
	if s.attempt.state != StateConnected || s.attempt.conn == nil {
		state := s.attempt.state
		s.attempt.mu.Unlock()
		return Result{}, fmt.Errorf("snapshot current ICE pair in state %d: %w", state, ErrClosed)
	}
	conn := s.attempt.conn
	before := s.result.AdapterRequestsBefore
	s.attempt.mu.Unlock()
	return s.attempt.snapshotSelectedPair(conn, before)
}

// Close releases the PreparedSession, not the engine's shared Control lease.
func (s *ConnectedSession) Close() error {
	if s == nil || s.attempt == nil {
		return nil
	}
	return s.attempt.Close()
}
