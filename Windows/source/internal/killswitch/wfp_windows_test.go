// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"slices"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/safetypolicy"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"golang.org/x/sys/windows"
)

type memoryWFP struct {
	state  wfp.Snapshot
	weight uint16
	fail   bool
}

func (s *memoryWFP) Inspect(context.Context) (wfp.Snapshot, error) {
	r := s.state
	r.Rules = slices.Clone(r.Rules)
	return r, nil
}
func (s *memoryWFP) Remove(context.Context) error {
	if s.fail {
		return errors.New("injected remove failure")
	}
	s.state = wfp.Snapshot{}
	return nil
}
func (s *memoryWFP) Replace(_ context.Context, r []wfp.Rule) error {
	if s.fail {
		return errors.New("injected transaction failure")
	}
	s.state = wfp.Snapshot{Installed: true, Active: true, SublayerWeight: s.weight, Rules: slices.Clone(r)}
	return nil
}

func (s *memoryWFP) ReplaceInstalled(ctx context.Context, r []wfp.Rule) error {
	if !s.state.Installed {
		return wfp.ErrNotInstalled
	}
	return s.Replace(ctx, r)
}

type closeFunction func() error

func (f closeFunction) Close() error { return f() }

func windowsFixture() WindowsBackendConfig {
	return WindowsBackendConfig{Policy: Policy{TUNName: "irvio0", BypassMark: 1}, Owner: wfp.Config{Provider: windows.GUID{Data1: 100}, Sublayer: windows.GUID{Data1: 101}, Name: "test", Marker: "irvio-test-windows-policy", ServiceName: "IRVIO-W05-Owner", Weight: 49200}, Network: WindowsNetwork{Application: "a\x00\x00\x00", TunnelLUID: 99, Underlay: []WindowsBypass{{Interface: 10, Source: netip.MustParseAddr("192.0.2.2"), Remote: netip.MustParseAddrPort("192.0.2.10:4000"), Protocol: 6}}}}
}

func TestWindowsPolicyCompiler(t *testing.T) {
	c := windowsFixture()
	rules, err := compileWindowsPolicy(c.Owner, c.Network)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := compileWindowsPolicy(c.Owner, c.Network)
	if !slices.Equal(rules, again) {
		t.Fatal("rule identity not deterministic")
	}
	seen := map[windows.GUID]bool{}
	defaults, loopback, tunnels, dhcp, nd := 0, 0, 0, 0, 0
	for _, r := range rules {
		if seen[r.Key] {
			t.Fatal("duplicate key")
		}
		seen[r.Key] = true
		if r.Priority == 0 {
			defaults++
			if r.Action != wfp.Block || r.LocalInterface != 0 || r.Remote.IsValid() || r.Protocol != 0 {
				t.Fatal("default is not family-wide deny")
			}
		}
		if r.LoopbackOnly {
			loopback++
			if r.Priority != 500 {
				t.Fatal("loopback precedence")
			}
		}
		if r.LocalInterface == 99 {
			tunnels++
			if r.Priority != 200 || r.Action != wfp.Permit {
				t.Fatal("tunnel permit")
			}
		}
		if r.Protocol == 17 && (r.LocalPort == 68 || r.LocalPort == 546) {
			dhcp++
			if (r.Layer == wfp.ConnectV4 || r.Layer == wfp.ConnectV6) && (r.User != wfp.DHCPService || r.Application == "") {
				t.Fatal("DHCP lacks service and executable restriction")
			}
		}
		if r.ICMP {
			nd++
			if r.Protocol != 58 || r.ICMPType < 133 || r.ICMPType > 136 || r.ICMPCode != 0 {
				t.Fatal("wide ICMP permission")
			}
		}
		if r.Priority == 400 {
			if r.LocalAddress != c.Network.Underlay[0].Source || r.Remote.Bits() != 32 || r.RemotePort != 4000 || r.LocalInterface != 10 {
				t.Fatal("wide control grant")
			}
			if r.Layer == wfp.ConnectV4 && (r.Application == "" || r.User != wfp.SystemOrAdministrator) {
				t.Fatal("control grant lacks application or privilege restriction")
			}
		}
	}
	if defaults != 4 || loopback != 4 || tunnels != 4 || dhcp != 4 || nd != 8 {
		t.Fatalf("coverage %d %d %d %d %d", defaults, loopback, tunnels, dhcp, nd)
	}
	for _, l := range []windows.GUID{wfp.ConnectV4, wfp.ConnectV6, wfp.TransportV4, wfp.TransportV6} {
		for _, p := range []safetypolicy.Transport{safetypolicy.TransportTCP, safetypolicy.TransportUDP} {
			proto := uint8(6)
			if p == safetypolicy.TransportUDP {
				proto = 17
			}
			for _, port := range safetypolicy.Ports(safetypolicy.V0, p) {
				if !slices.ContainsFunc(rules, func(r wfp.Rule) bool {
					return r.Layer == l && r.Protocol == proto && r.RemotePort == port && r.Action == wfp.Block && r.Priority == 300
				}) {
					t.Fatal("missing canonical Safety Policy rule")
				}
			}
		}
	}
}

