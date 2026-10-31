// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"net/netip"
	"time"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/libp2p/go-libp2p/core/peer"
)

func DefaultProductionObfuscation() awg.ObfuscationConfig {
	return awg.ObfuscationConfig{
		JunkPacketCount: 1, JunkMin: 24, JunkMax: 24,
		InitPadding: 16, ResponsePadding: 16, CookiePadding: 8,
		InitHeader: 101, ResponseHeader: 202, CookieHeader: 303, TransportHeader: 404,
		SignaturePackets: [5]string{"<b 0x4859445241>"},
	}
}

type ProductionConfig struct {
	Platform            ClientDataPlanePlatform
	MuxFactory          transportgen.MuxFactory
	UDPPort             int
	RequireFixedUDPPort bool
	STUNIPv4            netip.AddrPort
	STUNIPv4Pool        []netip.AddrPort
	STUNIPv6            netip.AddrPort
	TUNName             string
	TunnelDNSServers    []netip.Addr
	Bypass              []netip.Prefix
	Obfuscation         awg.ObfuscationConfig
	ConnectCountry      exitselector.CountryIntent
	PrivateTarget       peer.ID
	PrivateLocator      [32]byte

	ExitWANName      string
	ExitTUNAddresses []netip.Prefix
	ExitIPv4Pool     netip.Prefix
	ExitIPv6Pool     netip.Prefix
	ExitManagement   []netip.Prefix
	LeaseTTL         time.Duration
	LeaseSweep       time.Duration
	LeaseRenewLead   time.Duration
	LeaseRenewRetry  time.Duration
}

func leaseRenewalTimings(ttl, lead, retry time.Duration) (time.Duration, time.Duration, error) {
	if ttl <= 0 {
		return 0, 0, ErrInvalidConfig
	}
	if lead == 0 {
		lead = 20 * time.Second
		if scaled := ttl / 3; lead > scaled {
			lead = scaled
		}
	}
	if retry == 0 {
		retry = 2 * time.Second
		if scaled := ttl / 10; retry > scaled {
			retry = scaled
		}
	}
	if lead <= 0 || lead >= ttl || retry <= 0 || retry >= ttl {
		return 0, 0, ErrInvalidConfig
	}
	return lead, retry, nil
}
