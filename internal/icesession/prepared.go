// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesession

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
)

// PreparedSession owns one Agent and description while borrowing the generation's adapter.
type PreparedSession struct {
	agent               *ice.Agent
	adapter             *iceadapter.Adapter
	release             func()
	description         Description
	transportGeneration uint64
	closeOnce           sync.Once
	closeErr            error
	lifecycle           atomic.Uint32
	bindingMu           sync.Mutex
	binding             ConnectivityBinding
	bound               bool
}

// TransportGeneration returns the immutable local transport generation ID.
func (s *PreparedSession) TransportGeneration() uint64 {
	if s == nil {
		return 0
	}
	return s.transportGeneration
}

// BindConnectivity records the authenticated signaling exchange exactly once.
func (s *PreparedSession) BindConnectivity(binding ConnectivityBinding) error {
	if s == nil {
		return fmt.Errorf("bind ICE connectivity: nil prepared session")
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("bind ICE connectivity: %w", err)
	}
	s.bindingMu.Lock()
	defer s.bindingMu.Unlock()
	if s.lifecycle.Load() == preparedClosed {
		return ErrPreparedSessionClosed
	}
	if s.bound {
		return ErrSessionAlreadyBound
	}
	s.binding = binding
	s.bound = true
	return nil
}

// ConnectivityBinding returns the authenticated signaling binding, if set.
func (s *PreparedSession) ConnectivityBinding() (ConnectivityBinding, bool) {
	if s == nil {
		return ConnectivityBinding{}, false
	}
	s.bindingMu.Lock()
	defer s.bindingMu.Unlock()
	return s.binding, s.bound
}

const (
	preparedAvailable uint32 = iota
	preparedClaimed
	preparedClosed
)

// ClaimConnectivity reserves one generation; claimed or closed sessions cannot be reused.
func (s *PreparedSession) ClaimConnectivity() error {
	if s == nil {
		return fmt.Errorf("claim ICE connectivity: nil prepared session")
	}
	if s.lifecycle.CompareAndSwap(preparedAvailable, preparedClaimed) {
		return nil
	}
	if s.lifecycle.Load() == preparedClosed {
		return ErrPreparedSessionClosed
	}
	return ErrConnectivityClaimed
}

// Agent returns the live Pion Agent for future M5c ownership transfer.
func (s *PreparedSession) Agent() *ice.Agent {
	if s == nil {
		return nil
	}
	return s.agent
}

// Adapter returns the borrowed shared Irvio ICE adapter for diagnostics.
func (s *PreparedSession) Adapter() *iceadapter.Adapter {
	if s == nil {
		return nil
	}
	return s.adapter
}

// LocalDescription returns an independent copy of the non-trickle description.
func (s *PreparedSession) LocalDescription() Description {
	if s == nil {
		return Description{}
	}
	return s.description.Clone()
}

// Close releases only this Pion Agent and its shared-engine registration.
func (s *PreparedSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.lifecycle.Store(preparedClosed)
		var agentErr error
		if s.agent != nil {
			agentErr = s.agent.GracefulClose()
		}
		if s.release != nil {
			s.release()
		}
		s.closeErr = errors.Join(agentErr)
	})
	return s.closeErr
}
