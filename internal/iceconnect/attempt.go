// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"

	"hydra.local/hydra/internal/icesession"
)

// Attempt owns the one connectivity generation claimed from a PreparedSession.
type Attempt struct {
	local     *icesession.PreparedSession
	agent     *ice.Agent
	binding   Binding
	remote    RemoteState
	localDesc icesession.Description
	config    Config
	stats     counters

	mu                 sync.Mutex
	state              State
	conn               *ice.Conn
	connected          *ConnectedSession
	connectionStates   []ice.ConnectionState
	selectedPairEvents uint64
	stateEvents        chan ice.ConnectionState
	closeOnce          sync.Once
	closeErr           error
}

// New snapshots authenticated signaling and claims one connectivity generation.
func New(local *icesession.PreparedSession, binding Binding, remote RemoteState, config Config) (*Attempt, error) {
	if local == nil || local.Agent() == nil || local.Adapter() == nil {
		return nil, fmt.Errorf("create ICE connectivity attempt: nil prepared session")
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("create ICE connectivity attempt: %w", err)
	}
	localDescription, err := icesession.ValidateDescription(local.LocalDescription(), config.Validation)
	if err != nil {
		return nil, fmt.Errorf("create ICE connectivity attempt: validate local description: %w", err)
	}
	remoteDescription, err := icesession.ValidateDescription(remote.Description, config.Validation)
	if err != nil {
		return nil, fmt.Errorf("create ICE connectivity attempt: validate remote description: %w", err)
	}
	remote.Description = remoteDescription.Clone()
	if err := validateBinding(binding, remote); err != nil {
		return nil, fmt.Errorf("create ICE connectivity attempt: %w", err)
	}
	preparedBinding, ok := local.ConnectivityBinding()
	if !ok {
		return nil, fmt.Errorf("create ICE connectivity attempt: %w", icesession.ErrSessionNotBound)
	}
	wantBinding := icesession.ConnectivityBinding{
		SessionID: binding.SessionID, Initiator: binding.Initiator, Responder: binding.Responder,
		LocalPeer: binding.LocalPeer, RemotePeer: binding.RemotePeer, Purpose: binding.Purpose,
		LocalRole: binding.LocalRole, RemoteRole: binding.RemoteRole,
	}
	if preparedBinding != wantBinding {
		return nil, fmt.Errorf("create ICE connectivity attempt: %w: prepared session mismatch", ErrInvalidBinding)
	}
	if !hasCommonFamily(localDescription, remoteDescription) {
		return nil, ErrNoUsableFamily
	}
	if err := local.ClaimConnectivity(); err != nil {
		return nil, fmt.Errorf("create ICE connectivity attempt: %w", err)
	}
	attempt := &Attempt{
		local: local, agent: local.Agent(), binding: binding, remote: remote,
		localDesc: localDescription.Clone(), config: config, state: StatePrepared,
		stateEvents: make(chan ice.ConnectionState, 1),
	}
	attempt.stats.attemptsCreated.Store(1)
	if err := attempt.agent.OnConnectionStateChange(attempt.recordConnectionState); err != nil {
		_ = local.Close()
		return nil, fmt.Errorf("register ICE connection-state callback: %w", err)
	}
	if err := attempt.agent.OnSelectedCandidatePairChange(attempt.recordSelectedPair); err != nil {
		_ = local.Close()
		return nil, fmt.Errorf("register ICE selected-pair callback: %w", err)
	}
	return attempt, nil
}

// ConnectionStateChanges coalesces states rather than blocking Pion callbacks.
func (a *Attempt) ConnectionStateChanges() <-chan ice.ConnectionState {
	if a == nil {
		return nil
	}
	return a.stateEvents
}

