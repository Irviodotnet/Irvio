// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnel

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"golang.org/x/sys/windows"
)

type testWindowsRing struct {
	event       windows.Handle
	ended       atomic.Bool
	reads       chan struct{}
	allocated   chan struct{}
	sendRelease chan struct{}
	packet      []byte
	released    int
	readErr     error
	writeErr    error
}

func (r *testWindowsRing) ReceivePacket() ([]byte, error) {
	select {
	case r.reads <- struct{}{}:
	default:
	}
	if r.ended.Load() {
		panic("read after End")
	}
	if r.readErr != nil {
		return nil, r.readErr
	}
	if r.packet != nil {
		return r.packet, nil
	}
	return nil, windows.ERROR_NO_MORE_ITEMS
}
func (r *testWindowsRing) ReleaseReceivePacket([]byte) { r.packet = nil; r.released++ }
func (r *testWindowsRing) AllocateSendPacket(n int) ([]byte, error) {
	if r.writeErr != nil {
		return nil, r.writeErr
	}
	if r.allocated != nil {
		close(r.allocated)
		<-r.sendRelease
	}
	if r.ended.Load() {
		panic("write after End")
	}
	return make([]byte, n), nil
}
func (*testWindowsRing) SendPacket([]byte)               {}
func (r *testWindowsRing) ReadWaitEvent() windows.Handle { return r.event }
func (r *testWindowsRing) End() {
	if r.ended.Swap(true) {
		panic("double End")
	}
	windows.CloseHandle(r.event)
}

type testWindowsAdapter struct {
	ring     *testWindowsRing
	closed   bool
	startErr error
}

func (*testWindowsAdapter) LUID() uint64 { return 99 }
func (a *testWindowsAdapter) Start() (windowsRing, error) {
	if a.startErr != nil {
		return nil, a.startErr
	}
	return a.ring, nil
}
func (a *testWindowsAdapter) Close() error {
	if a.startErr == nil && !a.ring.ended.Load() {
		panic("adapter closed before ring")
	}
	a.closed = true
	return nil
}

type testWindowsLinks struct {
	added     map[netip.Prefix]bool
	addCalls  int
	failAdd   int
	removeErr error
	readyErr  error
	automatic map[netip.Prefix]bool
	sourceErr error
}

func (*testWindowsLinks) Configure(uint64, int) (int, error) { return 7, nil }
func (l *testWindowsLinks) Add(_ uint64, p netip.Prefix) error {
	l.addCalls++
	if l.addCalls == l.failAdd {
		return os.ErrPermission
	}
	l.added[p] = true
	return nil
}
func (l *testWindowsLinks) Remove(_ uint64, p netip.Prefix) error {
	if l.removeErr != nil {
		return l.removeErr
	}
	delete(l.added, p)
	return nil
}
func (l *testWindowsLinks) Ready(context.Context, uint64, []netip.Prefix) error { return l.readyErr }

func (l *testWindowsLinks) AutomaticSource(_ uint64, p netip.Prefix, enabled bool) error {
	if l.sourceErr != nil {
		return l.sourceErr
	}
	if l.automatic == nil {
		l.automatic = make(map[netip.Prefix]bool)
	}
	l.automatic[p] = enabled
	return nil
}

