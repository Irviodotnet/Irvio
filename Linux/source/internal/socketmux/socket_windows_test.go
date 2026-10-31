// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"runtime/debug"
	"syscall"
	"testing"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/socketpolicy"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func ipv6Only(conn *net.UDPConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var value int
	var optionErr error
	if err := raw.Control(func(fd uintptr) {
		value, optionErr = windows.GetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, windows.IPV6_V6ONLY)
	}); err != nil {
		return 0, err
	}
	return value, optionErr
}

func TestWindowsMuxConcreteSourceKeepsInterfaceFence(t *testing.T) {
	requireIPv6(t)
	mux := newTestMux(t, dualStackConfig())
	for _, mark := range []uint32{socketpolicy.Underlay, 0} {
		if err := mux.SetMark(mark); err != nil {
			t.Fatal(err)
		}
		for _, socket := range []*physicalSocket{mux.ipv4, mux.ipv6} {
			binding, err := winroute.ForSource(socket.local.Addr())
			if err != nil {
				t.Fatal(err)
			}
			raw, err := socket.conn.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			index, err := winroute.SocketInterface(raw, socket.family == IPv4)
			if err != nil || index != binding.Index {
				t.Fatalf("%s native interface = %d, want %d: %v", socket.family, index, binding.Index, err)
			}
		}
	}
	for _, mark := range []uint32{1, socketpolicy.TunnelControl} {
		if err := mux.SetMark(mark); err == nil || mux.Mark() != 0 {
			t.Fatalf("unsupported policy accepted: mark=%#x err=%v", mark, err)
		}
	}
}

func TestWindowsMuxWildcardRejectsUnderlayPolicy(t *testing.T) {
	config := ipv4Config()
	config.IPv4Bind = netip.IPv4Unspecified()
	mux := newTestMux(t, config)
	if err := mux.SetMark(socketpolicy.Underlay); err == nil || mux.Mark() != 0 {
		t.Fatalf("wildcard underlay accepted: %v", err)
	}
}

func TestWindowsMuxExclusivePortCannotBeHijacked(t *testing.T) {
	mux := newTestMux(t, ipv4Config())
	local, _ := mux.LocalAddr(IPv4)
	lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var optionErr error
		if err := raw.Control(func(fd uintptr) {
			optionErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
		}); err != nil {
			return err
		}
		return optionErr
	}}
	conn, err := lc.ListenPacket(context.Background(), "udp4", local.String())
	if err == nil {
		conn.Close()
		t.Fatal("SO_REUSEADDR stole the mux port")
	}
	if !IsAddressInUse(err) {
		t.Fatalf("unexpected bind failure: %v", err)
	}
	if err := mux.Close(); err != nil {
		t.Fatal(err)
	}
	config := ipv4Config()
	config.Port = int(local.Port())
	newTestMux(t, config)
}

func TestWindowsBindConflictClassification(t *testing.T) {
	for _, err := range []error{windows.WSAEADDRINUSE, windows.WSAEACCES} {
		if !IsAddressInUse(fmt.Errorf("build generation: %w", err)) {
			t.Fatalf("native bind conflict not recognized: %v", err)
		}
	}
	for _, err := range []error{nil, windows.WSAEADDRNOTAVAIL, windows.WSAENETDOWN, context.Canceled} {
		if IsAddressInUse(err) {
			t.Fatalf("non-collision triggers destructive rebind: %v", err)
		}
	}
}

func TestWindowsDatagramErrorsAreNotReaderFailures(t *testing.T) {
	if !datagramTruncated(0, fmt.Errorf("recv: %w", windows.WSAEMSGSIZE)) ||
		!datagramTruncated(windows.MSG_TRUNC, nil) || datagramTruncated(0, windows.WSAECONNRESET) {
		t.Fatal("unexpected truncation classification")
	}
}

func TestWindowsMuxNativeLifecycleHandles(t *testing.T) {
	requireIPv6(t)
	previousGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGC)
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	cycle := func() {
		mux, err := New(context.Background(), dualStackConfig())
		if err != nil {
			t.Fatal(err)
		}
		if err := mux.SetMark(socketpolicy.Underlay); err != nil {
			mux.Close()
			t.Fatal(err)
		}
		if err := mux.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		cycle()
	}
	countHandles := func() uint32 {
		var count uint32
		result, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
		if result == 0 {
			t.Fatal(err)
		}
		return count
	}
	before := countHandles()
	for i := 0; i < 100; i++ {
		cycle()
	}
	after := countHandles()
	t.Logf("100 native dual-stack mux cycles: handles %d -> %d", before, after)
	if after > before+4 {
		t.Fatalf("native socket handles grew beyond runtime tolerance: %d -> %d", before, after)
	}
}
