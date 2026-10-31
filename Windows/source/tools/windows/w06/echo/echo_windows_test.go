// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package echo

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	winecho "github.com/Irviodotnet/Irvio/tools/windows/w06/echo/nativeecho"
	"golang.org/x/sys/windows"
)

// This probe validates an OS API candidate; it is not a production Echo backend.
func echoRequest(source, target netip.Addr, payload []byte) ([]byte, error) {
	api := windows.NewLazySystemDLL("iphlpapi.dll")
	h, _, err := api.NewProc("IcmpCreateFile").Call()
	if windows.Handle(h) == windows.InvalidHandle {
		return nil, err
	}
	defer api.NewProc("IcmpCloseHandle").Call(h)
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(event)
	s, d := source.As4(), target.As4()
	options := [16]byte{64, 0, 2}
	reply := make([]byte, 128+len(payload))
	var pin runtime.Pinner
	pin.Pin(&payload[0])
	pin.Pin(&options[0])
	pin.Pin(&reply[0])
	defer pin.Unpin()
	n, _, err := api.NewProc("IcmpSendEcho2Ex").Call(h, uintptr(event), 0, 0,
		uintptr(binary.LittleEndian.Uint32(s[:])), uintptr(binary.LittleEndian.Uint32(d[:])),
		uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)), uintptr(unsafe.Pointer(&options[0])),
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), 1200)
	runtime.KeepAlive(payload)
	runtime.KeepAlive(options)
	runtime.KeepAlive(reply)
	if n == 0 && errors.Is(err, windows.ERROR_IO_PENDING) {
		wait, waitErr := windows.WaitForSingleObject(event, windows.INFINITE)
		if waitErr != nil || wait != windows.WAIT_OBJECT_0 {
			return reply, fmt.Errorf("completion wait %d: %v", wait, waitErr)
		}
		n, _, err = api.NewProc("IcmpParseReplies").Call(uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)))
	}
	if n == 0 {
		return reply, err
	}
	if n != 1 || binary.LittleEndian.Uint32(reply[4:8]) != 0 || !bytes.Equal(reply[:4], d[:]) || !bytes.Contains(reply[28:], payload) {
		return reply, fmt.Errorf("invalid echo result replies=%d status=%d", n, binary.LittleEndian.Uint32(reply[4:8]))
	}
	return reply, nil
}

