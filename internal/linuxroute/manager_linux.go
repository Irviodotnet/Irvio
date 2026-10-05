// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package linuxroute

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Manager owns only the exact routes and policy rules it successfully added.
type Manager struct {
	config Config
	back   backend
	lock   *routeLock

	mu                 sync.Mutex
	rules              []netlink.Rule
	routes             []netlink.Route
	tunnelControlGuard map[int]bool
	closed             bool
	closeOnce          sync.Once
	closeErr           error
}

// Apply installs collision-checked routing state with rollback.
func Apply(config Config) (*Manager, error) {
	lock, err := acquireRouteLock()
	if err != nil {
		return nil, err
	}
	manager, err := apply(config, netlinkBackend{})
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	manager.lock = lock
	return manager, nil
}

// CleanupCrashResidue removes only marked reserved routes while excluding live owners through routeLock.
func CleanupCrashResidue() (resultErr error) {
	lock, err := acquireRouteLock()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	manager := &Manager{config: withDefaults(DefaultConfig()), back: netlinkBackend{}}
	return manager.reconcileOwnedResidue(true)
}

func apply(config Config, back backend) (_ *Manager, resultErr error) {
	config = withDefaults(config)
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	m := &Manager{config: config, back: back, tunnelControlGuard: make(map[int]bool)}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, m.rollback())
		}
	}()
	if err := m.reconcileOwnedResidue(false); err != nil {
		return nil, err
	}
	if err := m.ensureIdentifiersFree(); err != nil {
		return nil, err
	}
	for _, route := range buildRoutes(config) {
		candidate := route
		if err := back.RouteAdd(&candidate); err != nil {
			return nil, fmt.Errorf("add Irvio route %s: %w", candidate.String(), err)
		}
		m.routes = append(m.routes, candidate)
	}
	for _, rule := range buildRules(config) {
		candidate := rule
		if err := back.RuleAdd(&candidate); err != nil {
			return nil, fmt.Errorf("add Irvio policy rule priority %d: %w", candidate.Priority, err)
		}
		m.rules = append(m.rules, candidate)
	}
	for _, route := range config.TunnelControlRoutes {
		m.tunnelControlGuard[tunnelControlFamily(route.Destination)] = true
	}
	return m, nil
}

func withDefaults(config Config) Config {
	defaults := DefaultConfig()
	if config.OuterMark == 0 {
		config.OuterMark = defaults.OuterMark
	}
	if config.OutTable == 0 {
		config.OutTable = defaults.OutTable
	}
	if config.WANTable == 0 {
		config.WANTable = defaults.WANTable
	}
	if config.TunnelControlMark == 0 {
		config.TunnelControlMark = defaults.TunnelControlMark
	}
	if config.TunnelControlTable == 0 {
		config.TunnelControlTable = defaults.TunnelControlTable
	}
	if config.TunnelControlRulePriority == 0 {
		config.TunnelControlRulePriority = defaults.TunnelControlRulePriority
	}
	if config.OuterRulePriority == 0 {
		config.OuterRulePriority = defaults.OuterRulePriority
	}
	if config.ForwardRulePriority == 0 {
		config.ForwardRulePriority = defaults.ForwardRulePriority
	}
	if config.BypassRulePriority == 0 {
		config.BypassRulePriority = defaults.BypassRulePriority
	}
	if config.ProtectedRulePriority == 0 {
		config.ProtectedRulePriority = defaults.ProtectedRulePriority
	}
	config.Bypass = normalizePrefixes(config.Bypass)
	config.TunnelControl = normalizePrefixes(config.TunnelControl)
	config.TunnelControlRoutes = effectiveTunnelControlRoutes(config)
	config.TunnelControl = nil
	return config
}

