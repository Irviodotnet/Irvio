// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import (
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"

	"hydra.local/hydra/internal/icesession"
)

// State is the one-shot connectivity lifecycle state.
type State uint8

const (
	StatePrepared State = iota + 1
	StateLoading
	StateRemoteLoaded
	StateConnecting
	StateConnected
	StateFailed
	StateClosed
)

// Binding is the authenticated M5b session identity from the local viewpoint.
type Binding struct {
	SessionID  icesession.SessionID
	Initiator  peer.ID
	Responder  peer.ID
	LocalPeer  peer.ID
	RemotePeer peer.ID
	Purpose    icesession.SessionPurpose
	LocalRole  icesession.Role
	RemoteRole icesession.Role
}

// RemoteState binds one immutable remote description to authenticated metadata.
type RemoteState struct {
	SessionID   icesession.SessionID
	PeerID      peer.ID
	Role        icesession.Role
	Description icesession.Description
}

// Result is an immutable snapshot of one selected direct ICE pair.
type Result struct {
	TransportGeneration     uint64
	SessionID               icesession.SessionID
	Role                    icesession.Role
	LocalPeer               peer.ID
	RemotePeer              peer.ID
	SelectedLocalCandidate  CandidateSnapshot
	SelectedRemoteCandidate CandidateSnapshot
	RemoteCandidates        []CandidateSnapshot
	RemoteCandidatesDropped int
	Network                 icesession.NetworkType
	LocalEndpoint           netip.AddrPort
	RemoteEndpoint          netip.AddrPort
	PairID                  uint64
	Nominated               bool
	PairState               ice.CandidatePairState
	CurrentRTT              time.Duration
	RequestsSent            uint64
	RequestsReceived        uint64
	ResponsesSent           uint64
	ResponsesReceived       uint64
	ApplicationPacketsSent  uint32
	ApplicationPacketsRecv  uint32
	ApplicationBytesSent    uint64
	ApplicationBytesRecv    uint64
	AdapterRequestsBefore   uint64
	AdapterRequestsAfter    uint64
	ConnectedAt             time.Time
}

// Diagnostics is a bounded copy of Pion callback observations.
type Diagnostics struct {
	ConnectionStates   []ice.ConnectionState
	SelectedPairEvents uint64
}
