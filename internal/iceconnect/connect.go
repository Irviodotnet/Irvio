// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import (
	"context"
	"fmt"
	"time"

	"github.com/pion/ice/v4"

	"hydra.local/hydra/internal/icesession"
)

// Connect performs exactly one blocking Pion Dial or Accept operation.
func (a *Attempt) Connect(ctx context.Context) (*ConnectedSession, error) {
	if a == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("connect ICE session: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	if a.state != StateRemoteLoaded {
		a.stats.stateMisuseErrors.Add(1)
		state := a.state
		a.mu.Unlock()
		switch state {
		case StatePrepared, StateLoading:
			return nil, ErrRemoteNotLoaded
		case StateClosed:
			return nil, ErrClosed
		default:
			return nil, ErrAlreadyStarted
		}
	}
	a.state = StateConnecting
	a.stats.connectAttemptsStarted.Add(1)
	role := a.binding.LocalRole
	credentials := a.remote.Description.Credentials
	before := a.local.Adapter().Stats().OutboundTransactionsRegistered
	a.mu.Unlock()

	var (
		conn *ice.Conn
		err  error
	)
	switch role {
	case icesession.RoleControlling:
		a.stats.dialAttempts.Add(1)
		conn, err = a.agent.Dial(ctx, credentials.Ufrag, credentials.Pwd)
	case icesession.RoleControlled:
		a.stats.acceptAttempts.Add(1)
		conn, err = a.agent.Accept(ctx, credentials.Ufrag, credentials.Pwd)
	default:
		err = ErrInvalidBinding
	}
	if err != nil {
		return nil, a.fail("connect ICE session", contextFailure(ctx, err, &a.stats))
	}
	result, err := a.snapshotSelectedPair(conn, before)
	if err != nil {
		return nil, a.fail("validate selected ICE pair", err)
	}
	a.mu.Lock()
	if a.state != StateConnecting {
		a.mu.Unlock()
		return nil, a.fail("finish ICE connection", ErrClosed)
	}
	connected := &ConnectedSession{attempt: a, result: result}
	a.conn = conn
	a.connected = connected
	a.state = StateConnected
	a.mu.Unlock()
	a.stats.connected.Add(1)
	if result.Network == icesession.NetworkUDP4 {
		a.stats.selectedUDP4.Add(1)
	} else {
		a.stats.selectedUDP6.Add(1)
	}
	if result.SelectedLocalCandidate.Type == ice.CandidateTypeHost && result.SelectedRemoteCandidate.Type == ice.CandidateTypeHost {
		a.stats.selectedHostHost.Add(1)
	}
	switch result.SelectedRemoteCandidate.Type {
	case ice.CandidateTypeHost:
		a.stats.selectedRemoteHost.Add(1)
	case ice.CandidateTypeServerReflexive:
		a.stats.selectedRemoteSrflx.Add(1)
	case ice.CandidateTypePeerReflexive:
		a.stats.selectedRemotePrflx.Add(1)
	}
	for _, candidate := range result.RemoteCandidates {
		if candidate.Type == ice.CandidateTypePeerReflexive {
			a.stats.peerReflexiveObserved.Add(1)
		}
	}
	return connected, nil
}

