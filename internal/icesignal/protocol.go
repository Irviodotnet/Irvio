// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package icesignal exchanges authenticated non-trickle descriptions without starting checks.
package icesignal

import (
	"errors"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"

	"hydra.local/hydra/internal/icesession"
)

const (
	// ProtocolVersion is independent of the linked Pion version.
	ProtocolVersion uint32 = 1
	// ProtocolID is the authenticated one-off ICE signaling stream.
	ProtocolID libprotocol.ID = "/hydra/ice/1.0.0"
	// MaxFrameSize bounds every protobuf request and response.
	MaxFrameSize         = 32 * 1024
	maxErrorMessageBytes = 128
)

var (
	ErrClosed         = errors.New("ICE signaling is closed")
	ErrInvalidMessage = errors.New("invalid ICE signaling message")
	ErrWrongPeer      = errors.New("ICE signaling peer mismatch")
	ErrReplay         = errors.New("ICE signaling session replay")
	ErrBusy           = errors.New("ICE signaling capacity is busy")
	ErrUnauthorized   = errors.New("ICE signaling unauthorized")
	ErrRemote         = errors.New("remote ICE signaling error")
)

// Offer is a validated initiator description.
type Offer struct {
	SessionID   icesession.SessionID
	Initiator   peer.ID
	Responder   peer.ID
	Purpose     icesession.SessionPurpose
	Role        icesession.Role
	Description icesession.Description
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// Answer is a validated responder description.
type Answer struct {
	SessionID   icesession.SessionID
	Initiator   peer.ID
	Responder   peer.ID
	Purpose     icesession.SessionPurpose
	Role        icesession.Role
	Description icesession.Description
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// ExchangeResult retains both the session binding and validated remote state.
type ExchangeResult struct {
	SessionID         icesession.SessionID
	Initiator         peer.ID
	Responder         peer.ID
	RemotePeer        peer.ID
	RemoteDescription icesession.Description
	Purpose           icesession.SessionPurpose
	LocalRole         icesession.Role
	RemoteRole        icesession.Role
}

// AcceptedSession transfers responder PreparedSession ownership to the caller.
type AcceptedSession struct {
	SessionID         icesession.SessionID
	Initiator         peer.ID
	Responder         peer.ID
	RemotePeer        peer.ID
	Local             *icesession.PreparedSession
	RemoteDescription icesession.Description
	Purpose           icesession.SessionPurpose
	LocalRole         icesession.Role
	RemoteRole        icesession.Role
}

// RemoteError is a bounded protocol error without internal details.
type RemoteError struct {
	Code    ErrorCode
	Message string
}

func (e *RemoteError) Error() string { return ErrRemote.Error() + ": " + e.Message }
func (e *RemoteError) Unwrap() error { return ErrRemote }

// ErrorCode is a stable Irvio signaling error category.
type ErrorCode uint8

const (
	ErrorInvalidRequest ErrorCode = iota + 1
	ErrorUnsupportedVersion
	ErrorWrongPeer
	ErrorUnauthorized
	ErrorBusy
	ErrorSessionConflict
	ErrorInternal
)
