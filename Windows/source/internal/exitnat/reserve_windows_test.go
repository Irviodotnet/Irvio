// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"runtime"
	"sync"
	"testing"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func nativePortPath(t *testing.T) Path {
	t.Helper()
	value := os.Getenv("IRVIO_WINDOWS_NAT_PORT_SOURCE")
	if value == "" {
		t.Skip("set IRVIO_WINDOWS_NAT_PORT_SOURCE to an isolated lab NIC's IPv4 address")
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		t.Fatal(err)
	}
	b, err := winroute.ForSource(address)
	if err != nil {
		t.Fatal(err)
	}
	return Path{Generation: 1, Interface: b.LUID, Source: address}
}

func bindPort(t *testing.T, path Path, protocol uint8, port uint16, option int) error {
	t.Helper()
	typ := int32(windows.SOCK_STREAM)
	if protocol == UDP {
		typ = windows.SOCK_DGRAM
	}
	fd, err := windows.WSASocket(windows.AF_INET, typ, int32(protocol), nil, 0, windows.WSA_FLAG_NO_HANDLE_INHERIT)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.Closesocket(fd); err != nil {
			t.Error(err)
		}
	}()
	if option != 0 {
		if err := windows.SetsockoptInt(fd, windows.SOL_SOCKET, option, 1); err != nil {
			t.Fatal(err)
		}
	}
	return windows.Bind(fd, &windows.SockaddrInet4{Addr: path.Source.As4(), Port: int(port)})
}

func TestWindowsExclusivePortReservation(t *testing.T) {
	path := nativePortPath(t)
	for _, protocol := range []uint8{TCP, UDP} {
		r, err := ReserveWindows(context.Background(), path, protocol)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := r.Close(); err != nil {
				t.Error(err)
			}
		})
		native := r.(*windowsReservation)
		var flags uint32
		ok, _, flagErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation").Call(uintptr(native.socket), uintptr(unsafe.Pointer(&flags)))
		if ok == 0 || flags&windows.HANDLE_FLAG_INHERIT != 0 {
			t.Fatalf("reservation handle must not be inherited: flags=%d err=%v", flags, flagErr)
		}
		if r.Port() == 0 {
			t.Fatal("zero reserved port")
		}
		if value, err := windows.GetsockoptInt(native.socket, windows.SOL_SOCKET, exclusiveAddressUse); err != nil || value == 0 {
			t.Fatalf("missing native exclusivity: %d %v", value, err)
		}
		if protocol == TCP {
			const soAcceptConn = 2
			if value, err := windows.GetsockoptInt(native.socket, windows.SOL_SOCKET, soAcceptConn); err != nil || value != 0 {
				t.Fatalf("reservation must not accept TCP connections: %d %v", value, err)
			}
		}
		for _, option := range []int{0, windows.SO_REUSEADDR, exclusiveAddressUse} {
			err := bindPort(t, path, protocol, r.Port(), option)
			if !errors.Is(err, windows.WSAEADDRINUSE) && !errors.Is(err, windows.WSAEACCES) {
				t.Fatalf("same source/protocol/port collision option=%d: %v", option, err)
			}
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		var data windows.WSAData
		if err := windows.WSAStartup(0x202, &data); err != nil {
			t.Fatal(err)
		}
		err = bindPort(t, path, protocol, r.Port(), exclusiveAddressUse)
		cleanup := windows.WSACleanup()
		if err != nil || cleanup != nil {
			t.Fatalf("released port remains owned: %v %v", err, cleanup)
		}
	}
}

func TestWindowsPortReservationBounds(t *testing.T) {
	path := nativePortPath(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if r, err := ReserveWindows(canceled, path, UDP); r != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(r, err)
	}
	wrong := path
	wrong.Interface++
	if r, err := ReserveWindows(context.Background(), wrong, UDP); r != nil || !errors.Is(err, ErrStale) {
		t.Fatal(r, err)
	}
	if r, err := ReserveWindows(context.Background(), path, 1); r != nil || !errors.Is(err, ErrConfig) {
		t.Fatal(r, err)
	}
	if r, err := ReserveWindows(nil, path, TCP); r != nil || !errors.Is(err, ErrConfig) {
		t.Fatal(r, err)
	}
	if r, err := ReserveWindows(context.Background(), Path{}, TCP); r != nil || !errors.Is(err, ErrConfig) {
		t.Fatal(r, err)
	}
}

func TestWindowsPortReservation100Cycles(t *testing.T) {
	path := nativePortPath(t)
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	handles := func() uint32 {
		var n uint32
		r, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
		if r == 0 {
			t.Fatal(err)
		}
		return n
	}
	cycle := func() {
		var live []Reservation
		defer func() {
			for _, r := range live {
				if err := r.Close(); err != nil {
					t.Error(err)
				}
			}
		}()
		seen := make(map[external]bool)
		for i := 0; i < 10; i++ {
			protocol := TCP
			if i%2 == 0 {
				protocol = UDP
			}
			r, err := ReserveWindows(context.Background(), path, protocol)
			if err != nil {
				t.Fatal(err)
			}
			live = append(live, r)
			key := external{protocol, r.Port()}
			if seen[key] {
				t.Fatal("duplicate live protocol port")
			}
			seen[key] = true
		}
		var wg sync.WaitGroup
		for _, r := range live {
			for j := 0; j < 2; j++ {
				wg.Go(func() {
					if err := r.Close(); err != nil {
						t.Error(err)
					}
				})
			}
		}
		wg.Wait()
	}
	for i := 0; i < 10; i++ {
		cycle()
	}
	baseline := handles()
	var last, high uint32
	for i := 0; i < 100; i++ {
		cycle()
		last = handles()
		if last > high {
			high = last
		}
	}
	t.Logf("cycles=100 ports_per_cycle=10 handles baseline=%d last=%d max=%d", baseline, last, high)
	if last > baseline+2 || high > baseline+4 {
		t.Fatal("native reservation handle growth")
	}
}