func (a *Attempt) snapshotSelectedPair(conn *ice.Conn, before uint64) (Result, error) {
	pair, err := a.agent.GetSelectedCandidatePair()
	if err != nil {
		return Result{}, err
	}
	if pair == nil || pair.Local == nil || pair.Remote == nil {
		return Result{}, fmt.Errorf("%w: Pion returned no selected pair", ErrSelectedPair)
	}
	localCandidate, err := candidateSnapshot(pair.Local, a.config.Validation.AllowLoopback)
	if err != nil {
		return Result{}, fmt.Errorf("%w: local candidate: %v", ErrSelectedPair, err)
	}
	remoteCandidate, err := candidateSnapshot(pair.Remote, a.config.Validation.AllowLoopback)
	if err != nil {
		return Result{}, fmt.Errorf("%w: remote candidate: %v", ErrSelectedPair, err)
	}
	localSignaled, localOK := localCandidate.signaled()
	remoteSignaled, remoteOK := remoteCandidate.signaled()
	if localCandidate.Type != ice.CandidateTypeHost || localCandidate.Network != remoteCandidate.Network ||
		!localOK || !containsCandidate(a.localDesc.Candidates, localSignaled) ||
		(remoteCandidate.Type != ice.CandidateTypePeerReflexive && (!remoteOK || !containsCandidate(a.remote.Description.Candidates, remoteSignaled))) {
		return Result{}, fmt.Errorf("%w: selected pair is not backed by local or remote candidate state", ErrSelectedPair)
	}
	remoteCandidates, dropped, err := a.snapshotRemoteCandidates()
	if err != nil {
		return Result{}, fmt.Errorf("%w: snapshot remote candidates: %v", ErrSelectedPair, err)
	}
	selectedStats, ok := a.agent.GetSelectedCandidatePairStats()
	if !ok {
		return Result{}, fmt.Errorf("%w: selected pair stats unavailable", ErrSelectedPair)
	}
	var fullStats ice.CandidatePairStats
	found := false
	for _, stats := range a.agent.GetCandidatePairsStats() {
		if stats.LocalCandidateID == selectedStats.LocalCandidateID && stats.RemoteCandidateID == selectedStats.RemoteCandidateID {
			fullStats = stats
			found = true
			break
		}
	}
	if !found || !fullStats.Nominated || fullStats.State != ice.CandidatePairStateSucceeded {
		return Result{}, fmt.Errorf("%w: pair is not nominated and succeeded", ErrSelectedPair)
	}
	if fullStats.RequestsSent == 0 || fullStats.ResponsesReceived == 0 {
		return Result{}, fmt.Errorf("%w: missing connectivity-check evidence", ErrSelectedPair)
	}
	if fullStats.PacketsSent != 0 || fullStats.PacketsReceived != 0 || fullStats.BytesSent != 0 || fullStats.BytesReceived != 0 ||
		conn.BytesSent() != 0 || conn.BytesReceived() != 0 {
		return Result{}, fmt.Errorf("%w: unexpected application traffic", ErrSelectedPair)
	}
	after := a.local.Adapter().Stats().OutboundTransactionsRegistered
	if after <= before {
		return Result{}, fmt.Errorf("%w: Irvio adapter observed no outbound connectivity check", ErrSelectedPair)
	}
	result := Result{
		TransportGeneration: a.local.TransportGeneration(), SessionID: a.binding.SessionID, Role: a.binding.LocalRole,
		LocalPeer: a.binding.LocalPeer, RemotePeer: a.binding.RemotePeer,
		SelectedLocalCandidate: localCandidate, SelectedRemoteCandidate: remoteCandidate,
		RemoteCandidates: remoteCandidates, RemoteCandidatesDropped: dropped,
		Network: localCandidate.Network, LocalEndpoint: localCandidate.endpoint(), RemoteEndpoint: remoteCandidate.endpoint(),
		Nominated: fullStats.Nominated, PairState: fullStats.State,
		CurrentRTT:   durationFromSeconds(fullStats.CurrentRoundTripTime),
		RequestsSent: fullStats.RequestsSent, RequestsReceived: fullStats.RequestsReceived,
		ResponsesSent: fullStats.ResponsesSent, ResponsesReceived: fullStats.ResponsesReceived,
		ApplicationPacketsSent: fullStats.PacketsSent, ApplicationPacketsRecv: fullStats.PacketsReceived,
		ApplicationBytesSent: fullStats.BytesSent, ApplicationBytesRecv: fullStats.BytesReceived,
		AdapterRequestsBefore: before, AdapterRequestsAfter: after, ConnectedAt: time.Now(),
	}
	for _, info := range conn.GetCandidatePairsInfo() {
		if info.Nominated && info.State == ice.CandidatePairStateSucceeded &&
			info.LocalCandidateType == pair.Local.Type() && info.RemoteCandidateType == pair.Remote.Type() {
			result.PairID = info.ID
			break
		}
	}
	return result, nil
}

func (a *Attempt) snapshotRemoteCandidates() ([]CandidateSnapshot, int, error) {
	candidates, err := a.agent.GetRemoteCandidates()
	if err != nil {
		return nil, 0, err
	}
	limit := len(candidates)
	if limit > a.config.MaxRemoteSnapshots {
		limit = a.config.MaxRemoteSnapshots
	}
	result := make([]CandidateSnapshot, 0, limit)
	for _, candidate := range candidates[:limit] {
		snapshot, err := candidateSnapshot(candidate, a.config.Validation.AllowLoopback)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, snapshot)
	}
	return result, len(candidates) - limit, nil
}

func containsCandidate(candidates []icesession.Candidate, wanted icesession.Candidate) bool {
	for _, candidate := range candidates {
		if candidate == wanted {
			return true
		}
	}
	return false
}
