// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"sync/atomic"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var fixtureSequence atomic.Uint32

func nativeStore(t *testing.T) *Store {
	t.Helper()
	if os.Getenv("IRVIO_WFP_NATIVE_TEST") != "1" {
		t.Skip("requires an elevated disposable Windows VM with IRVIO-W05-Owner service")
	}
	c := testConfig()
	c.Weight = uint16(47000 + fixtureSequence.Add(1))
	var err error
	c.Provider, err = windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	c.Sublayer, err = windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture provider=%s sublayer=%s", c.Provider.String(), c.Sublayer.String())
	t.Cleanup(func() {
		if err := s.Remove(context.Background()); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	return s
}

func nativeRuleFixture(t *testing.T) Rule {
	t.Helper()
	k, err := windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	return Rule{Key: k, Layer: ConnectV4, Priority: 10, Action: Block, Remote: netip.MustParsePrefix("192.0.2.253/32"), Protocol: 6, RemotePort: 49493}
}

func assertNativeRules(t *testing.T, s *Store, want []Rule) {
	t.Helper()
	state, err := s.Inspect(context.Background())
	if err != nil || !state.Installed || !state.Active || len(state.Rules) != len(want) {
		t.Fatalf("inspect: %+v, %v", state, err)
	}
	for _, r := range want {
		if !slices.Contains(state.Rules, r) {
			t.Fatalf("missing native rule %+v in %+v", r, state)
		}
	}
}

func TestNativeTransactionalOwnership(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	r := nativeRuleFixture(t)
	if err := s.Replace(ctx, []Rule{r}); err != nil {
		t.Fatal(err)
	}
	assertNativeRules(t, s, []Rule{r})
	foreignConfig := s.config
	foreignConfig.Marker = "irvio-another-owner-v1"
	foreign, _ := NewStore(foreignConfig)
	if err := foreign.Remove(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign removal: %v", err)
	}
	if err := foreign.Replace(ctx, []Rule{r}); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign replacement: %v", err)
	}
	assertNativeRules(t, s, []Rule{r})
	other := nativeStore(t)
	collision := nativeRuleFixture(t)
	if err := other.Replace(ctx, []Rule{collision}); err != nil {
		t.Fatal(err)
	}
	assertNativeRules(t, other, []Rule{collision})
	newRule := nativeRuleFixture(t)
	if err := s.Replace(ctx, []Rule{newRule, collision}); err == nil {
		t.Fatal("cross-provider filter key collision accepted")
	} else {
		t.Logf("expected collision error: %v", err)
	}
	assertNativeRules(t, s, []Rule{r})
	assertNativeRules(t, other, []Rule{collision})
	cancelled, cancel := context.WithCancel(ctx)
	err := s.transaction(cancelled, false, func(h windows.Handle) error {
		if err := s.removeFilters(cancelled, h); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled commit: %v", err)
	}
	assertNativeRules(t, s, []Rule{r})
	if err := s.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := s.Inspect(ctx)
	if err != nil || state.Installed || state.Active || len(state.Rules) != 0 {
		t.Fatalf("residue %+v %v", state, err)
	}
}

func TestNativeMultipleEnumerationPages(t *testing.T) {
	s := nativeStore(t)
	var rules []Rule
	for i := 0; i < 130; i++ {
		r := nativeRuleFixture(t)
		r.Priority = uint64(i)
		if i%2 != 0 {
			r.Layer = ConnectV6
			r.Remote = netip.MustParsePrefix("2001:db8::fd/128")
		}
		rules = append(rules, r)
	}
	if err := s.Replace(context.Background(), rules); err != nil {
		t.Fatal(err)
	}
	assertNativeRules(t, s, rules)
}

func TestNativePriorityCollisionRollsBack(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	r := nativeRuleFixture(t)
	if err := s.Replace(ctx, []Rule{r}); err != nil {
		t.Fatal(err)
	}
	other := nativeStore(t)
	other.config.Weight = s.config.Weight
	if err := other.Replace(ctx, []Rule{nativeRuleFixture(t)}); err == nil {
		t.Fatal("kernel priority reassignment silently accepted")
	}
	after, err := other.Inspect(ctx)
	if err != nil || after.Installed {
		t.Fatalf("failed install residue: %+v %v", after, err)
	}
	assertNativeRules(t, s, []Rule{r})
}

func TestNativeManagementWithAggressiveGC(t *testing.T) {
	s := nativeStore(t)
	previous := debug.SetGCPercent(1)
	defer debug.SetGCPercent(previous)
	for i := 0; i < 20; i++ {
		r := nativeRuleFixture(t)
		if err := s.Replace(context.Background(), []Rule{r}); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		assertNativeRules(t, s, []Rule{r})
	}
}

func TestNativeAllSupportedLayers(t *testing.T) {
	s := nativeStore(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	app, err := ApplicationIDForPath(exe)
	if err != nil {
		t.Fatal(err)
	}
	iface := windows.MibIfRow2{InterfaceIndex: 1}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &iface); err != nil {
		t.Fatal(err)
	}
	var rules []Rule
	for _, layer := range []windows.GUID{ConnectV4, ConnectV6, TransportV4, TransportV6, AcceptV4, AcceptV6} {
		for _, proto := range []uint8{6, 17} {
			r := nativeRuleFixture(t)
			r.Layer = layer
			r.Protocol = proto
			r.LocalInterface = iface.InterfaceLuid
			r.LocalPort = 51820
			r.LocalAddress = netip.MustParseAddr("192.0.2.1")
			if layer == ConnectV4 || layer == ConnectV6 || layer == AcceptV4 || layer == AcceptV6 {
				r.Application = app
			}
			if layer == ConnectV6 || layer == TransportV6 || layer == AcceptV6 {
				r.Remote = netip.MustParsePrefix("2001:db8::fd/128")
				r.LocalAddress = netip.MustParseAddr("2001:db8::1")
			}
			rules = append(rules, r)
		}
	}
	if err := s.Replace(context.Background(), rules); err != nil {
		t.Fatal(err)
	}
	assertNativeRules(t, s, rules)
}

func TestNative100ReplacementLifetimes(t *testing.T) {
	s := nativeStore(t)
	r := nativeRuleFixture(t)
	ctx := context.Background()
	count := func() uint32 {
		runtime.GC()
		var n uint32
		ret, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
		if ret == 0 {
			t.Fatal(err)
		}
		return n
	}
	for i := 0; i < 3; i++ {
		if err := s.Replace(ctx, []Rule{r}); err != nil {
			t.Fatal(err)
		}
	}
	baseline := count()
	before := handleTypes()
	t.Logf("baseline handle types: %v", before)
	for i := 0; i < 100; i++ {
		r.Priority = uint64(i)
		if err := s.Replace(ctx, []Rule{r}); err != nil {
			t.Fatal(err)
		}
		assertNativeRules(t, s, []Rule{r})
		if i%10 == 9 {
			n := count()
			types := handleTypes()
			t.Logf("cycle %d handles=%d goroutines=%d types=%v", i+1, n, runtime.NumGoroutine(), types)
			if err := resourceGrowth(before, types); err != nil {
				t.Fatal(err)
			}
		}
	}
	last := count()
	after := handleTypes()
	t.Logf("final handle types: %v", after)
	t.Logf("100 persistent replacements, handles %d -> %d", baseline, last)
	if err := resourceGrowth(before, after); err != nil {
		t.Fatal(err)
	}
}

func TestResourceGrowthOracle(t *testing.T) {
	base := map[string]int{"Thread": 10, "Event": 30, "IRTimer": 20, "WaitCompletionPacket": 15, "File": 8}
	for _, tc := range []struct {
		name  string
		delta map[string]int
		fail  bool
	}{
		{"flat", nil, false},
		{"fewer threads", map[string]int{"Thread": -1}, false},
		{"fewer threads and leak", map[string]int{"Thread": -1, "File": 5}, true},
		{"one runtime M", map[string]int{"Thread": 1, "Event": 2, "IRTimer": 2, "WaitCompletionPacket": 1}, false},
		{"event leak", map[string]int{"Event": 5}, true},
		{"file leak", map[string]int{"File": 5}, true},
		{"thread leak", map[string]int{"Thread": 3, "Event": 6, "IRTimer": 6, "WaitCompletionPacket": 3}, true},
		{"M plus leak", map[string]int{"Thread": 1, "Event": 7, "IRTimer": 2, "WaitCompletionPacket": 1}, true},
		{"incomplete", map[string]int{"unqueried": 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := make(map[string]int)
			for k, v := range base {
				after[k] = v
			}
			for k, v := range tc.delta {
				after[k] += v
			}
			if err := resourceGrowth(base, after); (err != nil) != tc.fail {
				t.Fatalf("oracle: %v, want failure=%v", err, tc.fail)
			}
		})
	}
}
