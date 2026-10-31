// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"net/netip"

	ma "github.com/multiformats/go-multiaddr"
)

const STUNPort uint16 = 3478

// BootstrapSTUNIPv4 derives a redundant public IPv4 pool, excluding relay, private, and test addresses.
func BootstrapSTUNIPv4(addresses []string) []netip.AddrPort {
	return BootstrapSTUNIPv4WithTestAddresses(addresses, false)
}

// BootstrapSTUNIPv4WithTestAddresses permits private addresses only in engineering labs.
func BootstrapSTUNIPv4WithTestAddresses(addresses []string, allowTest bool) []netip.AddrPort {
	result := make([]netip.AddrPort, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, value := range addresses {
		address, err := ma.NewMultiaddr(value)
		if err != nil {
			continue
		}
		parts := ma.Split(address)
		if len(parts) != 3 || parts[0].Protocols()[0].Code != ma.P_IP4 ||
			parts[1].Protocols()[0].Code != ma.P_TCP || parts[2].Protocols()[0].Code != ma.P_P2P {
			continue
		}
		ip, err := netip.ParseAddr(parts[0].Value())
		if err != nil || !publicIPv4(ip) && !(allowTest && ip.Is4() && ip.IsGlobalUnicast()) {
			continue
		}
		if _, exists := seen[ip]; exists {
			continue
		}
		seen[ip] = struct{}{}
		result = append(result, netip.AddrPortFrom(ip, STUNPort))
	}
	return result
}

func publicIPv4(ip netip.Addr) bool {
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range nonPublicIPv4 {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicIPv4 = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
}
