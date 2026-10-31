// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/wintest"
	"golang.org/x/sys/windows"
)

func ownedRouteFixture(t *testing.T) (*tunnel.Windows, *TunnelRoutes, string) {
	t.Helper()
	if os.Getenv("IRVIO_ROUTE_OWNED_TEST") != "1" {
		t.Skip("disposable elevated Windows VM with Guest Agent required")
	}
	owner, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-Routes", Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.247.1/24"), netip.MustParsePrefix("fd77:247::1/64")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	path := filepath.Join(t.TempDir(), "private")
	m, err := OpenTunnelRoutes(owner.LUID(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return owner, m, path
}

func TestNativeOwnedRouteLifecycle(t *testing.T) {
	owner, m, path := ownedRouteFixture(t)
	ctx := context.Background()
	if duplicate, err := OpenTunnelRoutes(owner.LUID(), path); err == nil {
		duplicate.Close()
		t.Fatal("duplicate journal owner")
	}
	if err := m.Ensure(ctx, true, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(ctx, true, true); err != nil {
		t.Fatal("idempotence", err)
	}
	for _, address := range []string{"203.0.113.25", "2001:db8::25"} {
		row, _, err := bestRoute(netip.MustParseAddr(address), 0)
		if err != nil || row.InterfaceLuid != owner.LUID() {
			t.Fatal("route not selected", err)
		}
	}
	if err := m.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	for _, p := range splitPrefixes(true, true) {
		present, _, err := m.back.Lookup(owner.LUID(), p)
		if err != nil || present {
			t.Fatal("route residue", err)
		}
	}
	if err := m.Ensure(ctx, false, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTunnelRoutes(ctx, path); err != nil {
		t.Fatal(err)
	}
	t.Log("journal exclusion, idempotent install, v4/v6 selection, removal and inactive recovery PASS")
}

func TestNativeOwnedControlRouteLifecycle(t *testing.T) {
	owner, m, path := ownedRouteFixture(t)
	ctx := context.Background()
	addresses := []netip.Addr{netip.MustParseAddr("10.253.246.2"), netip.MustParseAddr("fd77:246::2")}
	for cycle := 0; cycle < 20; cycle++ {
		if err := m.EnsureControl(ctx, addresses); err != nil {
			t.Fatal(err)
		}
		if err := m.EnsureControl(ctx, addresses); err != nil {
			t.Fatal("idempotence", err)
		}
		for _, a := range addresses {
			row, _, err := bestRoute(a, 0)
			if err != nil || row.InterfaceLuid != owner.LUID() {
				t.Fatalf("control route not selected: %v", err)
			}
		}
		for _, p := range splitPrefixes(true, true) {
			present, _, err := m.back.Lookup(owner.LUID(), p)
			if err != nil || present {
				t.Fatal("pre-BIND installed a user route", err)
			}
		}
		if err := m.Remove(ctx); err != nil {
			t.Fatal(err)
		}
		for _, a := range addresses {
			present, _, err := m.back.Lookup(owner.LUID(), netip.PrefixFrom(a, a.BitLen()))
			if err != nil || present {
				t.Fatal("control route residue", err)
			}
		}
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTunnelRoutes(ctx, path); err != nil {
		t.Fatal(err)
	}
	t.Log("20 control-only IPv4/IPv6 cycles: exact routes, no split defaults, clean removal")
}

func TestNativeOwnedRouteExternalChange(t *testing.T) {
	owner, m, _ := ownedRouteFixture(t)
	ctx := context.Background()
	if err := m.Ensure(ctx, true, true); err != nil {
		t.Fatal(err)
	}
	p := splitPrefixes(true, true)[1]
	row, err := ownedRouteRow(owner.LUID(), p)
	if err != nil {
		t.Fatal(err)
	}
	row.Metric = 901
	if err := routeCall("SetIpForwardEntry2", uintptr(unsafe.Pointer(&row))); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = routeCall("DeleteIpForwardEntry2", uintptr(unsafe.Pointer(&row))); _ = m.Remove(ctx) })
	if err := m.Remove(ctx); !errors.Is(err, ErrRouteConflict) {
		t.Fatal("foreign edit not reported", err)
	}
	if err := windows.GetIpForwardEntry2(&row); err != nil || row.Metric != 901 {
		t.Fatal("foreign route changed", err)
	}
	if err := routeCall("DeleteIpForwardEntry2", uintptr(unsafe.Pointer(&row))); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	t.Log("foreign metric edit preserved; other owned routes removed; journal retained until resolution PASS")
}

func TestNativeOwnedRouteCollision(t *testing.T) {
	owner, m, _ := ownedRouteFixture(t)
	p := splitPrefixes(true, false)[0]
	for _, nextHop := range []string{"0.0.0.0", "10.253.247.254"} {
		row, err := ownedRouteRow(owner.LUID(), p)
		if err != nil {
			t.Fatal(err)
		}
		row.NextHop = nativeAddress(netip.MustParseAddr(nextHop))
		if err := routeCall("CreateIpForwardEntry2", uintptr(unsafe.Pointer(&row))); err != nil {
			t.Fatal(err)
		}
		if err := m.Ensure(context.Background(), true, true); !errors.Is(err, ErrRouteConflict) {
			t.Fatal("foreign exact route adopted", err)
		}
		if len(m.state.Prefixes) != 0 {
			t.Fatal("foreign preflight wrote owned state")
		}
		if err := windows.GetIpForwardEntry2(&row); err != nil {
			t.Fatal("foreign route lost", err)
		}
		if err := routeCall("DeleteIpForwardEntry2", uintptr(unsafe.Pointer(&row))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeOwnedRouteAdapterRemoval(t *testing.T) {
	owner, m, _ := ownedRouteFixture(t)
	if err := m.Ensure(context.Background(), true, true); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(m.state.Prefixes) != 0 {
		t.Fatal("gone GUID journal retained")
	}
}

func TestNativeOwnedRoute100Cycles(t *testing.T) {
	_, m, _ := ownedRouteFixture(t)
	ctx := context.Background()
	cycle := func() {
		t.Helper()
		if err := m.Ensure(ctx, true, true); err != nil {
			t.Fatal(err)
		}
		if err := m.Remove(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		cycle()
	}
	sample := func() map[string]int { runtime.GC(); return wintest.HandleTypes() }
	base := sample()
	g := runtime.NumGoroutine()
	t.Logf("route baseline types=%v", base)
	for i := 1; i <= 100; i++ {
		cycle()
		if i%10 == 0 {
			after := sample()
			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			t.Logf("cycle=%d types=%v goroutines=%d heap=%d", i, after, runtime.NumGoroutine(), mem.HeapInuse)
			if err := wintest.ResourceGrowth(base, after); err != nil {
				t.Fatal(err)
			}
			if runtime.NumGoroutine() > g+2 {
				t.Fatal("route goroutine growth")
			}
		}
	}
}

func TestNativeOwnedRouteCrashHelper(t *testing.T) {
	if os.Getenv("IRVIO_ROUTE_CRASH_HELPER") != "1" {
		t.Skip("subprocess helper only")
	}
	luid, err := strconv.ParseUint(os.Getenv("IRVIO_ROUTE_LUID"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenTunnelRoutes(luid, os.Getenv("IRVIO_ROUTE_JOURNAL"))
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("IRVIO_ROUTE_CONTROL_CRASH") == "1" {
		err = m.EnsureControl(context.Background(), controlAddresses())
	} else {
		err = m.Ensure(context.Background(), true, true)
	}
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("ROUTE_CRASH_READY")
	for {
		time.Sleep(time.Hour)
	}
}

func TestNativeOwnedRouteCrashRecovery(t *testing.T) {
	nativeOwnedRouteCrashRecovery(t, false)
}

func TestNativeOwnedControlRouteCrashRecovery(t *testing.T) {
	nativeOwnedRouteCrashRecovery(t, true)
}

func nativeOwnedRouteCrashRecovery(t *testing.T, control bool) {
	t.Helper()
	owner, m, path := ownedRouteFixture(t)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestNativeOwnedRouteCrashHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "IRVIO_ROUTE_CRASH_HELPER=1", "IRVIO_ROUTE_LUID="+strconv.FormatUint(owner.LUID(), 10), "IRVIO_ROUTE_JOURNAL="+path)
	prefixes := splitPrefixes(true, true)
	if control {
		cmd.Env = append(cmd.Env, "IRVIO_ROUTE_CONTROL_CRASH=1")
		prefixes = nil
		for _, a := range controlAddresses() {
			prefixes = append(prefixes, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if err := RecoverTunnelRoutes(context.Background(), path); err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(pipe)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "ROUTE_CRASH_READY" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatalf("crash helper not ready: %v", scanner.Err())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if cmd.Wait() == nil {
		t.Fatal("helper did not crash")
	}
	for _, p := range prefixes {
		present, exact, err := (nativeRouteBackend{}).Lookup(owner.LUID(), p)
		if err != nil || !present || !exact {
			t.Fatal("crash residue missing", err)
		}
	}
	if err := RecoverTunnelRoutes(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	for _, p := range prefixes {
		present, _, err := (nativeRouteBackend{}).Lookup(owner.LUID(), p)
		if err != nil || present {
			t.Fatal("recovery residue", err)
		}
	}
	t.Log("real route-owner process kill -> same-GUID journal recovery -> zero owned routes PASS")
}
