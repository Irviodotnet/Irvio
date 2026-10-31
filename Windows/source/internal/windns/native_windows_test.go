// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windns

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

func nativeOwner(t *testing.T) (*tunnel.Windows, windows.GUID) {
	t.Helper()
	if os.Getenv("IRVIO_DNS_NATIVE_TEST") != "1" {
		t.Skip("requires elevated disposable Windows VM")
	}
	owner, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-DNS", Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.245.1/24"), netip.MustParsePrefix("fd77:245::1/64")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	id, err := interfaceGUID(owner.LUID())
	if err != nil {
		t.Fatal(err)
	}
	return owner, id
}

func TestNativeDNSRestoration(t *testing.T) {
	owner, id := nativeOwner(t)
	back := nativeBackend{}
	for _, mode := range []string{"dhcp", "static4", "static6", "dual"} {
		t.Run(mode, func(t *testing.T) {
			v4, v6 := "", ""
			if mode == "static4" || mode == "dual" {
				v4 = "192.0.2.53,192.0.2.54"
			}
			if mode == "static6" || mode == "dual" {
				v6 = "2001:db8::53,2001:db8::54"
			}
			if err := back.SetServers(id, false, v4); err != nil {
				t.Fatal(err)
			}
			if err := back.SetServers(id, true, v6); err != nil {
				t.Fatal(err)
			}
			domain, _ := windows.UTF16PtrFromString("irvio-dns.invalid")
			search, _ := windows.UTF16PtrFromString("one.invalid,two.invalid")
			setting := nativeSettings{Version: 1, Flags: 0x20 | 0x4, Domain: domain, SearchList: search}
			if err := nativeCall("SetInterfaceDnsSettings", uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&setting))); err != nil {
				t.Fatal(err)
			}
			baseline, err := Read(owner.LUID())
			if err != nil {
				t.Fatal(err)
			}
			if baseline.IPv4.Servers != v4 || baseline.IPv6.Servers != v6 {
				t.Fatalf("family baseline %+v", baseline)
			}
			path := filepath.Join(t.TempDir(), "private")
			m, err := Open(owner.LUID(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := m.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := Open(owner.LUID(), path); err == nil {
				t.Fatal("second DNS owner accepted")
			}
			if err := m.Apply(context.Background(), servers()); err != nil {
				t.Fatal(err)
			}
			got, err := Read(owner.LUID())
			if err != nil {
				t.Fatal(err)
			}
			want, _ := desiredServers(servers())
			if serverLists(got) != want {
				t.Fatal("wrong effective configured DNS")
			}
			if err := m.Restore(context.Background()); err != nil {
				t.Fatal(err)
			}
			after, err := Read(owner.LUID())
			if err != nil || after != baseline {
				t.Fatalf("baseline differs: %+v vs %+v: %v", baseline, after, err)
			}
			t.Logf("%s: apply/readback/restore exact native settings PASS", mode)
		})
	}
}

