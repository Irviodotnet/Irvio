// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package sessioncontrol implements authenticated lease maintenance inside AWG.
package sessioncontrol

import (
	"errors"
	"time"

	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

const (
	ProtocolVersion      uint32         = 1
	ProtocolID           libprotocol.ID = "/hydra/session-control/1.0.0"
	MaxFrameSize                        = 16 * 1024
	RequestIDSize                       = 16
	DefaultTimeout                      = 5 * time.Second
	DefaultReplayEntries                = 256
	MaxSignedAWGBinding                 = 8 * 1024
)

var (
	ErrInvalid    = errors.New("invalid session-control message")
	ErrWrongPeer  = errors.New("session-control peer mismatch")
	ErrStale      = errors.New("stale session-control ownership")
	ErrRemote     = errors.New("remote session-control failure")
	ErrClosed     = errors.New("session-control is closed")
	ErrConflict   = errors.New("conflicting session-control replay")
	ErrNoRotation = errors.New("no AWG rotation is pending")
)

type RotationProposal struct {
	SignedBinding []byte
	KeyEpoch      uint64
}