func validateConfig(config Config) error {
	if config.TUNIndex <= 0 || config.TUNName == "" || len(config.TUNName) > 15 || config.OuterMark == 0 ||
		config.TunnelControlMark == 0 || config.TunnelControlMark == config.OuterMark ||
		config.OutTable <= 0 || config.WANTable <= 0 || config.TunnelControlTable <= 0 ||
		config.OutTable == config.WANTable || config.OutTable == config.TunnelControlTable ||
		config.WANTable == config.TunnelControlTable ||
		config.TunnelControlRulePriority <= 0 || config.TunnelControlRulePriority >= config.BypassRulePriority ||
		config.BypassRulePriority <= 0 || config.OuterRulePriority <= config.BypassRulePriority ||
		config.ForwardRulePriority <= config.OuterRulePriority ||
		config.ProtectedRulePriority <= config.ForwardRulePriority ||
		(!config.ProtectIPv4 && !config.RejectIPv4 && !config.ProtectIPv6 && !config.RejectIPv6) {
		return ErrInvalidConfig
	}
	if config.ForwardedTUNName != "" && (len(config.ForwardedTUNName) > 15 || config.ForwardedTUNName != config.TUNName) {
		return ErrInvalidConfig
	}
	if config.ProtectIPv4 && config.RejectIPv4 || config.ProtectIPv6 && config.RejectIPv6 {
		return ErrInvalidConfig
	}
	seenControl := make(map[netip.Prefix]netip.Addr, len(config.TunnelControlRoutes))
	for _, route := range config.TunnelControlRoutes {
		prefix, source := route.Destination, route.Source
		if previous, exists := seenControl[prefix]; exists && previous != source {
			return ErrInvalidConfig
		}
		seenControl[prefix] = source
	}
	if config.TunnelIPv4Source.IsValid() && (!config.TunnelIPv4Source.Is4() || config.TunnelIPv4Source.Is4In6() ||
		(!config.ProtectIPv4 && !config.RejectIPv4)) ||
		config.TunnelIPv6Source.IsValid() && (!config.TunnelIPv6Source.Is6() || config.TunnelIPv6Source.Is4In6() ||
			(!config.ProtectIPv6 && !config.RejectIPv6)) {
		return ErrInvalidConfig
	}
	for _, familyIsIPv4 := range []bool{true, false} {
		count := 0
		for _, prefix := range config.Bypass {
			if prefix.IsValid() && prefix.Addr().Is4() == familyIsIPv4 {
				count++
			}
		}
		if config.BypassRulePriority+count >= config.OuterRulePriority {
			return ErrInvalidConfig
		}
	}
	if !config.OuterIPv4 && !config.OuterIPv6 || config.OuterIPv4 && !validWAN(config.WANIPv4, true) || config.OuterIPv6 && !validWAN(config.WANIPv6, false) {
		return ErrInvalidConfig
	}
	for _, prefix := range config.Bypass {
		if !prefix.IsValid() || prefix.Addr().Is4In6() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
			return ErrInvalidConfig
		}
	}
	for _, route := range config.TunnelControlRoutes {
		prefix, source := route.Destination, route.Source
		if !prefix.IsValid() || !source.IsValid() || source.Is4In6() || source.Is4() != prefix.Addr().Is4() ||
			prefix.Addr().Is4In6() || prefix.Addr().IsUnspecified() ||
			prefix.Addr().IsMulticast() || prefix.Addr().IsLoopback() ||
			(prefix.Addr().Is4() && prefix.Bits() != 32) || (prefix.Addr().Is6() && prefix.Bits() != 128) {
			return ErrInvalidConfig
		}
	}
	return nil
}

func validWAN(path WANPath, ipv4 bool) bool {
	if path.LinkIndex <= 0 || !path.Source.IsValid() || path.Source.Is4In6() || path.Source.Is4() != ipv4 {
		return false
	}
	if path.Gateway.IsValid() && (path.Gateway.Is4In6() || path.Gateway.Is4() != ipv4) {
		return false
	}
	if path.Connected.IsValid() {
		if path.Connected.Addr().Is4In6() || path.Connected.Addr().Is4() != ipv4 || !path.Connected.Contains(path.Source) {
			return false
		}
	}
	if path.Gateway.IsValid() && (!path.Connected.IsValid() || !path.Connected.Contains(path.Gateway)) {
		return false
	}
	return true
}

