// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/wintest"
	"golang.org/x/sys/windows"
)

func TestExactPrefix(t *testing.T) {
	want := netip.MustParsePrefix("10.17.0.0/24")
	for _, text := range []string{"10.17.0.0/24", "10.17.0.0/255.255.255.0"} {
		if !samePrefix(text, want) {
			t.Fatalf("equivalent prefix rejected: %q", text)
		}
	}
	for _, text := range []string{"*", "LocalSubnet", "10.17.0.0/16", "10.17.0.1/24", "10.17.0.0/255.0.255.0", "10.17.0.0/24,10.18.0.0/24"} {
		if samePrefix(text, want) {
			t.Fatalf("foreign prefix accepted: %q", text)
		}
	}
}

func TestControlRejectsInvalidScope(t *testing.T) {
	id := windows.GUID{Data1: 1}
	for _, tc := range []struct {
		endpoint string
		pool     string
	}{
		{"0.0.0.0:7890", "10.17.0.0/24"},
		{"1.1.1.1:7890", "1.1.1.0/24"},
		{"10.17.0.1:0", "10.17.0.0/24"},
		{"10.17.0.1:7890", "10.0.0.0/8"},
		{"10.17.0.1:7890", "10.17.0.1/24"},
		{"10.17.0.1:7890", "10.18.0.0/24"},
		{"[fd00::1]:7890", "fd00::/64"},
	} {
		if c, err := NewControl(id, 1, netip.MustParseAddrPort(tc.endpoint), netip.MustParsePrefix(tc.pool)); err == nil || c != nil {
			t.Fatalf("invalid scope accepted: %+v", tc)
		}
	}
}

func TestBSTRRoundtrip(t *testing.T) {
	for _, s := range []string{"", "IRVIO тест", "prefix\x00suffix", "\U0001f30d"} {
		p, err := newString(s)
		if err != nil {
			t.Fatal(err)
		}
		got, err := readString(p)
		freeString.Call(p)
		if err != nil || got != s {
			t.Fatalf("BSTR roundtrip %q: %q %v", s, got, err)
		}
	}
}

func TestVariantABI(t *testing.T) {
	if unsafe.Sizeof(variant{}) != 24 {
		t.Fatal("unexpected x64 VARIANT layout")
	}
	values := make([]*variant, 1000)
	for i := range values {
		values[i] = alignedVariant()
		if uintptr(unsafe.Pointer(values[i]))&15 != 0 {
			t.Fatal("unaligned by-value aggregate")
		}
	}
}

