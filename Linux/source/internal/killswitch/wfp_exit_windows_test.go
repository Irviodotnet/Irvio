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

func exitControlFixture() WindowsBackendConfig {
	c := controlFixture()
	c.Network.ExitControl = &WindowsExitControl{Interface: 99, Local: netip.MustParseAddrPort("10.78.0.1:7890"), Clients: netip.MustParsePrefix("10.78.0.0/24")}
	return c
}

func TestWindowsExitControlGrantIsNarrow(t *testing.T) {
	c := exitControlFixture()
	rules, err := compileWindowsPolicy(c.Owner, c.Network)
	if err != nil {
		t.Fatal(err)
	}
	again, err := compileWindowsPolicy(c.Owner, c.Network)
	if err != nil || !slices.Equal(rules, again) {
		t.Fatal("nondeterministic Exit policy", err)
	}
	count := 0
	for _, r := range rules {
		if r.Priority == 200 {
			t.Fatal("unbound ordinary tunnel permitted")
		}
		if r.LocalAddress != c.Network.ExitControl.Local.Addr() {
			continue
		}
		count++
		if r.Action != wfp.Permit || r.Priority != 400 || r.Protocol != 6 || r.LocalInterface != 99 ||
			r.LocalPort != 7890 || r.Remote != c.Network.ExitControl.Clients || r.RemotePort != 0 ||
			r.Layer != wfp.ConnectV4 && r.Layer != wfp.TransportV4 {
			t.Fatalf("broad Exit grant: %+v", r)
		}
		if r.Layer == wfp.ConnectV4 && (r.Application != c.Network.Application || r.User != wfp.SystemOrAdministrator) {
			t.Fatal("missing daemon authorization")
		}
	}
	if count != 2 {
		t.Fatal("missing Exit control grants", count)
	}
}

func TestWindowsExitControlRejectsUnsafeGrants(t *testing.T) {
	for name, change := range map[string]func(*WindowsNetwork){
		"no-interface":             func(n *WindowsNetwork) { n.ExitControl.Interface = 0 },
		"no-app":                   func(n *WindowsNetwork) { n.Application = "" },
		"no-port":                  func(n *WindowsNetwork) { n.ExitControl.Local = netip.MustParseAddrPort("10.78.0.1:0") },
		"public-source":            func(n *WindowsNetwork) { n.ExitControl.Local = netip.MustParseAddrPort("1.1.1.1:7890") },
		"mapped-source":            func(n *WindowsNetwork) { n.ExitControl.Local = netip.MustParseAddrPort("[::ffff:10.78.0.1]:7890") },
		"source-outside":           func(n *WindowsNetwork) { n.ExitControl.Local = netip.MustParseAddrPort("10.79.0.1:7890") },
		"wildcard-pool":            func(n *WindowsNetwork) { n.ExitControl.Clients = netip.MustParsePrefix("0.0.0.0/0") },
		"noncanonical-pool":        func(n *WindowsNetwork) { n.ExitControl.Clients = netip.MustParsePrefix("10.78.0.1/24") },
		"v6-pool":                  func(n *WindowsNetwork) { n.ExitControl.Clients = netip.MustParsePrefix("fd78::/64") },
		"oversized-pool":           func(n *WindowsNetwork) { n.ExitControl.Clients = netip.MustParsePrefix("10.78.0.0/15") },
		"undersized-pool":          func(n *WindowsNetwork) { n.ExitControl.Clients = netip.MustParsePrefix("10.78.0.0/31") },
		"different-prebind-device": func(n *WindowsNetwork) { n.ExitControl.Interface = 100 },
		"different-active-device":  func(n *WindowsNetwork) { n.TunnelControl = nil; n.TunnelLUID = 100 },
		"physical-collision":       func(n *WindowsNetwork) { n.Underlay[0].Interface = 99 },
	} {
		t.Run(name, func(t *testing.T) {
			c := exitControlFixture()
			change(&c.Network)
			if _, err := compileWindowsPolicy(c.Owner, c.Network); err == nil {
				t.Fatal("unsafe Exit grant accepted")
			}
		})
	}
}

func TestWindowsExitControlIndependentLifetime(t *testing.T) {
	c := exitControlFixture()
	want := *c.Network.ExitControl
	s := &memoryWFP{weight: c.Owner.Weight}
	b, err := newWindowsBackend(c, s)
	if err != nil {
		t.Fatal(err)
	}
	c.Network.ExitControl.Local = netip.MustParseAddrPort("10.78.0.2:7891")
	if *b.network.ExitControl != want {
		t.Fatal("constructor retained caller pointer")
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
	if err := u.UpdateTunnel(ctx, 99, nil); err != nil {
		t.Fatal(err)
	}
	if *b.network.ExitControl != want {
		t.Fatal("upstream promotion lost Exit listener")
	}
	if err := u.UpdateTunnel(ctx, 0, nil); err != nil {
		t.Fatal(err)
	}
	if *b.network.ExitControl != want {
		t.Fatal("own NO_EXIT lost downstream control")
	}
	if err := u.UpdateUnderlay(ctx, c.Network.Application, c.Network.Underlay); err != nil {
		t.Fatal(err)
	}
	if *b.network.ExitControl != want {
		t.Fatal("underlay update lost downstream control")
	}
	before := slices.Clone(s.state.Rules)
	s.fail = true
	if err := u.UpdateExitControl(ctx, nil); err == nil || !slices.Equal(before, s.state.Rules) || *b.network.ExitControl != want {
		t.Fatal("failed revoke mutated state", err)
	}
	s.fail = false
	if err := u.UpdateExitControl(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if b.network.ExitControl != nil || !slices.Equal(b.network.Underlay, c.Network.Underlay) {
		t.Fatal("Exit revoke damaged other lane")
	}
	if err := u.UpdateExitControl(ctx, &want); err != nil {
		t.Fatal(err)
	}
	want.Local = netip.MustParseAddrPort("10.78.0.2:9999")
	if b.network.ExitControl.Local == want.Local {
		t.Fatal("updater retained caller pointer")
	}
	if err := i.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Ensure(ctx, c.Policy); err != nil {
		t.Fatal(err)
	}
	if b.network.ExitControl != nil {
		t.Fatal("new Connect resurrected listener grant")
	}
	if err := u.UpdateExitControl(ctx, &want); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("old updater retained authority", err)
	}
}
