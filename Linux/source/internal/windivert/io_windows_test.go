// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windivert

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"runtime/debug"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func ioModule(t *testing.T) *Module {
	t.Helper()
	if os.Getenv("IRVIO_DIVERT_TEST_SERVICE") != "running" {
		t.Skip("requires explicit running disposable driver fixture")
	}
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNativeOverlappedCancellationAndClose(t *testing.T) {
	m := ioModule(t)
	// Exclude lazy GC workers and ensure finalizers cannot hide missing native closes.
	oldGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(oldGC)
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	count := func() uint32 {
		var n uint32
		ok, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
		if ok == 0 {
			t.Fatal(err)
		}
		return n
	}
	cycle := func(closePending bool) {
		h, err := m.openIO("false", 0, 4)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := h.Close(); err != nil {
				t.Error(err)
			}
		}()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() { _, err := h.ReceiveIPv4(ctx); result <- err }()
		deadline := time.Now().Add(time.Second)
		for {
			h.mu.Lock()
			active := h.active
			h.mu.Unlock()
			if active {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("read did not become pending")
			}
			time.Sleep(time.Millisecond)
		}
		start := time.Now()
		want := error(context.Canceled)
		if closePending {
			want = ErrClosed
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			cancel()
		}
		select {
		case err := <-result:
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		case <-time.After(time.Second):
			t.Fatal("pending read did not cancel")
		}
		if time.Since(start) > time.Second {
			t.Fatal("slow cancellation")
		}
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := h.ReceiveIPv4(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatal("read after close", err)
		}
	}
	var warmHigh uint32
	for i := 0; i < 50; i++ {
		cycle(i%2 == 0)
		if n := count(); n > warmHigh {
			warmHigh = n
		}
	}
	time.Sleep(50 * time.Millisecond)
	before := count()
	var high uint32
	for i := 0; i < 100; i++ {
		cycle(i%2 == 0)
		if i%10 == 9 {
			time.Sleep(10 * time.Millisecond)
			n := count()
			if n > high {
				high = n
			}
			t.Logf("overlapped cycle=%d handles=%d", i+1, n)
		}
	}
	time.Sleep(50 * time.Millisecond)
	after := count()
	t.Logf("overlapped_cycles=100 handles warmup_max=%d before=%d after=%d sampled_max=%d", warmHigh, before, after, high)
	if after > warmHigh+4 || high > warmHigh+4 {
		t.Fatal("overlapped lifecycle handle growth")
	}
}

func TestNativeOverlappedDeadline(t *testing.T) {
	m := ioModule(t)
	h, err := m.openIO("false", 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := h.ReceiveIPv4(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("deadline did not cancel native read")
	}
	if _, err := h.ReceiveIPv4(nil); !errors.Is(err, ErrPacket) {
		t.Fatal("nil context", err)
	}
}

func TestNativeForwardAndExactReplyCapture(t *testing.T) {
	m := ioModule(t)
	source, err := netip.ParseAddr(os.Getenv("IRVIO_DIVERT_IO_SOURCE"))
	if err != nil {
		t.Skip("requires explicit lab physical source")
	}
	oracle, err := netip.ParseAddrPort(os.Getenv("IRVIO_DIVERT_IO_ORACLE"))
	if err != nil || !oracle.Addr().Is4() || !oracle.Addr().IsPrivate() {
		t.Skip("requires isolated private lab UDP echo oracle")
	}
	b, err := winroute.ForSource(source)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := m.OpenForwardSender(b)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sender.Close(); err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 5; i++ {
		lc := net.ListenConfig{Control: func(network, address string, raw syscall.RawConn) error {
			if err := b.Control(network, address, raw); err != nil {
				return err
			}
			var option error
			if err := raw.Control(func(fd uintptr) {
				option = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, ^windows.SO_REUSEADDR, 1)
			}); err != nil {
				return err
			}
			return option
		}}
		conn, err := lc.ListenPacket(context.Background(), "udp4", net.JoinHostPort(source.String(), "0"))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		port := uint16(conn.LocalAddr().(*net.UDPAddr).Port)
		receiver, err := m.OpenReplyCapture(b, 17, port)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := receiver.Close(); err != nil {
				t.Error(err)
			}
		}()
		token := []byte(fmt.Sprintf("irvio-native-io-%d-%d", i, time.Now().UnixNano()))
		packet := testUDP(source, oracle.Addr(), port, oracle.Port(), token)
		wrongSource := append([]byte(nil), packet...)
		wrongSource[12] ^= 1
		if err := sender.SendIPv4(context.Background(), wrongSource); !errors.Is(err, ErrPacket) {
			t.Fatal("wrong source accepted", err)
		}
		canceled, abort := context.WithCancel(context.Background())
		abort()
		if err := sender.SendIPv4(canceled, packet); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled send accepted", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		if err := sender.SendIPv4(ctx, packet); err != nil {
			cancel()
			t.Fatal(err)
		}
		reply, err := receiver.ReceiveIPv4(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if len(reply) < 28 || netip.AddrFrom4([4]byte(reply[12:16])) != oracle.Addr() || binary.BigEndian.Uint16(reply[20:22]) != oracle.Port() || string(reply[28:]) != string(token) {
			t.Fatal("reply mismatch")
		}
		if err := conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if n, _, err := conn.ReadFrom(make([]byte, 256)); err == nil || n != 0 {
			t.Fatal("diverted reply also reached host socket")
		}
		t.Logf("physical UDP probe=%d bytes=%d ifindex=%d", i+1, len(reply), b.Index)
	}
}

func testUDP(source, destination netip.Addr, sport, dport uint16, data []byte) []byte {
	b := make([]byte, 28+len(data))
	b[0] = 0x45
	b[8] = 64
	b[9] = 17
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	s, d := source.As4(), destination.As4()
	copy(b[12:16], s[:])
	copy(b[16:20], d[:])
	binary.BigEndian.PutUint16(b[20:22], sport)
	binary.BigEndian.PutUint16(b[22:24], dport)
	binary.BigEndian.PutUint16(b[24:26], uint16(len(b)-20))
	copy(b[28:], data)
	sum := uint32(0)
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(b[10:12], ^uint16(sum))
	return b
}
