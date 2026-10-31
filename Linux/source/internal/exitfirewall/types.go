// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitfirewall

import (
	"context"
	"net/netip"
)

const DefaultTableName = "hydra_exit"

// Policy is the complete bounded forwarding authority granted to one Exit.
type Policy struct {
	TableName string
	TUNName   string
	WANName   string

	ClientIPv4 netip.Prefix
	ClientIPv6 netip.Prefix
	WANIPv4    netip.Addr
	WANIPv6    netip.Addr
	// SessionControl lists exact pre-authorization inner TCP endpoints for authenticated peers.
	SessionControl []netip.AddrPort

	// Extra management blocks supplement mandatory loopback, private, and link-local blocks.
	BlockedDestinations []netip.Prefix
}

// Snapshot contains redacted independent counters read back from the kernel.
type Snapshot struct {
	TableName string
	Rules     int
	Counters  map[string]Counter
}

// Counter is one nftables rule counter.
type Counter struct {
	Packets uint64
	Bytes   uint64
}

// Installation owns one dedicated nftables table.
type Installation interface {
	Snapshot(context.Context) (Snapshot, error)
	// SetLocalTunnelAddresses permits established upstream replies; an empty slice revokes access.
	SetLocalTunnelAddresses(context.Context, []netip.Addr) error
	// SetNATAddresses pins WAN sources; zero retains kernel-selected masquerading.
	SetNATAddresses(context.Context, netip.Addr, netip.Addr) error
	Close() error
}

// WANPathReconciler changes only WAN-dependent rules, preserving safety and anti-multihop ordering.
type WANPathReconciler interface {
	SetWANPath(context.Context, string, netip.Addr, netip.Addr) error
}

// FirewallBackend returns an owned policy without modifying unrelated tables.
type FirewallBackend interface {
	Apply(context.Context, Policy) (Installation, error)
}
