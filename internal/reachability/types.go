// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package reachability proves fresh inbound authenticated control access, not outbound health.
package reachability

import (
	"errors"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

const (
	ProtocolVersion      uint32 = 1
	ProtocolID                  = "/hydra/reachability/1.0.0"
	ChallengeProtocolID         = "/hydra/reachability/challenge/1.0.0"
	AttemptIDSize               = 16
	NonceSize                   = 32
	MaxFrameSize                = 4096
	DefaultQuorum               = 2
	DefaultVerifierLimit        = 3
	DefaultMaxActive            = 32
	DefaultPerPeer              = 1
	DefaultPerIP                = 4
	DefaultAttemptTTL           = 10 * time.Second
	DefaultProofTTL             = 10 * time.Minute
)

var (
	ErrInvalidConfig    = errors.New("invalid reachability configuration")
	ErrInvalidMessage   = errors.New("invalid reachability message")
	ErrWrongPeer        = errors.New("authenticated reachability peer mismatch")
	ErrRateLimited      = errors.New("reachability verification rate limited")
	ErrUnreachable      = errors.New("public control endpoint is unreachable")
	ErrIdentityMismatch = errors.New("public endpoint has another Irvio identity")
	ErrReplay           = errors.New("reachability attempt replayed")
	ErrExpired          = errors.New("reachability proof expired")
	ErrInsufficient     = errors.New("reachability verifier quorum not met")
	ErrStaleGeneration  = errors.New("stale public reachability generation")
	ErrClosed           = errors.New("reachability service is closed")
)

type AttemptID [AttemptIDSize]byte

func (id AttemptID) IsZero() bool { return id == AttemptID{} }

type Endpoint struct {
	Family            irviov1.ReachabilityFamily
	Address           netip.AddrPort
	NetworkGeneration uint64
	PublicEpoch       uint64
}

type Verifier struct {
	PeerID peer.ID
}

type Proof struct {
	Endpoint     Endpoint
	Subject      peer.ID
	Attestations []*irviov1.SignedReachabilityAttestation
	ExpiresAt    time.Time
}

type ProofPolicy struct {
	Quorum int
	Now    func() time.Time
}

func DefaultProofPolicy() ProofPolicy {
	return ProofPolicy{Quorum: DefaultQuorum, Now: time.Now}
}

type ObservedIPPolicy struct {
	AllowDocumentation bool
	AllowPrivate       bool
	// AllowLoopback is test-only and must remain false in production.
	AllowLoopback bool
}
