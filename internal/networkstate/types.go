// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package networkstate

import (
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// Generation identifies a process-local semantic network state.
type Generation uint64

// Family identifies an IP address family.
type Family uint8

const (
	FamilyUnknown Family = iota
	FamilyIPv4
	FamilyIPv6
)

const mainRouteTable = 254

// Route describes a usable default route.
type Route struct {
	Family          Family
	Table           int
	Priority        int
	LinkIndex       int
	Gateway         netip.Addr
	PreferredSource netip.Addr
}

// Link describes a default-route interface and its usable addresses.
type Link struct {
	Index     int
	Name      string
	Up        bool
	MTU       int
	Addresses []netip.Prefix
}

// Snapshot is a normalized immutable view of transport-relevant network state.
type Snapshot struct {
	Routes []Route
	Links  []Link
}

// State pairs a snapshot with its monotonically increasing generation.
type State struct {
	Generation Generation
	Snapshot   Snapshot
	ObservedAt time.Time
}

var (
	ErrClosed             = errors.New("network state monitor is closed")
	ErrSubscriptionClosed = errors.New("network state subscription closed unexpectedly")
)

func (f Family) String() string {
	switch f {
	case FamilyIPv4:
		return "ipv4"
	case FamilyIPv6:
		return "ipv6"
	default:
		return "unknown"
	}
}

func familyForAddr(addr netip.Addr) Family {
	if !addr.IsValid() || addr.Is4In6() {
		return FamilyUnknown
	}
	if addr.Is4() {
		return FamilyIPv4
	}
	return FamilyIPv6
}

func validateFamily(f Family) error {
	if f != FamilyIPv4 && f != FamilyIPv6 {
		return fmt.Errorf("invalid address family %d", f)
	}
	return nil
}
