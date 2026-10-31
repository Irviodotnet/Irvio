// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/wfp"
)

func controlFixture() WindowsBackendConfig {
	c := windowsFixture()
	c.Network.TunnelLUID = 0
	c.Network.TunnelControl = []WindowsTunnelControl{
		{Interface: 99, Source: netip.MustParseAddr("10.77.0.2"), Remote: netip.MustParseAddrPort("10.77.0.1:51822")},
		{Interface: 99, Source: netip.MustParseAddr("fd77::2"), Remote: netip.MustParseAddrPort("[fd77::1]:51822")},
	}
	return c
}

func TestWindowsPreBindPolicy(t *testing.T) {
	c := controlFixture()
	rules, err := compileWindowsPolicy(c.Owner, c.Network)
	if err != nil {
		t.Fatal(err)
	}
	again, err := compileWindowsPolicy(c.Owner, c.Network)
	if err != nil || !slices.Equal(rules, again) {
		t.Fatal("non-deterministic pre-BIND policy", err)
	}
	grants := 0
	for _, r := range rules {
		if r.Priority == 200 {
			t.Fatal("ordinary tunnel traffic permitted before BIND")
		}
		if r.LocalInterface != 99 {
			continue
		}
		grants++
		i := 0
		if r.Layer == wfp.ConnectV6 || r.Layer == wfp.TransportV6 {
			i = 1
		}
		e := c.Network.TunnelControl[i]
		if r.Action != wfp.Permit || r.Priority != 400 || r.Protocol != 6 || r.LocalAddress != e.Source ||
			r.Remote != netip.PrefixFrom(e.Remote.Addr(), e.Remote.Addr().BitLen()) || r.RemotePort != e.Remote.Port() {
			t.Fatalf("broad control grant: %+v", r)
		}
		if r.Layer == wfp.ConnectV4 || r.Layer == wfp.ConnectV6 {
			if r.Application != c.Network.Application || r.User != wfp.SystemOrAdministrator {
				t.Fatal("control grant missing daemon authorization")
			}
		}
	}
	if grants != 4 {
		t.Fatalf("missing IPv4/IPv6 ALE/transport grants: %d", grants)
	}
}

func TestWindowsPreBindRejectsUnsafeGrants(t *testing.T) {
	for name, mutate := range map[string]func(*WindowsNetwork){
		"no-app":             func(n *WindowsNetwork) { n.Application = "" },
		"bad-app":            func(n *WindowsNetwork) { n.Application = "bad" },
		"full-tunnel":        func(n *WindowsNetwork) { n.TunnelLUID = 99 },
		"no-interface":       func(n *WindowsNetwork) { n.TunnelControl[0].Interface = 0 },
		"mixed-interface":    func(n *WindowsNetwork) { n.TunnelControl[0].Interface++ },
		"underlay-collision": func(n *WindowsNetwork) { n.Underlay[0].Interface = 99 },
		"same-address":       func(n *WindowsNetwork) { n.TunnelControl[0].Source = n.TunnelControl[0].Remote.Addr() },
		"wildcard-source":    func(n *WindowsNetwork) { n.TunnelControl[0].Source = netip.MustParseAddr("0.0.0.0") },
		"loopback":           func(n *WindowsNetwork) { n.TunnelControl[0].Remote = netip.MustParseAddrPort("127.0.0.1:51822") },
		"multicast":          func(n *WindowsNetwork) { n.TunnelControl[0].Remote = netip.MustParseAddrPort("224.0.0.1:51822") },
		"zero-port":          func(n *WindowsNetwork) { n.TunnelControl[0].Remote = netip.MustParseAddrPort("10.77.0.1:0") },
		"wrong-family":       func(n *WindowsNetwork) { n.TunnelControl[0].Source = netip.MustParseAddr("fd77::2") },
		"mapped-source":      func(n *WindowsNetwork) { n.TunnelControl[0].Source = netip.MustParseAddr("::ffff:10.77.0.2") },
		"zoned-remote":       func(n *WindowsNetwork) { n.TunnelControl[1].Remote = netip.MustParseAddrPort("[fd77::1%3]:51822") },
		"duplicate":          func(n *WindowsNetwork) { n.TunnelControl[1] = n.TunnelControl[0] },
		"too-many":           func(n *WindowsNetwork) { n.TunnelControl = append(n.TunnelControl, n.TunnelControl[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			c := controlFixture()
			mutate(&c.Network)
			if _, err := compileWindowsPolicy(c.Owner, c.Network); err == nil {
				t.Fatal("unsafe control grant accepted")
			}
		})
	}
}

func TestWindowsPreBindPromotionAndRevocation(t *testing.T) {
	c := controlFixture()
	s := &memoryWFP{weight: c.Owner.Weight}
	b, err := newWindowsBackend(c, s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	i, _, err := b.Ensure(ctx, c.Policy)
	if err != nil {
		t.Fatal(err)
	}
	u, err := b.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := slices.Clone(s.state.Rules)
	full := c.Network
	full.TunnelControl = nil
	full.TunnelLUID = 99
	s.fail = true
	if err := u.Update(ctx, full); err == nil || !slices.Equal(before, s.state.Rules) {
		t.Fatal("failed promotion changed pre-BIND protection")
	}
	s.fail = false
	if err := u.Update(ctx, full); err != nil {
		t.Fatal(err)
	}
	want, _ := compileWindowsPolicy(c.Owner, full)
	if !slices.Equal(want, s.state.Rules) {
		t.Fatal("promotion not atomic")
	}
	if err := u.Update(ctx, c.Network); err != nil {
		t.Fatal(err)
	}
	if err := i.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Ensure(ctx, c.Policy); err != nil {
		t.Fatal(err)
	}
	idle, _ := compileWindowsPolicy(c.Owner, WindowsNetwork{})
	if !slices.Equal(s.state.Rules, idle) {
		t.Fatal("next Connect resurrected a stale control endpoint")
	}
	if err := u.Update(ctx, c.Network); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("old updater retained authority", err)
	}
}

func TestWindowsPolicyIndependentLaneUpdates(t *testing.T) {
	c := controlFixture()
	s := &memoryWFP{weight: c.Owner.Weight}
	b, err := newWindowsBackend(c, s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _, err = b.Ensure(ctx, c.Policy)
	if err != nil {
		t.Fatal(err)
	}
	u, err := b.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	underlay := slices.Clone(c.Network.Underlay)
	underlay[0].Remote = netip.MustParseAddrPort("192.0.2.20:4000")
	if err := u.UpdateUnderlay(ctx, c.Network.Application, underlay); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.network.TunnelControl, c.Network.TunnelControl) {
		t.Fatal("underlay update clobbered pre-BIND")
	}
	if err := u.UpdateTunnel(ctx, 99, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.network.Underlay, underlay) || b.network.TunnelLUID != 99 {
		t.Fatal("promotion lost fresh underlay")
	}
	underlay[0].LocalPort = 54321
	if b.network.Underlay[0].LocalPort != 0 {
		t.Fatal("caller mutated stored policy")
	}
	if err := u.UpdateTunnel(ctx, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := u.UpdateUnderlay(ctx, c.Network.Application, underlay); err != nil {
		t.Fatal(err)
	}
	if b.network.TunnelLUID != 0 || len(b.network.TunnelControl) != 0 {
		t.Fatal("underlay update resurrected tunnel")
	}
}