func TestNativeDNSCrashHelper(t *testing.T) {
	if os.Getenv("IRVIO_DNS_CRASH_HELPER") != "1" {
		t.Skip("subprocess helper only")
	}
	luid, err := strconv.ParseUint(os.Getenv("IRVIO_DNS_LUID"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Open(luid, os.Getenv("IRVIO_DNS_JOURNAL"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(context.Background(), servers()); err != nil {
		t.Fatal(err)
	}
	fmt.Println("DNS_CRASH_READY")
	for {
		time.Sleep(time.Hour)
	}
}

func TestNativeDNSProcessCrashRecovery(t *testing.T) {
	owner, _ := nativeOwner(t)
	baseline, err := Read(owner.LUID())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "private")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, exe, "-test.run=^TestNativeDNSCrashHelper$", "-test.v")
	command.Env = append(os.Environ(), "IRVIO_DNS_CRASH_HELPER=1", "IRVIO_DNS_LUID="+strconv.FormatUint(owner.LUID(), 10), "IRVIO_DNS_JOURNAL="+path)
	pipe, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		if err := Recover(context.Background(), path); err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(pipe)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "DNS_CRASH_READY" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatalf("crash helper not ready: %v", scanner.Err())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if command.Wait() == nil {
		t.Fatal("child did not crash")
	}
	afterKill, err := Read(owner.LUID())
	if err != nil {
		t.Fatal(err)
	}
	if afterKill == baseline {
		t.Fatal("process kill did not leave DNS residue")
	}
	if err := Recover(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	after, err := Read(owner.LUID())
	if err != nil || after != baseline {
		t.Fatalf("recovery %+v %v", after, err)
	}
	t.Log("real process kill -> durable recovery -> exact original DNS PASS")
}

func TestNativeDNSExternalChange(t *testing.T) {
	owner, id := nativeOwner(t)
	baseline, err := Read(owner.LUID())
	if err != nil {
		t.Fatal(err)
	}
	m, err := Open(owner.LUID(), filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Apply(context.Background(), servers()); err != nil {
		t.Fatal(err)
	}
	if err := (nativeBackend{}).SetServers(id, false, "192.0.2.99"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.Restore(context.Background()), ErrConflict) {
		t.Fatal("external edit not reported")
	}
	after, err := Read(owner.LUID())
	if err != nil {
		t.Fatal(err)
	}
	if after.IPv4.Servers != "192.0.2.99" || after.IPv6.Servers != baseline.IPv6.Servers {
		t.Fatal("external edit overwritten or owned IPv6 not restored")
	}
	if !errors.Is(m.Close(), ErrConflict) {
		t.Fatal("cleanup error lost")
	}
	t.Log("native external IPv4 edit preserved; owned IPv6 restored; conflict retained")
}

func TestNativeDNSAdapterRemoval(t *testing.T) {
	owner, _ := nativeOwner(t)
	m, err := Open(owner.LUID(), filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Apply(context.Background(), servers()); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.state.Active {
		t.Fatal("removed interface journal not cleared")
	}
	t.Log("removed owned GUID recovered without targeting a different interface")
}

func TestNativeDNS100Cycles(t *testing.T) {
	if os.Getenv("IRVIO_DNS_PIN_TEST_THREAD") == "1" {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		t.Log("diagnostic: test caller pinned to one OS thread")
	}
	owner, _ := nativeOwner(t)
	baseline, err := Read(owner.LUID())
	if err != nil {
		t.Fatal(err)
	}
	m, err := Open(owner.LUID(), filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	cycle := func() {
		t.Helper()
		if err := m.Apply(context.Background(), servers()); err != nil {
			t.Fatal(err)
		}
		if err := m.Restore(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		cycle()
	}
	handles := func() uint32 {
		runtime.GC()
		var n uint32
		ret, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
		if ret == 0 {
			t.Fatal(err)
		}
		return n
	}
	start := handles()
	beforeTypes := wintest.HandleTypes()
	t.Logf("DNS baseline handles=%d types=%v", start, beforeTypes)
	g := runtime.NumGoroutine()
	for i := 1; i <= 100; i++ {
		cycle()
		if i%10 == 0 {
			n := handles()
			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			t.Logf("DNS cycle=%d handles=%d baseline=%d goroutines=%d heap_inuse=%d", i, n, start, runtime.NumGoroutine(), mem.HeapInuse)
			types := wintest.HandleTypes()
			t.Logf("DNS handle types=%v", types)
			if err := wintest.ResourceGrowth(beforeTypes, types); err != nil {
				t.Error(err)
			}
			// Typed accounting includes at most two Go worker threads; their documented footprint alone can exceed eight handles.
			if runtime.NumGoroutine() > g+2 {
				t.Fatal("DNS resource-growth limit exceeded")
			}
		}
	}
	after, err := Read(owner.LUID())
	if err != nil || after != baseline {
		t.Fatalf("DNS lifecycle residue: %v", err)
	}
}
