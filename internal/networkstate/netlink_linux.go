// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package networkstate

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"github.com/vishvananda/netlink"
)

type linuxScanner struct{}

func newLinuxScanner() scanner { return linuxScanner{} }

func (linuxScanner) Scan(context.Context) (Snapshot, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return Snapshot{}, fmt.Errorf("list links: %w", err)
	}
	routes := make([]Route, 0)
	for _, family := range []struct {
		netlink int
		irvio   Family
	}{{netlink.FAMILY_V4, FamilyIPv4}, {netlink.FAMILY_V6, FamilyIPv6}} {
		familyRoutes, err := netlink.RouteListFiltered(family.netlink, &netlink.Route{Table: mainRouteTable}, netlink.RT_FILTER_TABLE)
		if err != nil {
			return Snapshot{}, fmt.Errorf("list %s routes: %w", family.irvio, err)
		}
		for _, route := range familyRoutes {
			if !isDefaultRoute(route) {
				continue
			}
			if len(route.MultiPath) == 0 {
				routes = append(routes, Route{Family: family.irvio, Table: route.Table, Priority: route.Priority, LinkIndex: route.LinkIndex, Gateway: canonicalIP(route.Gw, family.irvio), PreferredSource: canonicalIP(route.Src, family.irvio)})
				continue
			}
			for _, hop := range route.MultiPath {
				if hop == nil {
					continue
				}
				routes = append(routes, Route{Family: family.irvio, Table: route.Table, Priority: route.Priority, LinkIndex: hop.LinkIndex, Gateway: canonicalIP(hop.Gw, family.irvio), PreferredSource: canonicalIP(route.Src, family.irvio)})
			}
		}
	}

	normalizedLinks := make([]Link, 0, len(links))
	for _, link := range links {
		attrs := link.Attrs()
		if attrs == nil {
			continue
		}
		addresses, err := netlink.AddrList(link, netlink.FAMILY_ALL)
		if err != nil {
			return Snapshot{}, fmt.Errorf("list addresses for %s: %w", attrs.Name, err)
		}
		prefixes := make([]netip.Prefix, 0, len(addresses))
		for _, address := range addresses {
			prefix, ok := prefixFromNetlink(address)
			if ok {
				prefixes = append(prefixes, prefix)
			}
		}
		normalizedLinks = append(normalizedLinks, Link{
			Index:     attrs.Index,
			Name:      attrs.Name,
			Up:        linkIsUp(attrs),
			MTU:       attrs.MTU,
			Addresses: prefixes,
		})
	}
	return Normalize(routes, normalizedLinks)
}

func isDefaultRoute(route netlink.Route) bool {
	if route.Dst == nil {
		return true
	}
	ones, _ := route.Dst.Mask.Size()
	return ones == 0
}

func canonicalIP(ip net.IP, family Family) netip.Addr {
	if len(ip) == 0 {
		return netip.Addr{}
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}
	}
	if family == FamilyIPv4 {
		addr = addr.Unmap()
	}
	return addr
}

func prefixFromNetlink(address netlink.Addr) (netip.Prefix, bool) {
	if address.IPNet == nil {
		return netip.Prefix{}, false
	}
	addr, ok := netip.AddrFromSlice(address.IPNet.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	ones, _ := address.IPNet.Mask.Size()
	prefix := netip.PrefixFrom(addr, ones)
	return prefix, prefix.IsValid()
}

func linkIsUp(attrs *netlink.LinkAttrs) bool {
	if attrs.Flags&net.FlagUp == 0 {
		return false
	}
	switch attrs.OperState {
	case netlink.OperDown, netlink.OperLowerLayerDown, netlink.OperNotPresent:
		return false
	default:
		return true
	}
}

type linuxSubscription struct {
	events chan struct{}
	errors chan error
	stop   chan struct{}
	done   chan struct{}

	netlinkDone chan struct{}
	closeOnce   sync.Once
	closeErr    error
}

func newLinuxSubscription(eventBuffer int) (subscription, error) {
	s := &linuxSubscription{
		events:      make(chan struct{}, 1),
		errors:      make(chan error, 1),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		netlinkDone: make(chan struct{}),
	}
	routeUpdates := make(chan netlink.RouteUpdate, eventBuffer)
	addrUpdates := make(chan netlink.AddrUpdate, eventBuffer)
	linkUpdates := make(chan netlink.LinkUpdate, eventBuffer)
	options := netlink.RouteSubscribeOptions{ErrorCallback: s.reportError}
	if err := netlink.RouteSubscribeWithOptions(routeUpdates, s.netlinkDone, options); err != nil {
		close(s.netlinkDone)
		return nil, err
	}
	if err := netlink.AddrSubscribeWithOptions(addrUpdates, s.netlinkDone, netlink.AddrSubscribeOptions{ErrorCallback: s.reportError}); err != nil {
		close(s.netlinkDone)
		return nil, err
	}
	if err := netlink.LinkSubscribeWithOptions(linkUpdates, s.netlinkDone, netlink.LinkSubscribeOptions{ErrorCallback: s.reportError}); err != nil {
		close(s.netlinkDone)
		return nil, err
	}
	go s.run(routeUpdates, addrUpdates, linkUpdates)
	return s, nil
}

func (s *linuxSubscription) run(routes <-chan netlink.RouteUpdate, addresses <-chan netlink.AddrUpdate, links <-chan netlink.LinkUpdate) {
	defer close(s.done)
	defer close(s.events)
	for {
		select {
		case <-s.stop:
			return
		case _, ok := <-routes:
			if !ok {
				s.reportError(ErrSubscriptionClosed)
				return
			}
			s.notify()
		case _, ok := <-addresses:
			if !ok {
				s.reportError(ErrSubscriptionClosed)
				return
			}
			s.notify()
		case _, ok := <-links:
			if !ok {
				s.reportError(ErrSubscriptionClosed)
				return
			}
			s.notify()
		}
	}
}

func (s *linuxSubscription) notify() {
	select {
	case s.events <- struct{}{}:
	default:
	}
}

func (s *linuxSubscription) reportError(err error) {
	select {
	case s.errors <- err:
	default:
	}
}

func (s *linuxSubscription) Events() <-chan struct{} { return s.events }
func (s *linuxSubscription) Errors() <-chan error    { return s.errors }

func (s *linuxSubscription) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		close(s.netlinkDone)
		<-s.done
	})
	return s.closeErr
}