func normalizePrefixes(input []netip.Prefix) []netip.Prefix {
	seen := make(map[netip.Prefix]struct{}, len(input))
	result := make([]netip.Prefix, 0, len(input))
	for _, prefix := range input {
		if !prefix.IsValid() {
			result = append(result, prefix)
			continue
		}
		prefix = prefix.Masked()
		if _, exists := seen[prefix]; exists {
			continue
		}
		seen[prefix] = struct{}{}
		result = append(result, prefix)
	}
	slices.SortFunc(result, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
	return result
}

func normalizeTunnelControlRoutes(input []TunnelControlRoute) []TunnelControlRoute {
	seen := make(map[TunnelControlRoute]struct{}, len(input))
	result := make([]TunnelControlRoute, 0, len(input))
	for _, route := range input {
		if route.Destination.IsValid() {
			route.Destination = route.Destination.Masked()
		}
		if _, exists := seen[route]; exists {
			continue
		}
		seen[route] = struct{}{}
		result = append(result, route)
	}
	slices.SortFunc(result, func(a, b TunnelControlRoute) int {
		if value := a.Destination.Addr().Compare(b.Destination.Addr()); value != 0 {
			return value
		}
		if a.Destination.Bits() < b.Destination.Bits() {
			return -1
		}
		if a.Destination.Bits() > b.Destination.Bits() {
			return 1
		}
		return a.Source.Compare(b.Source)
	})
	return result
}

func effectiveTunnelControlRoutes(config Config) []TunnelControlRoute {
	routes := append([]TunnelControlRoute(nil), config.TunnelControlRoutes...)
	for _, prefix := range normalizePrefixes(config.TunnelControl) {
		source := config.TunnelIPv6Source
		if prefix.Addr().Is4() {
			source = config.TunnelIPv4Source
		}
		routes = append(routes, TunnelControlRoute{Destination: prefix, Source: source})
	}
	return normalizeTunnelControlRoutes(routes)
}

func (m *Manager) ensureIdentifiersFree() error {
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		rules, err := m.back.RuleList(family)
		if err != nil {
			return fmt.Errorf("list policy rules: %w", err)
		}
		desired := desiredPriorities(m.config, family)
		for _, rule := range rules {
			if _, exists := desired[rule.Priority]; exists {
				return fmt.Errorf("policy rule priority %d: %w", rule.Priority, ErrConflict)
			}
		}
		for _, table := range ownedTables(m.config) {
			routes, err := m.back.RouteListFiltered(family, &netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
			if err != nil {
				return fmt.Errorf("list route table %d: %w", table, err)
			}
			if len(routes) != 0 {
				return fmt.Errorf("route table %d: %w", table, ErrConflict)
			}
		}
	}
	return nil
}

func (m *Manager) reconcileOwnedResidue(includeControl bool) error {
	type ownedState struct {
		rules  []netlink.Rule
		routes []netlink.Route
	}
	var owned ownedState
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		rules, err := m.back.RuleList(family)
		if err != nil {
			return fmt.Errorf("list policy rules for reconciliation: %w", err)
		}
		desired := desiredPriorities(m.config, family)
		for _, rule := range rules {
			_, desiredPriority := desired[rule.Priority]
			if rule.Protocol == IrvioRoutingProtocol && (desiredPriority ||
				rule.Priority >= m.config.TunnelControlRulePriority && rule.Priority <= m.config.ProtectedRulePriority) {
				owned.rules = append(owned.rules, rule)
				continue
			}
			if _, exists := desired[rule.Priority]; exists || includeControl && rule.Priority == m.config.TunnelControlRulePriority {
				return fmt.Errorf("policy rule priority %d: %w", rule.Priority, ErrConflict)
			}
		}
		tables := ownedTables(m.config)
		if includeControl && !slices.Contains(tables, m.config.TunnelControlTable) {
			tables = append(tables, m.config.TunnelControlTable)
		}
		for _, table := range tables {
			routes, err := m.back.RouteListFiltered(family, &netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
			if err != nil {
				return fmt.Errorf("list route table %d for reconciliation: %w", table, err)
			}
			for _, route := range routes {
				if route.Protocol != netlink.RouteProtocol(IrvioRoutingProtocol) {
					return fmt.Errorf("route table %d: %w", table, ErrConflict)
				}
				owned.routes = append(owned.routes, route)
			}
		}
	}
	for index := len(owned.rules) - 1; index >= 0; index-- {
		if err := m.back.RuleDel(&owned.rules[index]); err != nil && !routingStateAbsent(err) {
			return fmt.Errorf("remove stale Irvio policy rule: %w", err)
		}
	}
	for index := len(owned.routes) - 1; index >= 0; index-- {
		if err := m.back.RouteDel(&owned.routes[index]); err != nil && !routingStateAbsent(err) {
			return fmt.Errorf("remove stale Irvio route: %w", err)
		}
	}
	return nil
}

func buildRoutes(config Config) []netlink.Route {
	routes := make([]netlink.Route, 0, 8)
	for _, item := range []struct {
		enabled bool
		family  int
		path    WANPath
	}{
		{config.OuterIPv4, netlink.FAMILY_V4, config.WANIPv4},
		{config.OuterIPv6, netlink.FAMILY_V6, config.WANIPv6},
	} {
		if !item.enabled {
			continue
		}
		if item.path.Connected.IsValid() {
			routes = append(routes, netlink.Route{LinkIndex: item.path.LinkIndex, Dst: prefixIPNet(item.path.Connected), Src: addrIP(item.path.Source), Scope: netlink.SCOPE_LINK, Table: config.WANTable, Protocol: netlink.RouteProtocol(IrvioRoutingProtocol)})
		}
		routes = append(routes, netlink.Route{LinkIndex: item.path.LinkIndex, Dst: defaultIPNet(item.family), Gw: addrIP(item.path.Gateway), Src: addrIP(item.path.Source), Table: config.WANTable, Protocol: netlink.RouteProtocol(IrvioRoutingProtocol)})
	}
	if config.ProtectIPv4 {
		routes = append(routes, netlink.Route{LinkIndex: config.TUNIndex, Dst: defaultIPNet(netlink.FAMILY_V4), Src: addrIP(config.TunnelIPv4Source), Table: config.OutTable, Protocol: netlink.RouteProtocol(IrvioRoutingProtocol), Family: netlink.FAMILY_V4})
	}
	if config.ProtectIPv6 {
		routes = append(routes, netlink.Route{LinkIndex: config.TUNIndex, Dst: defaultIPNet(netlink.FAMILY_V6), Src: addrIP(config.TunnelIPv6Source), Table: config.OutTable, Protocol: netlink.RouteProtocol(IrvioRoutingProtocol), Family: netlink.FAMILY_V6})
	}
	for _, item := range []struct {
		reject bool
		family int
	}{{config.RejectIPv4, netlink.FAMILY_V4}, {config.RejectIPv6, netlink.FAMILY_V6}} {
		if item.reject {
			routes = append(routes, netlink.Route{Dst: defaultIPNet(item.family), Table: config.OutTable, Type: unix.RTN_UNREACHABLE, Protocol: netlink.RouteProtocol(IrvioRoutingProtocol), Family: item.family})
		}
	}
	controlFamilies := make(map[int]bool)
	for _, control := range effectiveTunnelControlRoutes(config) {
		prefix, source := control.Destination, control.Source
		family := tunnelControlFamily(prefix)
		controlFamilies[family] = true
		routes = append(routes, netlink.Route{
			LinkIndex: config.TUNIndex, Dst: prefixIPNet(prefix), Src: addrIP(source), Table: config.TunnelControlTable,
			Protocol: netlink.RouteProtocol(IrvioRoutingProtocol), Family: family,
		})
	}
	for family := range controlFamilies {
		routes = append(routes, netlink.Route{
			Dst: defaultIPNet(family), Table: config.TunnelControlTable, Type: unix.RTN_UNREACHABLE,
			Protocol: netlink.RouteProtocol(IrvioRoutingProtocol), Family: family,
		})
	}
	return routes
}

func ownedTables(config Config) []int {
	tables := []int{config.OutTable, config.WANTable}
	if len(effectiveTunnelControlRoutes(config)) != 0 {
		tables = append(tables, config.TunnelControlTable)
	}
	return tables
}

// ReconcileWAN replaces physical-egress routes without changing protected tunnel ownership.
func (m *Manager) ReconcileWAN(ipv4, ipv6 WANPath) error {
	if m == nil {
		return ErrClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	next := m.config
	next.OuterIPv4, next.WANIPv4 = ipv4.Source.IsValid(), ipv4
	next.OuterIPv6, next.WANIPv6 = ipv6.Source.IsValid(), ipv6
	if err := validateConfig(next); err != nil {
		return err
	}
	oldWAN, retained := splitWANRoutes(m.routes, m.config.WANTable)
	desiredWAN, _ := splitWANRoutes(buildRoutes(next), next.WANTable)
	rollback := func() error {
		var result error
		for index := len(oldWAN) - 1; index >= 0; index-- {
			route := oldWAN[index]
			result = errors.Join(result, m.back.RouteReplace(&route))
		}
		for index := len(desiredWAN) - 1; index >= 0; index-- {
			if hasRouteDestination(oldWAN, desiredWAN[index]) {
				continue
			}
			route := desiredWAN[index]
			if err := m.back.RouteDel(&route); err != nil && !routingStateAbsent(err) {
				result = errors.Join(result, err)
			}
		}
		return result
	}
	for index := range desiredWAN {
		route := desiredWAN[index]
		if err := m.back.RouteReplace(&route); err != nil {
			return errors.Join(fmt.Errorf("replace Irvio WAN route %s: %w", route.String(), err), rollback())
		}
	}
	for index := range oldWAN {
		if hasRouteDestination(desiredWAN, oldWAN[index]) {
			continue
		}
		route := oldWAN[index]
		if err := m.back.RouteDel(&route); err != nil && !routingStateAbsent(err) {
			return errors.Join(fmt.Errorf("remove obsolete Irvio WAN route %s: %w", route.String(), err), rollback())
		}
	}
	m.config = next
	m.routes = append(retained, desiredWAN...)
	return nil
}

// ReconcileTunnelControl retains marked unreachable defaults until Close, preventing stale sockets from falling through to WAN.
func (m *Manager) ReconcileTunnelControl(routes []TunnelControlRoute) error {
	if m == nil {
		return ErrClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	next := m.config
	next.TunnelControl = nil
	next.TunnelControlRoutes = normalizeTunnelControlRoutes(routes)
	if err := validateConfig(next); err != nil {
		return err
	}
	guards := make(map[int]bool, len(m.tunnelControlGuard)+2)
	for family := range m.tunnelControlGuard {
		guards[family] = true
	}
	for _, route := range next.TunnelControlRoutes {
		guards[tunnelControlFamily(route.Destination)] = true
	}
	oldControl, retainedRoutes := splitTableRoutes(m.routes, m.config.TunnelControlTable)
	desiredControl := desiredTunnelControlRoutes(next, guards)
	oldRules, retainedRules := splitTunnelControlRules(m.rules, m.config)
	desiredRules := desiredTunnelControlRules(next, guards)
	var addedRules []netlink.Rule
	rollback := func() error {
		var result error
		for index := len(oldControl) - 1; index >= 0; index-- {
			route := oldControl[index]
			result = errors.Join(result, m.back.RouteReplace(&route))
		}
		for index := len(desiredControl) - 1; index >= 0; index-- {
			if hasRouteDestination(oldControl, desiredControl[index]) {
				continue
			}
			route := desiredControl[index]
			if err := m.back.RouteDel(&route); err != nil && !routingStateAbsent(err) {
				result = errors.Join(result, err)
			}
		}
		for index := len(addedRules) - 1; index >= 0; index-- {
			rule := addedRules[index]
			if err := m.back.RuleDel(&rule); err != nil && !routingStateAbsent(err) {
				result = errors.Join(result, err)
			}
		}
		return result
	}
	for index := range desiredControl {
		route := desiredControl[index]
		if err := m.back.RouteReplace(&route); err != nil {
			return errors.Join(fmt.Errorf("replace Irvio tunnel-control route %s: %w", route.String(), err), rollback())
		}
	}
	for index := range desiredRules {
		if hasTunnelControlRule(oldRules, desiredRules[index]) {
			continue
		}
		rule := desiredRules[index]
		if err := m.back.RuleAdd(&rule); err != nil {
			return errors.Join(fmt.Errorf("add Irvio tunnel-control rule for family %d: %w", rule.Family, err), rollback())
		}
		addedRules = append(addedRules, rule)
	}
	for index := range oldControl {
		if hasRouteDestination(desiredControl, oldControl[index]) {
			continue
		}
		route := oldControl[index]
		if err := m.back.RouteDel(&route); err != nil && !routingStateAbsent(err) {
			return errors.Join(fmt.Errorf("remove obsolete Irvio tunnel-control route %s: %w", route.String(), err), rollback())
		}
	}
	m.config = next
	m.tunnelControlGuard = guards
	m.routes = append(retainedRoutes, desiredControl...)
	m.rules = append(retainedRules, desiredRules...)
	return nil
}

func desiredTunnelControlRoutes(config Config, guards map[int]bool) []netlink.Route {
	var result []netlink.Route
	for _, route := range buildRoutes(config) {
		if route.Table == config.TunnelControlTable && route.Type != unix.RTN_UNREACHABLE {
			result = append(result, route)
		}
	}
	for family := range guards {
		result = append(result, netlink.Route{
			Dst: defaultIPNet(family), Table: config.TunnelControlTable, Type: unix.RTN_UNREACHABLE,
			Protocol: netlink.RouteProtocol(IrvioRoutingProtocol), Family: family,
		})
	}
	return result
}

func desiredTunnelControlRules(config Config, guards map[int]bool) []netlink.Rule {
	mask := uint32(0xffffffff)
	result := make([]netlink.Rule, 0, len(guards))
	for family := range guards {
		rule := netlink.NewRule()
		rule.Protocol = IrvioRoutingProtocol
		rule.Family = family
		rule.Priority = config.TunnelControlRulePriority
		rule.Table = config.TunnelControlTable
		rule.Mark = config.TunnelControlMark
		rule.Mask = &mask
		result = append(result, *rule)
	}
	return result
}

func splitTableRoutes(routes []netlink.Route, table int) (selected, retained []netlink.Route) {
	for _, route := range routes {
		if route.Table == table {
			selected = append(selected, route)
		} else {
			retained = append(retained, route)
		}
	}
	return selected, retained
}

func splitTunnelControlRules(rules []netlink.Rule, config Config) (selected, retained []netlink.Rule) {
	for _, rule := range rules {
		if rule.Table == config.TunnelControlTable && rule.Priority == config.TunnelControlRulePriority && rule.Mark == config.TunnelControlMark {
			selected = append(selected, rule)
		} else {
			retained = append(retained, rule)
		}
	}
	return selected, retained
}

func hasTunnelControlRule(rules []netlink.Rule, wanted netlink.Rule) bool {
	for _, rule := range rules {
		if rule.Family == wanted.Family && rule.Table == wanted.Table && rule.Priority == wanted.Priority && rule.Mark == wanted.Mark {
			return true
		}
	}
	return false
}

func splitWANRoutes(routes []netlink.Route, table int) (wan, retained []netlink.Route) {
	for _, route := range routes {
		if route.Table == table {
			wan = append(wan, route)
		} else {
			retained = append(retained, route)
		}
	}
	return wan, retained
}

func hasRouteDestination(routes []netlink.Route, wanted netlink.Route) bool {
	for _, route := range routes {
		if route.Table == wanted.Table && route.Family == wanted.Family && routeDestination(route) == routeDestination(wanted) {
			return true
		}
	}
	return false
}

func routeDestination(route netlink.Route) string {
	if route.Dst == nil {
		return "default"
	}
	return route.Dst.String()
}

func buildRules(config Config) []netlink.Rule {
	mask := uint32(0xffffffff)
	rules := make([]netlink.Rule, 0, 8+len(config.Bypass))
	controlFamilies := make(map[int]bool)
	for _, route := range effectiveTunnelControlRoutes(config) {
		controlFamilies[tunnelControlFamily(route.Destination)] = true
	}
	for family := range controlFamilies {
		control := netlink.NewRule()
		control.Protocol = IrvioRoutingProtocol
		control.Family = family
		control.Priority = config.TunnelControlRulePriority
		control.Table = config.TunnelControlTable
		control.Mark = config.TunnelControlMark
		control.Mask = &mask
		rules = append(rules, *control)
	}
	for _, item := range []struct {
		user   bool
		outer  bool
		family int
	}{{config.ProtectIPv4 || config.RejectIPv4, config.OuterIPv4, netlink.FAMILY_V4}, {config.ProtectIPv6 || config.RejectIPv6, config.OuterIPv6, netlink.FAMILY_V6}} {
		if !item.user && !item.outer {
			continue
		}
		if item.outer {
			outer := netlink.NewRule()
			outer.Protocol = IrvioRoutingProtocol
			outer.Family = item.family
			outer.Priority = config.OuterRulePriority
			outer.Table = config.WANTable
			outer.Mark = config.OuterMark
			outer.Mask = &mask
			rules = append(rules, *outer)
			if config.ForwardedTUNName != "" {
				forwarded := netlink.NewRule()
				forwarded.Protocol = IrvioRoutingProtocol
				forwarded.Family = item.family
				forwarded.Priority = config.ForwardRulePriority
				forwarded.Table = config.WANTable
				forwarded.IifName = config.ForwardedTUNName
				rules = append(rules, *forwarded)
			}
		}
		if !item.user {
			continue
		}
		priority := config.BypassRulePriority
		for _, prefix := range config.Bypass {
			if prefix.Addr().Is4() != (item.family == netlink.FAMILY_V4) {
				continue
			}
			bypass := netlink.NewRule()
			bypass.Protocol = IrvioRoutingProtocol
			bypass.Family = item.family
			bypass.Priority = priority
			bypass.Table = unix.RT_TABLE_MAIN
			bypass.Dst = prefixIPNet(prefix)
			rules = append(rules, *bypass)
			priority++
		}
		protected := netlink.NewRule()
		protected.Protocol = IrvioRoutingProtocol
		protected.Family = item.family
		protected.Priority = config.ProtectedRulePriority
		protected.Table = config.OutTable
		protected.Mark = 0
		protected.Mask = &mask
		rules = append(rules, *protected)
	}
	return rules
}

func desiredPriorities(config Config, family int) map[int]struct{} {
	outer := family == netlink.FAMILY_V4 && config.OuterIPv4 || family == netlink.FAMILY_V6 && config.OuterIPv6
	user := family == netlink.FAMILY_V4 && (config.ProtectIPv4 || config.RejectIPv4) || family == netlink.FAMILY_V6 && (config.ProtectIPv6 || config.RejectIPv6)
	result := make(map[int]struct{})
	for _, route := range effectiveTunnelControlRoutes(config) {
		if route.Destination.Addr().Is4() == (family == netlink.FAMILY_V4) {
			result[config.TunnelControlRulePriority] = struct{}{}
			break
		}
	}
	if !user && !outer {
		return result
	}
	if outer {
		result[config.OuterRulePriority] = struct{}{}
		if config.ForwardedTUNName != "" {
			result[config.ForwardRulePriority] = struct{}{}
		}
	}
	if !user {
		return result
	}
	priority := config.BypassRulePriority
	for _, prefix := range config.Bypass {
		if prefix.Addr().Is4() == (family == netlink.FAMILY_V4) {
			result[priority] = struct{}{}
			priority++
		}
	}
	result[config.ProtectedRulePriority] = struct{}{}
	return result
}

func prefixIPNet(prefix netip.Prefix) *net.IPNet {
	if !prefix.IsValid() {
		return nil
	}
	prefix = prefix.Masked()
	bits := prefix.Addr().BitLen()
	return &net.IPNet{IP: addrIP(prefix.Addr()), Mask: net.CIDRMask(prefix.Bits(), bits)}
}

func defaultIPNet(family int) *net.IPNet {
	if family == netlink.FAMILY_V4 {
		return prefixIPNet(netip.MustParsePrefix("0.0.0.0/0"))
	}
	return prefixIPNet(netip.MustParsePrefix("::/0"))
}

func addrIP(addr netip.Addr) net.IP {
	if !addr.IsValid() {
		return nil
	}
	return net.IP(append([]byte(nil), addr.AsSlice()...))
}

func tunnelControlFamily(prefix netip.Prefix) int {
	if prefix.Addr().Is4() {
		return netlink.FAMILY_V4
	}
	return netlink.FAMILY_V6
}

// State reports the bounded installed resource count.
func (m *Manager) State() State {
	if m == nil {
		return State{Closed: true}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return State{
		OuterMark: m.config.OuterMark, OutTable: m.config.OutTable, WANTable: m.config.WANTable,
		TunnelControlMark: m.config.TunnelControlMark, TunnelControlTable: m.config.TunnelControlTable,
		Rules: len(m.rules), Routes: len(m.routes), Closed: m.closed,
	}
}

// Close removes only successfully installed Irvio state, in reverse order.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		m.closeErr = errors.Join(m.rollback(), m.lock.Close())
	})
	return m.closeErr
}

func (m *Manager) rollback() error {
	var errs []error
	for index := len(m.rules) - 1; index >= 0; index-- {
		rule := m.rules[index]
		if err := m.back.RuleDel(&rule); err != nil && !routingStateAbsent(err) {
			errs = append(errs, fmt.Errorf("delete policy rule priority %d: %w", rule.Priority, err))
		}
	}
	for index := len(m.routes) - 1; index >= 0; index-- {
		route := m.routes[index]
		if err := m.back.RouteDel(&route); err != nil && !routingStateAbsent(err) {
			errs = append(errs, fmt.Errorf("delete route: %w", err))
		}
	}
	m.rules = nil
	m.routes = nil
	return errors.Join(errs...)
}

func routingStateAbsent(err error) bool {
	return errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ESRCH)
}