func TestCOMRuleRoundtrip(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil || len(interfaces) == 0 {
		t.Fatalf("native interface enumeration: %v", err)
	}
	row := windows.MibIfRow2{InterfaceIndex: uint32(interfaces[0].Index)}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		t.Fatal(err)
	}
	err = withRules(func(_ *object) error {
		rule, err := create(&classRule, &iidRule)
		if err != nil {
			return err
		}
		defer rule.release()
		c := &Control{name: "IRVIO.Control.unit", application: `C:\IRVIO\irviod.exe`, iface: interfaces[0].Name, guid: row.InterfaceGuid, description: "IRVIO control тест",
			endpoint: netip.MustParseAddrPort("10.17.0.1:7890"), pool: netip.MustParsePrefix("10.17.0.0/24")}
		if err := c.configure(rule); err != nil {
			return err
		}
		if err := c.verify(rule); err != nil {
			return err
		}
		if !c.matchesInterface(row.InterfaceGuid.String()) {
			return fmt.Errorf("GUID representation rejected")
		}
		c.guid = windows.GUID{Data1: 99}
		if c.matchesInterface(interfaces[0].Name) || c.matchesInterface(row.InterfaceGuid.String()) {
			return fmt.Errorf("reused name or foreign GUID accepted")
		}
		c.guid = row.InterfaceGuid
		if err := rule.putString(18, "7891"); err != nil {
			return err
		}
		if !errors.Is(c.verify(rule), ErrConflict) {
			return fmt.Errorf("changed port was accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeControlLifecycle(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_FIREWALL_TEST") != "1" {
		t.Skip("requires isolated elevated Windows VM and Wintun")
	}
	owner, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-FWTEST", Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.245.1/24")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	id, err := windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewControl(id, owner.LUID(), netip.MustParseAddrPort("10.253.245.1:7890"), netip.MustParsePrefix("10.253.245.0/24"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	cycle := func(i int) {
		if err := c.Install(); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
		if err := c.Install(); err != nil {
			t.Fatalf("idempotent install %d: %v", i, err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("idempotent close %d: %v", i, err)
		}
	}
	warmup := controlWarmup{high: make(map[string]int)}
	for i := 0; i < 100 && warmup.stable < 20; i++ {
		cycle(i)
		warmup.observe(wintest.HandleTypes())
		if (i+1)%20 == 0 {
			t.Logf("firewall warmup cycles=%d stable=%d highwater=%v", i+1, warmup.stable, warmup.high)
		}
	}
	if warmup.stable < 20 {
		t.Fatal("control firewall resources did not stabilize within 100 warmup cycles")
	}
	baseline := warmup.high
	t.Logf("firewall stable baseline: %v", baseline)
	for i := 0; i < 100; i++ {
		cycle(i)
		if (i+1)%20 == 0 {
			after := wintest.HandleTypes()
			t.Logf("firewall measured cycles=%d handles: %v", i+1, after)
			if err := wintest.ResourceGrowth(baseline, after); err != nil {
				t.Fatal(err)
			}
		}
	}
	err = withRules(func(rules *object) error {
		rule, err := create(&classRule, &iidRule)
		if err != nil {
			return err
		}
		defer rule.release()
		if err := c.configure(rule); err != nil {
			return err
		}
		if err := rule.putString(18, "7891"); err != nil {
			return err
		}
		return rules.call(8, uintptr(unsafe.Pointer(rule)))
	})
	if err != nil {
		t.Fatal(err)
	}
	// This test owns the deliberate foreign-scope fixture and restores it before ordinary cleanup.
	t.Cleanup(func() {
		if err := withRules(func(rules *object) error { return rules.putString(9, c.name) }); err != nil {
			t.Error(err)
		}
	})
	if !errors.Is(c.Install(), ErrConflict) {
		t.Fatal("overwrote conflicting rule")
	}
	if !errors.Is(c.Close(), ErrConflict) {
		t.Fatal("removed conflicting rule")
	}
	if err := withRules(func(rules *object) error {
		rule, err := findRule(rules, c.name)
		if err != nil {
			return err
		}
		if rule == nil {
			return fmt.Errorf("foreign rule disappeared")
		}
		defer rule.release()
		port, err := rule.getString(17)
		if err != nil {
			return err
		}
		if port != "7891" {
			return fmt.Errorf("foreign port modified")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("100 measured install/remove cycles after stable bounded warmup; bounded handles; idempotence; conflicting rule preserved PASS")
}

type controlWarmup struct {
	high   map[string]int
	stable int
}

func (w *controlWarmup) observe(sample map[string]int) {
	grew := false
	for kind, n := range sample {
		if n > w.high[kind] {
			w.high[kind] = n
			grew = true
		}
	}
	if grew {
		w.stable = 0
	} else {
		w.stable++
	}
}

func TestControlWarmupRejectsMonotonicGrowth(t *testing.T) {
	w := controlWarmup{high: make(map[string]int)}
	for i := 0; i < 100; i++ {
		w.observe(map[string]int{"ALPC Port": i + 1})
		if w.stable >= 20 {
			t.Fatal("accepted monotonic resource growth")
		}
	}
	for i := 0; i < 20; i++ {
		w.observe(map[string]int{"ALPC Port": 100})
	}
	if w.stable != 20 {
		t.Fatal("bounded stable window not recognized")
	}
	w.observe(map[string]int{"ALPC Port": 101})
	if w.stable != 0 {
		t.Fatal("new growth did not reset warmup")
	}
}