func validateBinding(binding Binding, remote RemoteState) error {
	if binding.SessionID == (icesession.SessionID{}) || remote.SessionID != binding.SessionID {
		return fmt.Errorf("%w: SessionID mismatch", ErrInvalidBinding)
	}
	ids := []peer.ID{binding.Initiator, binding.Responder, binding.LocalPeer, binding.RemotePeer, remote.PeerID}
	for _, id := range ids {
		if id == "" || id.Validate() != nil {
			return fmt.Errorf("%w: invalid PeerID", ErrInvalidBinding)
		}
	}
	if binding.Initiator == binding.Responder || binding.LocalPeer == binding.RemotePeer ||
		remote.PeerID != binding.RemotePeer || binding.Purpose != icesession.PurposeExit || remote.Role != binding.RemoteRole {
		return ErrInvalidBinding
	}
	if binding.LocalPeer == binding.Initiator && binding.RemotePeer == binding.Responder {
		if binding.LocalRole != icesession.RoleControlling || binding.RemoteRole != icesession.RoleControlled {
			return fmt.Errorf("%w: initiator must be controlling", ErrInvalidBinding)
		}
		return nil
	}
	if binding.LocalPeer == binding.Responder && binding.RemotePeer == binding.Initiator {
		if binding.LocalRole != icesession.RoleControlled || binding.RemoteRole != icesession.RoleControlling {
			return fmt.Errorf("%w: responder must be controlled", ErrInvalidBinding)
		}
		return nil
	}
	return fmt.Errorf("%w: local and remote peers do not match initiator/responder", ErrInvalidBinding)
}

func hasCommonFamily(local, remote icesession.Description) bool {
	localFamilies := make(map[icesession.NetworkType]struct{}, 2)
	for _, candidate := range local.Candidates {
		localFamilies[candidate.Network] = struct{}{}
	}
	for _, candidate := range remote.Candidates {
		if _, ok := localFamilies[candidate.Network]; ok {
			return true
		}
	}
	return false
}

// State returns the current one-shot lifecycle state.
func (a *Attempt) State() State {
	if a == nil {
		return StateClosed
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// RemoteDescription returns a defensive copy of the frozen remote description.
func (a *Attempt) RemoteDescription() icesession.Description {
	if a == nil {
		return icesession.Description{}
	}
	return a.remote.Description.Clone()
}

// Stats returns a nonblocking race-safe activity snapshot.
func (a *Attempt) Stats() Stats {
	if a == nil {
		return Stats{}
	}
	return a.stats.snapshot()
}

// Diagnostics returns bounded Pion callback observations.
func (a *Attempt) Diagnostics() Diagnostics {
	if a == nil {
		return Diagnostics{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return Diagnostics{
		ConnectionStates:   append([]ice.ConnectionState(nil), a.connectionStates...),
		SelectedPairEvents: a.selectedPairEvents,
	}
}

func (a *Attempt) recordConnectionState(state ice.ConnectionState) {
	a.mu.Lock()
	if len(a.connectionStates) < a.config.MaxStateTransitions {
		a.connectionStates = append(a.connectionStates, state)
	}
	a.mu.Unlock()
	select {
	case a.stateEvents <- state:
		return
	default:
	}
	select {
	case <-a.stateEvents:
	default:
	}
	select {
	case a.stateEvents <- state:
	default:
	}
}

func (a *Attempt) recordSelectedPair(ice.Candidate, ice.Candidate) {
	a.mu.Lock()
	a.selectedPairEvents++
	a.mu.Unlock()
}

func (a *Attempt) fail(operation string, err error) error {
	if err == nil {
		err = ErrConnectivityFailed
	}
	a.mu.Lock()
	if a.state != StateClosed && a.state != StateFailed {
		a.state = StateFailed
		a.stats.failed.Add(1)
	}
	a.mu.Unlock()
	return fmt.Errorf("%s: %w", operation, errors.Join(err, a.local.Close()))
}

// Close terminates this Agent but leaves shared adapter, lease, and mux ownership with ICEEngine.
func (a *Attempt) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.state = StateClosed
		a.mu.Unlock()
		a.closeErr = a.local.Close()
	})
	return a.closeErr
}

func contextFailure(ctx context.Context, err error, stats *counters) error {
	if ctx == nil || ctx.Err() == nil {
		return err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		stats.timeouts.Add(1)
	} else {
		stats.canceled.Add(1)
	}
	return errors.Join(ctx.Err(), err)
}

func durationFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