func TestNativeSourcePinnedEchoWithSplitRoutes(t *testing.T) {
	if os.Getenv("IRVIO_ECHO_ROUTE_LAB") != "1" {
		t.Skip("requires isolated SYSTEM Windows VM and out-of-band QGA")
	}
	target := netip.MustParseAddr("1.1.1.1")
	path, err := winroute.ResolveUnderlay(target, 0, nil)
	if err != nil || path.Binding.Source.String() != "10.0.2.15" {
		t.Fatal("not the isolated QEMU physical path", path, err)
	}
	payload := []byte("irvio-echo-feasibility-bound-0001")
	ownedEcho := func() error {
		r, err := winecho.Begin(context.Background(), path.Binding, target, payload, winecho.Options{TTL: 63, DontFragment: true, Timeout: 1200 * time.Millisecond})
		if r != nil {
			defer func() {
				if err := r.Close(); err != nil {
					t.Error(err)
				}
			}()
		}
		if err != nil {
			return err
		}
		_, err = r.Finish(context.Background())
		return err
	}
	preflight, err := echoRequest(path.Binding.Source, target, payload)
	if err != nil {
		t.Fatalf("physical preflight %v result=%x", err, preflight[:48])
	}
	t.Logf("native echo preflight PASS; response header=%x", preflight[:28])
	owner, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-W06-Echo", Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.248.1/24")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	device, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var packets [][]byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 65535)
		for {
			sizes := []int{0}
			if _, err := device.Read([][]byte{buffer}, sizes, 0); err != nil {
				return
			}
			if sizes[0] >= 28 && buffer[0] == 0x45 && buffer[9] == 1 {
				mu.Lock()
				packets = append(packets, bytes.Clone(buffer[:sizes[0]]))
				mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() {
		device.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("TUN reader did not close")
		}
	})
	routes, err := winroute.OpenTunnelRoutes(owner.LUID(), filepath.Join(t.TempDir(), "routes"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := routes.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := routes.Ensure(context.Background(), true, false); err != nil {
		t.Fatal(err)
	}
	selected, err := winroute.ResolveUnderlay(target, 0, nil)
	if err != nil || selected.Binding.LUID != owner.LUID() {
		t.Fatal("split route did not win", selected, err)
	}
	ordinary := []byte("irvio-echo-feasibility-ordinary")
	_, ordinaryErr := echoRequest(netip.IPv4Unspecified(), target, ordinary)
	if ordinaryErr == nil {
		t.Fatal("ordinary echo escaped lab TUN")
	}
	boundErr := ownedEcho()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	var ordinarySeen, boundSeen int
	for _, p := range packets {
		if bytes.Contains(p, ordinary) {
			ordinarySeen++
		}
		if bytes.Contains(p, payload) {
			boundSeen++
		}
	}
	mu.Unlock()
	t.Logf("split-default echo ordinary_error=%v ordinary_tun=%d bound_error=%v bound_tun=%d", ordinaryErr, ordinarySeen, boundErr, boundSeen)
	if ordinarySeen == 0 || boundSeen != 0 || boundErr != nil {
		t.Fatal("source-pinned API is not sufficient for physical echo under split routes")
	}
	if os.Getenv("IRVIO_ECHO_WFP_LAB") == "1" {
		store, err := wfp.NewStore(wfp.Config{Provider: windows.GUID{Data1: 0xe91617a1, Data2: 0x6333, Data3: 0x4bc1, Data4: [8]byte{0x90, 0x42, 0x32, 0x9a, 0, 0, 0, 1}}, Sublayer: windows.GUID{Data1: 0xe91617a2, Data2: 0x6333, Data3: 0x4bc1, Data4: [8]byte{0x90, 0x42, 0x32, 0x9a, 0, 0, 0, 1}}, Name: "IRVIO echo lab", Marker: "irvio-echo-feasibility-v1", ServiceName: "IRVIO-W06-EchoOwner", Weight: 49300})
		if err != nil {
			t.Fatal(err)
		}
		prior, err := store.Inspect(context.Background())
		if err != nil || prior.Installed {
			t.Fatal("existing lab WFP owner", err)
		}
		t.Cleanup(func() {
			if err := store.Remove(context.Background()); err != nil {
				t.Error(err)
			}
		})
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		app, err := wfp.ApplicationIDForPath(exe)
		if err != nil {
			t.Fatal(err)
		}
		apply := func(allow bool, variation string) {
			var rules []wfp.Rule
			for i, layer := range []windows.GUID{wfp.ConnectV4, wfp.TransportV4} {
				rules = append(rules, wfp.Rule{Key: windows.GUID{Data1: uint32(1 + i*2)}, Layer: layer, Action: wfp.Block, Remote: netip.PrefixFrom(target, 32), Protocol: 1})
				if allow {
					r := wfp.Rule{Key: windows.GUID{Data1: uint32(2 + i*2)}, Layer: layer, Action: wfp.Permit, Priority: 100, Remote: netip.PrefixFrom(target, 32), LocalAddress: path.Binding.Source, LocalInterface: path.Binding.LUID, Protocol: 1, ICMP: true, ICMPType: 8}
					if layer == wfp.ConnectV4 {
						r.Application = app
						r.User = wfp.SystemOrAdministrator
					}
					if variation == "no-type" {
						r.ICMP = false
						r.ICMPType = 0
					}
					if variation == "no-app" {
						r.Application = ""
						r.User = wfp.AnyUser
					}
					if variation == "no-user" {
						r.User = wfp.AnyUser
					}
					if variation == "no-app-only" {
						r.Application = ""
					}
					if variation == "no-ale-interface" && layer == wfp.ConnectV4 {
						r.LocalInterface = 0
					}
					if variation == "no-source" {
						r.LocalAddress = netip.Addr{}
					}
					if variation == "no-interface" {
						r.LocalInterface = 0
					}
					if variation == "all" {
						r.Application = ""
						r.User = wfp.AnyUser
						r.LocalAddress = netip.Addr{}
						r.LocalInterface = 0
						r.ICMP = false
						r.ICMPType = 0
					}
					rules = append(rules, r)
				}
			}
			if err := store.Replace(context.Background(), rules); err != nil {
				t.Fatal(err)
			}
		}
		apply(true, "exact")
		if err := ownedEcho(); err != nil {
			for _, variation := range []string{"no-user", "no-app-only"} {
				apply(true, variation)
				t.Logf("WFP diagnostic scope=%s result=%v", variation, ownedEcho())
			}
			t.Fatal("owned WFP-scoped echo", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		out, err := exec.CommandContext(ctx, "ping.exe", "-4", "-n", "1", "-w", "1200", "-S", path.Binding.Source.String(), target.String()).CombinedOutput()
		cancel()
		if err == nil {
			t.Fatalf("foreign ping bypassed app fence: %s", out)
		}
		apply(false, "exact")
		if err := ownedEcho(); err == nil {
			t.Fatal("echo survived grant withdrawal")
		}
		if err := store.Remove(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := ownedEcho(); err != nil {
			t.Fatal("echo after WFP cleanup", err)
		}
		t.Log("native WFP echo owner allowed; foreign ping denied; withdrawn grant denied; cleanup PASS")
	}
	if err := routes.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := echoRequest(path.Binding.Source, target, payload); err != nil {
		t.Fatal("post-route-cleanup echo", err)
	}
}
