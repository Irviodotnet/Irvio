// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package productruntime

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// UnderlayPath is a kernel-selected local route, not an inferred public NAT endpoint.
type UnderlayPath struct {
	Target    netip.Addr
	IfIndex   int
	IfName    string
	Source    netip.Addr
	Gateway   netip.Addr
	RouteMark uint32
}

// ResolveUnderlay queries the marked Linux FIB rather than guessing interfaces.
func ResolveUnderlay(target netip.Addr, mark uint32) (UnderlayPath, error) {
	if !usableTarget(target) {
		return UnderlayPath{}, fmt.Errorf("underlay target must be a usable literal IP")
	}
	routes, err := netlink.RouteGetWithOptions(net.IP(target.AsSlice()), &netlink.RouteGetOptions{Mark: mark})
	if err != nil {
		return UnderlayPath{}, fmt.Errorf("kernel route lookup for %s: %w", target, err)
	}
	if len(routes) != 1 {
		return UnderlayPath{}, fmt.Errorf("kernel returned %d underlay routes for %s; expected exactly one", len(routes), target)
	}
	route := routes[0]
	if route.LinkIndex <= 0 {
		return UnderlayPath{}, fmt.Errorf("kernel route to %s has no output interface", target)
	}
	link, err := netlink.LinkByIndex(route.LinkIndex)
	if err != nil {
		return UnderlayPath{}, fmt.Errorf("resolve kernel output interface %d: %w", route.LinkIndex, err)
	}
	if link == nil || link.Attrs() == nil {
		return UnderlayPath{}, fmt.Errorf("kernel output interface %d has no metadata", route.LinkIndex)
	}
	return underlayPathFromRoute(target, mark, route, link.Attrs())
}

func underlayPathFromRoute(target netip.Addr, mark uint32, route netlink.Route, attrs *netlink.LinkAttrs) (UnderlayPath, error) {
	if attrs == nil {
		return UnderlayPath{}, fmt.Errorf("kernel output interface has no metadata")
	}
	// Reject RTN_LOCAL bootstrap routes as forwarding WANs, even with public source addresses.
	if route.Type != unix.RTN_UNICAST {
		return UnderlayPath{}, fmt.Errorf("kernel route to %s is not a unicast underlay route (type %d)", target, route.Type)
	}
	if attrs.Name == "lo" || attrs.Flags&net.FlagLoopback != 0 {
		return UnderlayPath{}, fmt.Errorf("kernel route to %s uses a loopback interface", target)
	}
	if attrs.Name == "" || attrs.Name == "hydra0" || attrs.Flags&net.FlagUp == 0 {
		return UnderlayPath{}, fmt.Errorf("kernel route to %s uses an unavailable or Irvio tunnel interface", target)
	}
	source, ok := netip.AddrFromSlice(route.Src)
	if !ok {
		return UnderlayPath{}, fmt.Errorf("kernel route to %s has no preferred source", target)
	}
	source = source.Unmap()
	if !usableTarget(source) || source.Is4() != target.Is4() {
		return UnderlayPath{}, fmt.Errorf("kernel route to %s has an unusable source", target)
	}
	path := UnderlayPath{Target: target, IfIndex: route.LinkIndex, IfName: attrs.Name,
		Source: source, RouteMark: mark}
	if len(route.Gw) != 0 {
		gateway, ok := netip.AddrFromSlice(route.Gw)
		if !ok || gateway.Unmap().Is4() != target.Is4() {
			return UnderlayPath{}, fmt.Errorf("kernel route to %s has an invalid gateway", target)
		}
		path.Gateway = gateway.Unmap()
	}
	return path, nil
}

func usableTarget(address netip.Addr) bool {
	return address.IsValid() && !address.Is4In6() && !address.IsUnspecified() &&
		!address.IsLoopback() && !address.IsMulticast() && !address.IsLinkLocalUnicast()
}
