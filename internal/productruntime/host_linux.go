// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package productruntime

import (
	"fmt"
	"net/netip"

	"github.com/vishvananda/netlink"
)

const (
	DefaultDataDir   = "/var/lib/irvio/profile"
	DefaultGeoIPPath = "/var/lib/irvio/geo/country.mmdb"
)

// ResolveBootstrapUnderlay tries eligible bootstrap FIB routes without assuming an interface name.
func ResolveBootstrapUnderlay(stunPool []netip.AddrPort, mark uint32) (UnderlayPath, error) {
	var last error
	for _, endpoint := range stunPool {
		path, err := ResolveUnderlay(endpoint.Addr(), mark)
		if err == nil {
			return path, nil
		}
		last = err
	}
	if last != nil {
		return UnderlayPath{}, fmt.Errorf("no usable bootstrap underlay route: %w", last)
	}
	return UnderlayPath{}, fmt.Errorf("no direct public IPv4 bootstrap peer for underlay/STUN")
}

// ConnectedBypassPrefixes includes only directly connected IPv4 prefixes, never defaults or gateway routes.
func ConnectedBypassPrefixes() ([]netip.Prefix, error) {
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{}, 0)
	if err != nil {
		return nil, fmt.Errorf("list directly connected IPv4 routes: %w", err)
	}
	result := make([]netip.Prefix, 0, len(routes))
	seen := make(map[netip.Prefix]struct{}, len(routes))
	for _, route := range routes {
		if route.Dst == nil || route.Dst.IP == nil || len(route.Gw) != 0 || route.LinkIndex <= 0 {
			continue
		}
		link, err := netlink.LinkByIndex(route.LinkIndex)
		if err != nil || link.Attrs() == nil || link.Attrs().Name == "hydra0" {
			continue
		}
		prefix, ok := prefixFromIPNet(route.Dst)
		if !ok || prefix.Bits() == 0 {
			continue
		}
		if _, duplicate := seen[prefix]; duplicate {
			continue
		}
		seen[prefix] = struct{}{}
		result = append(result, prefix)
	}
	return result, nil
}