func TestWindowsPolicyRejectsBroadGrants(t *testing.T) {
	for _, mutate := range []func(*WindowsBackendConfig){
		func(c *WindowsBackendConfig) { c.Network.Application = "" },
		func(c *WindowsBackendConfig) { c.Network.Application = "bad" },
		func(c *WindowsBackendConfig) { c.Network.Underlay[0].Interface = 0 },
		func(c *WindowsBackendConfig) { c.Network.Underlay[0].Interface = c.Network.TunnelLUID },
		func(c *WindowsBackendConfig) { c.Network.Underlay[0].Remote = netip.MustParseAddrPort("192.0.2.10:0") },
		func(c *WindowsBackendConfig) { c.Network.Underlay[0].Source = netip.MustParseAddr("0.0.0.0") },
		func(c *WindowsBackendConfig) { c.Network.Underlay[0].Protocol = 0 },
		func(c *WindowsBackendConfig) { c.Network.Underlay = append(c.Network.Underlay, c.Network.Underlay[0]) },
	} {
		c := windowsFixture()
		mutate(&c)
		if _, err := compileWindowsPolicy(c.Owner, c.Network); err == nil {
			t.Fatal("unsafe grant accepted")
		}
	}
}

func TestWindowsControllerLifecycle(t *testing.T) {
	c := windowsFixture()
	s := &memoryWFP{weight: c.Owner.Weight}
	b, err := newWindowsBackend(c, s)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := New(Config{Backend: b, Policy: c.Policy})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gen, err := controller.Protect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Activate(ctx, gen, func(context.Context) (io.Closer, error) {
		if !s.state.Installed {
			t.Fatal("resource built before protection")
		}
		return closeFunction(func() error {
			if !s.state.Installed {
				t.Fatal("protection removed before resource close")
			}
			return nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Transition(gen, StateConnected); err != nil {
		t.Fatal(err)
	}
	if err := controller.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if s.state.Installed {
		t.Fatal("disconnect residue")
	}
	if _, err := b.NetworkUpdater(ctx); !errors.Is(err, ErrNotProtected) {
		t.Fatal(err)
	}
}

func TestWindowsAdoptionReconcileAndFencing(t *testing.T) {
	c := windowsFixture()
	s := &memoryWFP{weight: c.Owner.Weight}
	b, _ := newWindowsBackend(c, s)
	ctx := context.Background()
	i, result, err := b.Ensure(ctx, c.Policy)
	if err != nil || result != EnsureInstalled {
		t.Fatalf("fresh %v %v", result, err)
	}
	stale, err := b.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, result, err = b.Ensure(ctx, c.Policy)
	if err != nil || result != EnsureAdopted {
		t.Fatalf("adopt %v %v", result, err)
	}
	if err := i.Remove(ctx); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("stale installation removed current policy")
	}
	if err := stale.Update(ctx, c.Network); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("stale updater changed policy")
	}
	s.state.Rules = s.state.Rules[1:]
	i, result, err = b.Ensure(ctx, c.Policy)
	if err != nil || result != EnsureReconciled {
		t.Fatalf("repair %v %v", result, err)
	}
	old := slices.Clone(s.state.Rules)
	updater, err := b.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.fail = true
	n := c.Network
	n.TunnelLUID = 0
	if err := updater.Update(ctx, n); err == nil {
		t.Fatal("failed transaction accepted")
	}
	if !slices.Equal(old, s.state.Rules) {
		t.Fatal("failed update lost old policy")
	}
	if err := i.Remove(ctx); err == nil {
		t.Fatal("failed removal accepted")
	}
	s.fail = false
	if err := updater.Update(ctx, n); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(s.state.Rules, func(r wfp.Rule) bool { return r.LocalInterface == 99 }) {
		t.Fatal("stale tunnel grant")
	}
	s.state = wfp.Snapshot{}
	if err := i.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := i.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if err := updater.Update(ctx, c.Network); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("removed updater resurrected protection")
	}
	if _, _, err := b.Ensure(ctx, c.Policy); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(s.state.Rules, func(r wfp.Rule) bool { return r.LocalInterface != 0 || r.Priority == 400 }) {
		t.Fatal("next Connect reused previous interface/grants")
	}
}
