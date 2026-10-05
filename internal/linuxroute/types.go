// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package linuxroute

import (
	"net/netip"
)

const (
	IrvioBypassMark              uint32 = 0x48594452
	IrvioTunnelControlMark       uint32 = 0x48595443
	IrvioRoutingProtocol         uint8  = 0x48
	IrvioOutTable                       = 51820
	IrvioWANTable                       = 51821
	IrvioTunnelControlTable             = 51822
	DefaultOuterMark                    = IrvioBypassMark
	DefaultOutTable                     = IrvioOutTable
	DefaultWANTable                     = IrvioWANTable
	DefaultBypassPriority               = 9900
	DefaultTunnelControlPriority        = 9800
	DefaultOuterPriority                = 10000
	DefaultForwardPriority              = 10050
	DefaultProtectedPriority            = 11000
)

// WANPath requires a connected prefix when a gateway needs resolution in the alternate table.
type WANPath struct {
	LinkIndex int
	Source    netip.Addr
	Gateway   netip.Addr
	Connected netip.Prefix
}

// TunnelControlRoute binds an inner control destination to its exact logical lease source.
type TunnelControlRoute struct {
	Destination netip.Prefix
	Source      netip.Addr
}

// Config limits routing mutations to explicit, collision-checked identifiers.
type Config struct {
	TUNIndex int
	TUNName  string

	OuterMark          uint32
	OutTable           int
	WANTable           int
	TunnelControlMark  uint32
	TunnelControlTable int

	OuterRulePriority         int
	ForwardRulePriority       int
	BypassRulePriority        int
	ProtectedRulePriority     int
	TunnelControlRulePriority int

	ProtectIPv4 bool
	ProtectIPv6 bool
	RejectIPv4  bool
	RejectIPv6  bool
	OuterIPv4   bool
	OuterIPv6   bool
	// ForwardedTUNName routes downstream traffic to WAN before client protection; empty means client-only.
	ForwardedTUNName string
	WANIPv4          WANPath
	WANIPv6          WANPath
	// TunnelIPv4Source and TunnelIPv6Source select the client lease on a shared dual-role TUN.
	TunnelIPv4Source netip.Addr
	TunnelIPv6Source netip.Addr
	// TunnelControl is the single-source compatibility list for marked, TUN-only destinations.
	TunnelControl []netip.Prefix
	// TunnelControlRoutes assigns exactly one source/owner pair per destination.
	TunnelControlRoutes []TunnelControlRoute

	// Bypass prefixes retain main-table LAN/control reachability without recursive tunneling.
	Bypass []netip.Prefix
}

// DefaultConfig reserves identifiers; callers supply interfaces, families, WAN paths, and bypasses.
func DefaultConfig() Config {
	return Config{
		OuterMark: DefaultOuterMark, OutTable: DefaultOutTable, WANTable: DefaultWANTable,
		TunnelControlMark: IrvioTunnelControlMark, TunnelControlTable: IrvioTunnelControlTable,
		TunnelControlRulePriority: DefaultTunnelControlPriority,
		OuterRulePriority:         DefaultOuterPriority, ForwardRulePriority: DefaultForwardPriority,
		BypassRulePriority:    DefaultBypassPriority,
		ProtectedRulePriority: DefaultProtectedPriority,
	}
}

// State is a redacted immutable description of installed Irvio routing state.
type State struct {
	OuterMark          uint32
	OutTable           int
	WANTable           int
	TunnelControlMark  uint32
	TunnelControlTable int
	Rules              int
	Routes             int
	Closed             bool
}
