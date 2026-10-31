// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package linuxroute

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type fakeBackend struct {
	rules       []netlink.Rule
	routes      []netlink.Route
	adds        int
	failAt      int
	ruleDelErr  error
	routeDelErr error
}

func (f *fakeBackend) RuleList(int) ([]netlink.Rule, error) {
	return append([]netlink.Rule(nil), f.rules...), nil
}
func (f *fakeBackend) RuleAdd(v *netlink.Rule) error {
	f.adds++
	if f.failAt == f.adds {
		return errors.New("injected")
	}
	f.rules = append(f.rules, *v)
	return nil
}
func (f *fakeBackend) RuleDel(v *netlink.Rule) error {
	if f.ruleDelErr != nil {
		return f.ruleDelErr
	}
	for i := len(f.rules) - 1; i >= 0; i-- {
		if f.rules[i].Priority == v.Priority {
			f.rules = append(f.rules[:i], f.rules[i+1:]...)
			return nil
		}
	}
	return nil
}
func (f *fakeBackend) RouteListFiltered(_ int, filter *netlink.Route, _ uint64) ([]netlink.Route, error) {
	var out []netlink.Route
	for _, v := range f.routes {
		if v.Table == filter.Table {
			out = append(out, v)
		}
	}
	return out, nil
}
func (f *fakeBackend) RouteAdd(v *netlink.Route) error {
	f.adds++
	if f.failAt == f.adds {
		return errors.New("injected")
	}
	f.routes = append(f.routes, *v)
	return nil
}
func (f *fakeBackend) RouteReplace(v *netlink.Route) error {
	f.adds++
	if f.failAt == f.adds {
		return errors.New("injected")
	}
	for index := range f.routes {
		if f.routes[index].Table == v.Table && f.routes[index].Family == v.Family && routeDestination(f.routes[index]) == routeDestination(*v) {
			f.routes[index] = *v
			return nil
		}
	}
	f.routes = append(f.routes, *v)
	return nil
}
func (f *fakeBackend) RouteDel(v *netlink.Route) error {
	if f.routeDelErr != nil {
		return f.routeDelErr
	}
	for i := len(f.routes) - 1; i >= 0; i-- {
		if f.routes[i].Table == v.Table && f.routes[i].LinkIndex == v.LinkIndex && routeDestination(f.routes[i]) == routeDestination(*v) {
			f.routes = append(f.routes[:i], f.routes[i+1:]...)
			return nil
		}
	}
	return nil
}

func TestCloseAcceptsKernelRemovedState(t *testing.T) {
	b := &fakeBackend{}
	m, err := apply(testConfig(), b)
	if err != nil {
		t.Fatal(err)
	}
	b.rules = nil
	b.routes = nil
	b.ruleDelErr = unix.ENOENT
	b.routeDelErr = unix.ESRCH
	if err := m.Close(); err != nil {
		t.Fatalf("kernel-removed state made cleanup non-idempotent: %v", err)
	}
}

func testConfig() Config {
	c := DefaultConfig()
	c.TUNIndex = 8
	c.TUNName = "hydra0"
	c.ProtectIPv4 = true
	c.TunnelIPv4Source = netip.MustParseAddr("10.77.0.2")
	c.OuterIPv4 = true
	c.WANIPv4 = WANPath{LinkIndex: 9, Source: netip.MustParseAddr("192.0.2.2"), Gateway: netip.MustParseAddr("192.0.2.1"), Connected: netip.MustParsePrefix("192.0.2.0/24")}
	c.Bypass = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	return c
}

