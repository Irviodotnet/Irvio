// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesession

import (
	"net/netip"
	"time"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

const SessionIDSize = 16

// SessionID is a non-secret, cryptographically random session identifier.
type SessionID [SessionIDSize]byte

// CandidateType is a signaled ICE candidate type.
type CandidateType uint8

const (
	CandidateTypeHost CandidateType = iota + 1
	CandidateTypeServerReflexive
)

// NetworkType is a supported UDP candidate network.
type NetworkType uint8

const (
	NetworkUDP4 NetworkType = iota + 1
	NetworkUDP6
)

// SessionPurpose describes the single Irvio v1 ICE use case.
type SessionPurpose uint8

const (
	PurposeExit SessionPurpose = iota + 1
)

// Role records the future Pion ICE role without starting connectivity checks.
type Role uint8

const (
	RoleControlling Role = iota + 1
	RoleControlled
)

// Credentials holds ephemeral Pion ICE credentials.
type Credentials struct {
	Ufrag string
	Pwd   string
}

// Candidate is an implementation-independent structured ICE candidate.
type Candidate struct {
	Type           CandidateType
	Network        NetworkType
	Address        netip.Addr
	Port           uint16
	RelatedAddress netip.Addr
	RelatedPort    uint16
	Foundation     string
	Priority       uint32
	Component      uint16
}

// Description is one complete non-trickle ICE description.
type Description struct {
	Credentials Credentials
	Candidates  []Candidate
}

// SrflxObservation is an authenticated M3b Binding observation.
type SrflxObservation struct {
	Family         socketmux.AddressFamily
	LocalEndpoint  netip.AddrPort
	MappedEndpoint netip.AddrPort
}

// SessionMetadata binds a description exchange to peers and future ICE roles.
type SessionMetadata struct {
	ID        SessionID
	Purpose   SessionPurpose
	Role      Role
	CreatedAt time.Time
	ExpiresAt time.Time
}

// ValidationConfig bounds credentials and candidate collections.
type ValidationConfig struct {
	MaxCandidates int
	AllowLoopback bool
}

// DefaultValidationConfig returns conservative Irvio v1 limits.
func DefaultValidationConfig() ValidationConfig {
	return ValidationConfig{MaxCandidates: 16}
}

// Clone returns an independent description copy.
func (d Description) Clone() Description {
	return Description{
		Credentials: d.Credentials,
		Candidates:  append([]Candidate(nil), d.Candidates...),
	}
}

func familyForNetwork(network NetworkType) socketmux.AddressFamily {
	if network == NetworkUDP4 {
		return socketmux.IPv4
	}
	return socketmux.IPv6
}

func networkForFamily(family socketmux.AddressFamily) NetworkType {
	if family == socketmux.IPv4 {
		return NetworkUDP4
	}
	return NetworkUDP6
}
