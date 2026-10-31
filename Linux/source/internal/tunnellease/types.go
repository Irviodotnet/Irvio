// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnellease

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	DefaultMaxActive = 64
	DefaultMaxRecent = 128
	DefaultTTL       = 5 * time.Minute
)

// ID is a cryptographically random, non-secret 128-bit lease identifier.
type ID [16]byte

// SessionToken binds one authenticated ICE session without exposing credentials.
type SessionToken [16]byte

func (id ID) String() string { return hex.EncodeToString(id[:]) }

func (id ID) IsZero() bool {
	var zero ID
	return id == zero
}

func (token SessionToken) IsZero() bool {
	var zero SessionToken
	return token == zero
}

// Lease binds Exit-local inner addresses to one authenticated client.
type Lease struct {
	ID         ID
	ExitPeer   peer.ID
	ClientPeer peer.ID
	IPv4       netip.Prefix
	IPv6       netip.Prefix
	Generation uint64
	Session    SessionToken
	ValidUntil time.Time
}

// Validate checks immutable lease structure without consulting allocator state.
func Validate(lease Lease, now time.Time) error {
	if lease.ID.IsZero() || lease.ExitPeer == "" || lease.ClientPeer == "" || lease.ExitPeer == lease.ClientPeer || lease.Generation == 0 ||
		lease.ValidUntil.IsZero() || !lease.ValidUntil.After(now) ||
		!validLeasePrefix(lease.IPv4, true) && !validLeasePrefix(lease.IPv6, false) {
		return ErrInvalidConfig
	}
	if lease.IPv4.IsValid() && !validLeasePrefix(lease.IPv4, true) || lease.IPv6.IsValid() && !validLeasePrefix(lease.IPv6, false) {
		return ErrInvalidConfig
	}
	return nil
}

func validLeasePrefix(prefix netip.Prefix, ipv4 bool) bool {
	if !prefix.IsValid() || prefix.Addr().Is4() != ipv4 || prefix.Addr().Is4In6() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
		return false
	}
	if ipv4 {
		return prefix.Bits() == 32
	}
	return prefix.Bits() == 128
}

// Config deliberately requires caller-selected pools; Irvio has no global IPAM.
type Config struct {
	ExitPeer      peer.ID
	IPv4Pool      netip.Prefix
	IPv6Pool      netip.Prefix
	Reserved      []netip.Addr
	TTL           time.Duration
	MaxActive     int
	MaxRecent     int
	Now           func() time.Time
	Random        io.Reader
	ManagedExpiry bool
}

func DefaultConfig(exit peer.ID) Config {
	return Config{
		ExitPeer: exit, TTL: DefaultTTL, MaxActive: DefaultMaxActive,
		MaxRecent: DefaultMaxRecent, Now: time.Now, Random: rand.Reader,
	}
}