func TestWindowsAutomaticSourceOwnership(t *testing.T) {
	w, _, links := testWindowsOwner(t)
	control := w.Addresses()[0]
	if err := w.SetAutomaticSource(control, false); err != nil || links.automatic[control] {
		t.Fatal("control source preference", err)
	}
	foreign := netip.MustParsePrefix("10.253.244.1/32")
	if err := w.SetAutomaticSource(foreign, false); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("foreign source accepted", err)
	}
	if err := w.AddAddresses([]netip.Prefix{foreign}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetAutomaticSource(foreign, true); err != nil || !links.automatic[foreign] {
		t.Fatal("owned dynamic source", err)
	}
	links.sourceErr = os.ErrPermission
	if err := w.SetAutomaticSource(control, true); !errors.Is(err, os.ErrPermission) || links.automatic[control] {
		t.Fatal("source failure hidden", err)
	}
	if err := w.RemoveAddresses([]netip.Prefix{foreign}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetAutomaticSource(foreign, true); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("removed source accepted", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.SetAutomaticSource(control, true); !errors.Is(err, ErrClosed) {
		t.Fatal("closed owner accepted", err)
	}
}

func testWindowsOwner(t *testing.T) (*Windows, *testWindowsAdapter, *testWindowsLinks) {
	t.Helper()
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &testWindowsRing{event: event, reads: make(chan struct{}, 1)}
	a := &testWindowsAdapter{ring: r}
	l := &testWindowsLinks{added: make(map[netip.Prefix]bool)}
	w, err := newWindows(Config{Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.242.1/24")}}, func(string) (windowsAdapter, error) { return a, nil }, l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	return w, a, l
}

func blockedRead(device awgtun.Device) <-chan error {
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 65535)
		for {
			if _, err := device.Read([][]byte{buf}, []int{0}, 0); err != nil {
				done <- err
				return
			}
		}
	}()
	return done
}

func requireStopped(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("read exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("borrowed read did not stop")
	}
}

func TestWindowsBorrowCancelPreservesOwner(t *testing.T) {
	w, a, _ := testWindowsOwner(t)
	for range 100 {
		b, err := w.Borrow()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Borrow(); !errors.Is(err, ErrAlreadyBorrowed) {
			t.Fatalf("second borrower: %v", err)
		}
		done := blockedRead(b)
		<-a.ring.reads
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		requireStopped(t, done)
		if a.closed || a.ring.ended.Load() {
			t.Fatal("borrower destroyed owner")
		}
		if _, err := b.Write([][]byte{{1}}, 0); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("old borrower: %v", err)
		}
		for range b.Events() {
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !a.closed || !a.ring.ended.Load() {
		t.Fatal("owner leaked")
	}
	if _, err := w.Borrow(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestWindowsOwnerCloseJoinsBorrowedWrite(t *testing.T) {
	w, a, _ := testWindowsOwner(t)
	a.ring.allocated, a.ring.sendRelease = make(chan struct{}), make(chan struct{})
	b, _ := w.Borrow()
	written := make(chan error, 1)
	go func() { _, err := b.Write([][]byte{{1}}, 0); written <- err }()
	<-a.ring.allocated
	closed := make(chan error, 1)
	go func() { closed <- w.Close() }()
	select {
	case <-closed:
		t.Fatal("closed ring during write")
	case <-time.After(20 * time.Millisecond):
	}
	close(a.ring.sendRelease)
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestWindowsConcurrentClose(t *testing.T) {
	for range 100 {
		w, _, _ := testWindowsOwner(t)
		b, _ := w.Borrow()
		done := blockedRead(b)
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() { _ = w.Close() })
			wg.Go(func() { _ = b.Close() })
		}
		wg.Wait()
		requireStopped(t, done)
	}
}

func TestWindowsDynamicAddressesRollbackAndOwnership(t *testing.T) {
	w, _, l := testWindowsOwner(t)
	p := netip.MustParsePrefix("10.253.242.3/32")
	q := netip.MustParsePrefix("fd77::3/128")
	l.failAdd = l.addCalls + 2
	if err := w.AddAddresses([]netip.Prefix{p, q}); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	if len(w.dynamic) != 0 || l.added[p] {
		t.Fatal("partial address residue")
	}
	l.failAdd = 0
	if err := w.AddAddresses([]netip.Prefix{p, p, q}); err != nil {
		t.Fatal(err)
	}
	if len(w.dynamic) != 2 {
		t.Fatal("duplicate dynamic address")
	}
	if err := w.AddAddresses([]netip.Prefix{netip.MustParsePrefix("10.253.242.1/32")}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("static address replaced", err)
	}
	if err := w.RemoveAddresses(append(w.Addresses(), p, q)); err != nil {
		t.Fatal(err)
	}
	if len(l.added) != 1 || len(w.dynamic) != 0 {
		t.Fatal("static or dynamic ownership wrong")
	}
	l.readyErr = context.DeadlineExceeded
	l.removeErr = os.ErrPermission
	if err := w.AddAddresses([]netip.Prefix{p}); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	if _, ok := w.dynamic[p]; !ok {
		t.Fatal("failed rollback lost ownership")
	}
	l.removeErr = nil
	if err := w.RemoveAddresses([]netip.Prefix{p}); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsCreationFailureCleanup(t *testing.T) {
	for _, stage := range []string{"start", "add", "ready"} {
		t.Run(stage, func(t *testing.T) {
			event, err := windows.CreateEvent(nil, 1, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			a := &testWindowsAdapter{ring: &testWindowsRing{event: event}}
			l := &testWindowsLinks{added: make(map[netip.Prefix]bool)}
			switch stage {
			case "start":
				a.startErr = os.ErrPermission
				defer windows.CloseHandle(event)
			case "add":
				l.failAdd = 2
			case "ready":
				l.readyErr = context.DeadlineExceeded
			}
			w, err := newWindows(Config{Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.242.1/24"), netip.MustParsePrefix("fd77::1/64")}}, func(string) (windowsAdapter, error) { return a, nil }, l)
			if err == nil || w != nil || !a.closed {
				t.Fatal("failed startup leaked", w, err)
			}
		})
	}
}

func TestWindowsIPHelperLayout(t *testing.T) {
	var row tunIPInterfaceRow
	if unsafe.Sizeof(row) != 168 || unsafe.Offsetof(row.NlMtu) != 152 || unsafe.Offsetof(row.TransmitOffload) != 164 || unsafe.Offsetof(row.DisableDefaultRoutes) != 166 {
		t.Fatalf("IP Helper ABI mismatch: size=%d mtu=%d offload=%d", unsafe.Sizeof(row), unsafe.Offsetof(row.NlMtu), unsafe.Offsetof(row.TransmitOffload))
	}
	var address windows.MibUnicastIpAddressRow
	if unsafe.Sizeof(address) != 80 || unsafe.Offsetof(address.DadState) != 64 {
		t.Fatal("unicast ABI mismatch")
	}
}

func TestWindowsBorrowIOValidation(t *testing.T) {
	w, a, _ := testWindowsOwner(t)
	b, err := w.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{-1, 4, 5} {
		if _, err := b.Read([][]byte{make([]byte, 4)}, []int{0}, offset); !errors.Is(err, os.ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := b.Write([][]byte{make([]byte, 4)}, offset); !errors.Is(err, os.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := b.Read(nil, nil, 0); !errors.Is(err, os.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := b.Write([][]byte{make([]byte, 65536)}, 0); !errors.Is(err, os.ErrInvalid) {
		t.Fatal(err)
	}
	a.ring.packet = []byte{1, 2, 3, 4}
	if _, err := b.Read([][]byte{make([]byte, 3)}, []int{0}, 0); !errors.Is(err, io.ErrShortBuffer) {
		t.Fatal(err)
	}
	if a.ring.released != 1 {
		t.Fatal("short read did not release ring packet")
	}
	a.ring.packet = []byte{1, 2, 3, 4}
	buf, sizes := make([]byte, 6), []int{0}
	if n, err := b.Read([][]byte{buf}, sizes, 2); err != nil || n != 1 || sizes[0] != 4 || buf[2] != 1 || buf[5] != 4 {
		t.Fatal("offset read failed", n, err)
	}
	a.ring.readErr = windows.ERROR_HANDLE_EOF
	if _, err := b.Read([][]byte{buf}, sizes, 0); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	a.ring.writeErr = windows.ERROR_HANDLE_EOF
	if _, err := b.Write([][]byte{{1}}, 0); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	a.ring.writeErr = windows.ERROR_BUFFER_OVERFLOW
	if n, err := b.Write([][]byte{{1}}, 0); err != nil || n != 1 {
		t.Fatal("ring overflow semantics", n, err)
	}
}

func TestWindowsInvalidConfigDoesNotCreateAdapter(t *testing.T) {
	valid := netip.MustParsePrefix("10.253.242.1/24")
	for _, config := range []Config{
		{}, {Addresses: []netip.Prefix{valid, valid}},
		{Name: "name-is-too-long", Addresses: []netip.Prefix{valid}},
		{MTU: 575, Addresses: []netip.Prefix{valid}},
		{MTU: 65536, Addresses: []netip.Prefix{valid}},
		{Addresses: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/32")}},
		{Addresses: []netip.Prefix{netip.MustParsePrefix("ff00::1/128")}},
	} {
		w, err := newWindows(config, func(string) (windowsAdapter, error) {
			t.Fatal("invalid configuration reached native adapter creation")
			return nil, nil
		}, nil)
		if w != nil || !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("config=%+v result=%v", config, err)
		}
	}
}