func TestApplyAndCloseOwnExactState(t *testing.T) {
	b := &fakeBackend{}
	m, err := apply(testConfig(), b)
	if err != nil {
		t.Fatal(err)
	}
	if state := m.State(); state.Routes != 3 || state.Rules != 3 || state.Closed {
		t.Fatalf("state=%+v", state)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.rules) != 0 || len(b.routes) != 0 {
		t.Fatalf("residue rules=%d routes=%d", len(b.rules), len(b.routes))
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileWANReplacesOnlyMarkedEgressRoutes(t *testing.T) {
	backend := &fakeBackend{}
	manager, err := apply(testConfig(), backend)
	if err != nil {
		t.Fatal(err)
	}
	oldRoutes := manager.State().Routes
	path := WANPath{
		LinkIndex: 10, Source: netip.MustParseAddr("198.51.100.3"),
		Gateway: netip.MustParseAddr("198.51.100.1"), Connected: netip.MustParsePrefix("198.51.100.0/24"),
	}
	if err := manager.ReconcileWAN(path, WANPath{}); err != nil {
		t.Fatal(err)
	}
	if manager.State().Routes != oldRoutes {
		t.Fatalf("WAN reconciliation changed route ownership count: before=%d after=%d", oldRoutes, manager.State().Routes)
	}
	var wanDefault, wanConnected, protected bool
	for _, route := range backend.routes {
		if route.Table == DefaultWANTable && routeDestination(route) == "0.0.0.0/0" {
			wanDefault = route.LinkIndex == path.LinkIndex && route.Src.String() == path.Source.String()
		}
		if route.Table == DefaultWANTable && routeDestination(route) == path.Connected.String() {
			wanConnected = route.LinkIndex == path.LinkIndex && route.Src.String() == path.Source.String()
		}
		if route.Table == DefaultOutTable {
			protected = true
		}
	}
	if !wanDefault || !wanConnected || !protected {
		t.Fatalf("WAN reconciliation lost route classes: default=%t connected=%t protected=%t routes=%+v", wanDefault, wanConnected, protected, backend.routes)
	}
}

func TestBypassDestinationsPrecedeMarkedWANRule(t *testing.T) {
	rules := buildRules(testConfig())
	var bypassPriority, outerPriority int
	for _, rule := range rules {
		switch {
		case rule.Dst != nil:
			bypassPriority = rule.Priority
		case rule.Mark == DefaultOuterMark:
			outerPriority = rule.Priority
		}
	}
	if bypassPriority == 0 || outerPriority == 0 || bypassPriority >= outerPriority {
		t.Fatalf("control/LAN bypass must precede marked WAN routing: bypass=%d outer=%d", bypassPriority, outerPriority)
	}
}

func TestProtectedRouteCarriesTunnelLeaseSource(t *testing.T) {
	routes := buildRoutes(testConfig())
	for _, route := range routes {
		if route.Table == DefaultOutTable && route.Dst != nil && route.Dst.String() == "0.0.0.0/0" {
			if got := route.Src.String(); got != "10.77.0.2" {
				t.Fatalf("protected route source=%s", got)
			}
			return
		}
	}
	t.Fatal("protected IPv4 default route missing")
}

func TestApplyRollsBackPartialFailure(t *testing.T) {
	for failAt := 1; failAt <= 6; failAt++ {
		b := &fakeBackend{failAt: failAt}
		m, err := apply(testConfig(), b)
		if err == nil {
			if m != nil {
				_ = m.Close()
			}
			continue
		}
		if len(b.rules) != 0 || len(b.routes) != 0 {
			t.Fatalf("failure %d left rules=%d routes=%d", failAt, len(b.rules), len(b.routes))
		}
	}
}

func TestApplyRejectsIdentifierCollision(t *testing.T) {
	b := &fakeBackend{rules: []netlink.Rule{{Priority: DefaultOuterPriority}}}
	if _, err := apply(testConfig(), b); !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v", err)
	}
}

func TestApplyReconcilesOnlyOwnedCrashResidue(t *testing.T) {
	b := &fakeBackend{}
	if _, err := apply(testConfig(), b); err != nil {
		t.Fatal(err)
	}
	var retained []netlink.Route
	for _, route := range b.routes {
		if route.LinkIndex != testConfig().TUNIndex {
			retained = append(retained, route)
		}
	}
	b.routes = retained
	manager, err := apply(testConfig(), b)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range b.rules {
		if rule.Protocol != IrvioRoutingProtocol {
			t.Fatalf("unowned rule survived reconciliation: %+v", rule)
		}
	}
	for _, route := range b.routes {
		if route.Protocol != netlink.RouteProtocol(IrvioRoutingProtocol) {
			t.Fatalf("unowned route survived reconciliation: %+v", route)
		}
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupCrashResidueWithoutTUNRemovesControlAndWANState(t *testing.T) {
	config := withDefaults(DefaultConfig())
	back := &fakeBackend{
		rules: []netlink.Rule{
			{Priority: config.TunnelControlRulePriority, Protocol: IrvioRoutingProtocol},
			{Priority: config.OuterRulePriority, Protocol: IrvioRoutingProtocol},
			{Priority: config.ProtectedRulePriority, Protocol: IrvioRoutingProtocol},
			{Priority: 30000, Protocol: unix.RTPROT_STATIC},
		},
		routes: []netlink.Route{
			{Table: config.WANTable, Protocol: netlink.RouteProtocol(IrvioRoutingProtocol), LinkIndex: 2},
			{Table: config.TunnelControlTable, Protocol: netlink.RouteProtocol(IrvioRoutingProtocol), LinkIndex: 2},
			{Table: config.OutTable, Protocol: netlink.RouteProtocol(IrvioRoutingProtocol), LinkIndex: 2},
			{Table: 1234, Protocol: unix.RTPROT_STATIC, LinkIndex: 2},
		},
	}
	manager := &Manager{config: config, back: back}
	if err := manager.reconcileOwnedResidue(true); err != nil {
		t.Fatal(err)
	}
	if len(back.rules) != 1 || back.rules[0].Priority != 30000 || len(back.routes) != 1 || back.routes[0].Table != 1234 {
		t.Fatalf("crash residue cleanup retained owned state or removed foreign state: rules=%+v routes=%+v", back.rules, back.routes)
	}
}

func TestCleanupCrashResidueRejectsForeignReservedRouteBeforeDeleting(t *testing.T) {
	config := withDefaults(DefaultConfig())
	back := &fakeBackend{
		rules:  []netlink.Rule{{Priority: config.OuterRulePriority, Protocol: IrvioRoutingProtocol}},
		routes: []netlink.Route{{Table: config.WANTable, Protocol: unix.RTPROT_STATIC, LinkIndex: 2}},
	}
	manager := &Manager{config: config, back: back}
	if err := manager.reconcileOwnedResidue(true); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign route collision error = %v", err)
	}
	if len(back.rules) != 1 || len(back.routes) != 1 {
		t.Fatal("foreign route collision partially removed state")
	}
}

func TestApplyRejectsForeignRouteInReservedTable(t *testing.T) {
	b := &fakeBackend{routes: []netlink.Route{{Table: DefaultWANTable, Protocol: unix.RTPROT_STATIC}}}
	if _, err := apply(testConfig(), b); !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v", err)
	}
}

func TestRouteLockRejectsSecondLiveOwnerAndReleases(t *testing.T) {
	first, err := acquireRouteLock()
	if err != nil {
		t.Fatal(err)
	}
	if second, err := acquireRouteLock(); !errors.Is(err, ErrConflict) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second live route owner error=%v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireRouteLock()
	if err != nil {
		t.Fatal(err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnsupportedFamilyGetsExplicitUnreachable(t *testing.T) {
	config := testConfig()
	config.RejectIPv6 = true
	found := false
	for _, route := range buildRoutes(config) {
		if route.Family == netlink.FAMILY_V6 && route.Type != 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("missing IPv6 unreachable route")
	}
}

func TestDualRoleRoutesForwardedIrvioTrafficToPhysicalWAN(t *testing.T) {
	config := testConfig()
	config.ForwardedTUNName = config.TUNName
	rules := buildRules(config)
	var forwarded *netlink.Rule
	for index := range rules {
		if rules[index].Priority == DefaultForwardPriority {
			forwarded = &rules[index]
		}
	}
	if forwarded == nil {
		t.Fatal("missing dual-role forwarded-traffic rule")
	}
	if forwarded.IifName != config.TUNName || forwarded.Table != IrvioWANTable || forwarded.Mark != 0 {
		t.Fatalf("forwarded rule=%+v", *forwarded)
	}
	if !(DefaultOuterPriority < forwarded.Priority && forwarded.Priority < DefaultProtectedPriority) {
		t.Fatalf("forward priority %d does not precede protected routing", forwarded.Priority)
	}
}

func TestOuterIPv4CanCarryProtectedIPv6(t *testing.T) {
	config := testConfig()
	config.ProtectIPv4 = false
	config.ProtectIPv6 = true
	routes := buildRoutes(config)
	var wan4, tunnel6 bool
	for _, route := range routes {
		wan4 = wan4 || route.Table == config.WANTable && route.LinkIndex == config.WANIPv4.LinkIndex
		tunnel6 = tunnel6 || route.Table == config.OutTable && route.Family == netlink.FAMILY_V6
	}
	if !wan4 || !tunnel6 {
		t.Fatalf("missing cross-family policy: %+v", routes)
	}
	rules := buildRules(config)
	var marked4, protected6 bool
	for _, rule := range rules {
		marked4 = marked4 || rule.Family == netlink.FAMILY_V4 && rule.Mark == config.OuterMark && rule.Table == config.WANTable
		protected6 = protected6 || rule.Family == netlink.FAMILY_V6 && rule.Table == config.OutTable
	}
	if !marked4 || !protected6 {
		t.Fatalf("missing cross-family rules: %+v", rules)
	}
}

func TestTunnelControlHasExactIrvioRouteAndUnreachableDefault(t *testing.T) {
	config := testConfig()
	config.TunnelIPv6Source = netip.MustParseAddr("fd42::2")
	config.TunnelControl = []netip.Prefix{
		netip.MustParsePrefix("10.77.0.1/32"),
		netip.MustParsePrefix("fd42::1/128"),
	}
	routes := buildRoutes(config)
	rules := buildRules(config)
	var exact4, exact6, reject4, reject6, rule4, rule6 bool
	for _, route := range routes {
		if route.Table != IrvioTunnelControlTable {
			continue
		}
		switch routeDestination(route) {
		case "10.77.0.1/32":
			exact4 = route.LinkIndex == config.TUNIndex && route.Src.String() == "10.77.0.2"
		case "fd42::1/128":
			exact6 = route.LinkIndex == config.TUNIndex && route.Src.String() == "fd42::2"
		case "0.0.0.0/0":
			reject4 = route.Type == unix.RTN_UNREACHABLE
		case "::/0":
			reject6 = route.Type == unix.RTN_UNREACHABLE
		}
	}
	for _, rule := range rules {
		if rule.Mark != IrvioTunnelControlMark || rule.Table != IrvioTunnelControlTable {
			continue
		}
		rule4 = rule4 || rule.Family == netlink.FAMILY_V4
		rule6 = rule6 || rule.Family == netlink.FAMILY_V6
	}
	if !exact4 || !exact6 || !reject4 || !reject6 || !rule4 || !rule6 {
		t.Fatalf("TUNNEL_ONLY incomplete: exact4=%t exact6=%t reject4=%t reject6=%t rule4=%t rule6=%t", exact4, exact6, reject4, reject6, rule4, rule6)
	}
}

func TestTunnelControlRequiresLeaseOwnedSource(t *testing.T) {
	config := testConfig()
	config.TunnelIPv4Source = netip.Addr{}
	config.TunnelControl = []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")}
	if err := validateConfig(withDefaults(config)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("TUNNEL_ONLY without a lease-owned source was accepted: %v", err)
	}
}

func TestReconcileTunnelControlOwnsDualRoleSourcesAndKeepsFailClosedGuard(t *testing.T) {
	backend := &fakeBackend{}
	config := testConfig()
	config.TunnelControl = []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")}
	manager, err := apply(config, backend)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	upstream := TunnelControlRoute{
		Destination: netip.MustParsePrefix("10.77.0.1/32"), Source: netip.MustParseAddr("10.77.0.2"),
	}
	downstream := TunnelControlRoute{
		Destination: netip.MustParsePrefix("10.88.0.2/32"), Source: netip.MustParseAddr("10.88.0.1"),
	}
	if err := manager.ReconcileTunnelControl([]TunnelControlRoute{upstream, downstream}); err != nil {
		t.Fatal(err)
	}
	var upstreamOK, downstreamOK bool
	for _, route := range backend.routes {
		if route.Table != IrvioTunnelControlTable {
			continue
		}
		switch routeDestination(route) {
		case upstream.Destination.String():
			upstreamOK = route.Src.String() == upstream.Source.String()
		case downstream.Destination.String():
			downstreamOK = route.Src.String() == downstream.Source.String()
		}
	}
	if !upstreamOK || !downstreamOK {
		t.Fatalf("dual-role route sources upstream=%t downstream=%t routes=%+v", upstreamOK, downstreamOK, backend.routes)
	}
	if err := manager.ReconcileTunnelControl([]TunnelControlRoute{upstream}); err != nil {
		t.Fatal(err)
	}
	var stale, guard bool
	for _, route := range backend.routes {
		stale = stale || route.Table == IrvioTunnelControlTable && routeDestination(route) == downstream.Destination.String()
		guard = guard || route.Table == IrvioTunnelControlTable && routeDestination(route) == "0.0.0.0/0" && route.Type == unix.RTN_UNREACHABLE
	}
	if stale || !guard {
		t.Fatalf("revoked route stale=%t fail-closed guard=%t routes=%+v", stale, guard, backend.routes)
	}
}

func TestTunnelControlSourceAllowedDuringFailClosedBootstrap(t *testing.T) {
	config := testConfig()
	config.ProtectIPv4 = false
	config.RejectIPv4 = true
	config.TunnelIPv4Source = netip.MustParseAddr("10.77.0.2")
	config.TunnelControlRoutes = []TunnelControlRoute{{
		Destination: netip.MustParsePrefix("10.77.0.1/32"),
		Source:      netip.MustParseAddr("10.77.0.2"),
	}}

	manager, err := apply(config, &fakeBackend{})
	if err != nil {
		t.Fatalf("apply fail-closed bootstrap routes: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close fail-closed bootstrap routes: %v", err)
	}
}

func TestReconcileTunnelControlRollsBackFailedUpdate(t *testing.T) {
	backend := &fakeBackend{}
	config := testConfig()
	config.TunnelControl = []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")}
	manager, err := apply(config, backend)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	beforeRoutes := append([]netlink.Route(nil), backend.routes...)
	backend.failAt = backend.adds + 2
	err = manager.ReconcileTunnelControl([]TunnelControlRoute{
		{Destination: netip.MustParsePrefix("10.77.0.1/32"), Source: netip.MustParseAddr("10.77.0.2")},
		{Destination: netip.MustParsePrefix("10.88.0.2/32"), Source: netip.MustParseAddr("10.88.0.1")},
	})
	if err == nil {
		t.Fatal("injected tunnel-control update failure was accepted")
	}
	if len(backend.routes) != len(beforeRoutes) {
		t.Fatalf("rollback route count=%d want=%d", len(backend.routes), len(beforeRoutes))
	}
	for _, route := range backend.routes {
		if route.Table == IrvioTunnelControlTable && routeDestination(route) == "10.88.0.2/32" {
			t.Fatalf("failed update retained downstream route: %+v", route)
		}
	}
}
