// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesession

import (
	"context"
	"fmt"
	"sync"
)

// ControlPhase identifies the exclusive high-level owner of Mux.Control.
type ControlPhase uint8

const (
	ControlPhaseIdle ControlPhase = iota
	ControlPhaseSTUN
	ControlPhaseICE
)

// PhaseManager coordinates one high-level Control consumer without owning mux.
type PhaseManager struct {
	mu         sync.Mutex
	phase      ControlPhase
	generation uint64
	closed     bool
}

// Lease proves exclusive ownership of one Control phase.
type Lease struct {
	manager    *PhaseManager
	phase      ControlPhase
	generation uint64
	once       sync.Once
}

// NewPhaseManager creates an idle phase manager.
func NewPhaseManager() *PhaseManager { return &PhaseManager{} }

// Acquire obtains phase immediately or returns ErrControlBusy.
func (m *PhaseManager) Acquire(ctx context.Context, phase ControlPhase) (*Lease, error) {
	if ctx == nil {
		return nil, fmt.Errorf("acquire Control phase: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if phase != ControlPhaseSTUN && phase != ControlPhaseICE {
		return nil, ErrWrongPhase
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if m.phase != ControlPhaseIdle {
		return nil, ErrControlBusy
	}
	m.generation++
	m.phase = phase
	return &Lease{manager: m, phase: phase, generation: m.generation}, nil
}

// Phase returns the currently owned phase.
func (m *PhaseManager) Phase() ControlPhase {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.phase
}

// Close prevents future acquisitions. An existing lease remains releasable.
func (m *PhaseManager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}

// Phase returns the phase owned by this lease.
func (l *Lease) Phase() ControlPhase {
	if l == nil {
		return ControlPhaseIdle
	}
	return l.phase
}

// Release relinquishes ownership once. Stale and double releases are safe.
func (l *Lease) Release() {
	if l == nil || l.manager == nil {
		return
	}
	l.once.Do(func() {
		l.manager.mu.Lock()
		if l.manager.phase == l.phase && l.manager.generation == l.generation {
			l.manager.phase = ControlPhaseIdle
		}
		l.manager.mu.Unlock()
	})
}

func (l *Lease) require(phase ControlPhase) error {
	if l == nil || l.manager == nil || l.phase != phase {
		return ErrWrongPhase
	}
	l.manager.mu.Lock()
	defer l.manager.mu.Unlock()
	if l.manager.phase != phase || l.manager.generation != l.generation {
		return ErrWrongPhase
	}
	return nil
}

// Require verifies that this live lease still owns the requested phase.
func (l *Lease) Require(phase ControlPhase) error { return l.require(phase) }
