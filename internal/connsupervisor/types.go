// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package connsupervisor

import (
	"context"
	"errors"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"

	"hydra.local/hydra/internal/icesession"
	"hydra.local/hydra/internal/networkstate"
)

// SessionGeneration identifies one immutable local ICE attempt.
type SessionGeneration uint64

// Reason is a stable lifecycle trigger or failure category.
type Reason uint8

const (
	ReasonInitial Reason = iota + 1
	ReasonICEFailed
	ReasonNetworkChanged
	ReasonTransportClosed
	ReasonSignalingFailed
	ReasonCandidateLoadFailed
	ReasonContextCanceled
	ReasonExplicitRestart
	ReasonGenerationStale
)

// EventType identifies a bounded supervisor event.
type EventType uint8

const (
	EventConnecting EventType = iota + 1
	EventConnected
	EventDisconnected
	EventRecovered
	EventRestartScheduled
	EventRestartStarted
	EventRestartFailed
	EventRestartSucceeded
	EventTransportRefreshRequested
	EventNetworkGenerationChanged
	EventClosed
)

// State describes the logical connection state.
type State uint8

const (
	StateIdle State = iota
	StateConnecting
	StateConnected
	StateDisconnected
	StateClosed
)

// Intent is the stable identity of one logical EXIT relationship.
type Intent struct {
	LocalPeer  peer.ID
	RemotePeer peer.ID
	Purpose    icesession.SessionPurpose
	Role       icesession.Role
}

// Request asks the injected connector for an entirely fresh ICE generation.
type Request struct {
	Intent            Intent
	NetworkGeneration networkstate.Generation
	SessionGeneration SessionGeneration
	Reason            Reason
}

// SessionMetadata contains no ICE password or private key material.
type SessionMetadata struct {
	NetworkGeneration networkstate.Generation
	SessionGeneration SessionGeneration
	SessionID         icesession.SessionID
	RemotePeer        peer.ID
	Purpose           icesession.SessionPurpose
	LocalUfrag        string
}

// Session is a connected ICE generation retained until superseded or closed.
type Session interface {
	Metadata() SessionMetadata
	StateChanges() <-chan ice.ConnectionState
	Close() error
}

// Connector prepares, signals, loads candidates, and dials or accepts one request.
type Connector interface {
	Connect(context.Context, Request) (Session, error)
}

// ConnectorFunc adapts a function to Connector.
type ConnectorFunc func(context.Context, Request) (Session, error)

// Connect invokes f.
func (f ConnectorFunc) Connect(ctx context.Context, request Request) (Session, error) {
	return f(ctx, request)
}

// Event is a copied lifecycle notification without credentials.
type Event struct {
	Type              EventType
	Reason            Reason
	RemotePeer        peer.ID
	NetworkGeneration networkstate.Generation
	SessionGeneration SessionGeneration
	SessionID         icesession.SessionID
	At                time.Time
}

// Snapshot is the current generation-fenced logical state.
type Snapshot struct {
	State                    State
	NetworkGeneration        networkstate.Generation
	DesiredSessionGeneration SessionGeneration
	Active                   SessionMetadata
	HasActive                bool
}

var (
	ErrClosed             = errors.New("connection supervisor is closed")
	ErrAlreadyStarted     = errors.New("connection supervisor already started")
	ErrNotStarted         = errors.New("connection supervisor is not started")
	ErrInvalidSession     = errors.New("connector returned invalid session metadata")
	ErrTooManyConnections = errors.New("too many active logical connections")
)

// FailureError retains a local lifecycle reason and the underlying diagnostic error.
type FailureError struct {
	Reason Reason
	Err    error
}

func (e *FailureError) Error() string {
	if e == nil || e.Err == nil {
		return "connection lifecycle failure"
	}
	return e.Err.Error()
}
func (e *FailureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// WithFailureReason annotates an internal connector failure for supervision.
func WithFailureReason(reason Reason, err error) error {
	if err == nil {
		return nil
	}
	return &FailureError{Reason: reason, Err: err}
}
