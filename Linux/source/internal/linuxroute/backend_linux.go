// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package linuxroute

import "github.com/vishvananda/netlink"

type backend interface {
	RuleList(family int) ([]netlink.Rule, error)
	RuleAdd(*netlink.Rule) error
	RuleDel(*netlink.Rule) error
	RouteListFiltered(family int, filter *netlink.Route, mask uint64) ([]netlink.Route, error)
	RouteAdd(*netlink.Route) error
	RouteReplace(*netlink.Route) error
	RouteDel(*netlink.Route) error
}

type netlinkBackend struct{}

func (netlinkBackend) RuleList(family int) ([]netlink.Rule, error) { return netlink.RuleList(family) }
func (netlinkBackend) RuleAdd(rule *netlink.Rule) error            { return netlink.RuleAdd(rule) }
func (netlinkBackend) RuleDel(rule *netlink.Rule) error            { return netlink.RuleDel(rule) }
func (netlinkBackend) RouteListFiltered(family int, filter *netlink.Route, mask uint64) ([]netlink.Route, error) {
	return netlink.RouteListFiltered(family, filter, mask)
}
func (netlinkBackend) RouteAdd(route *netlink.Route) error     { return netlink.RouteAdd(route) }
func (netlinkBackend) RouteReplace(route *netlink.Route) error { return netlink.RouteReplace(route) }
func (netlinkBackend) RouteDel(route *netlink.Route) error     { return netlink.RouteDel(route) }
