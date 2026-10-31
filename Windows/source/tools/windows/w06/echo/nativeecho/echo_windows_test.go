// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package nativeecho

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"os"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func TestReplyBoundary(t *testing.T) {
	payload := []byte("irvio-native-echo")
	target := netip.MustParseAddr("1.1.1.1")
	for _, kind := range []string{"valid", "empty", "status", "source", "ttl", "size", "pointer-before", "pointer-after", "pointer-header", "truncated", "data"} {
		t.Run(kind, func(t *testing.T) {
			data := payload
			if kind == "empty" {
				data = nil
			}
			b := make([]byte, 40+len(data))
			copy(b[:4], target.AsSlice())
			b[24] = 64
			binary.LittleEndian.PutUint16(b[12:14], uint16(len(data)))
			base := uintptr(unsafe.Pointer(&b[0]))
			binary.LittleEndian.PutUint64(b[16:24], uint64(base+40))
			copy(b[40:], data)
			switch kind {
			case "status":
				binary.LittleEndian.PutUint32(b[4:8], 11010)
			case "source":
				b[3]++
			case "ttl":
				b[24] = 0
			case "size":
				b[12]++
			case "pointer-before":
				binary.LittleEndian.PutUint64(b[16:24], uint64(base-1))
			case "pointer-after":
				binary.LittleEndian.PutUint64(b[16:24], uint64(base)+uint64(len(b))+1)
			case "pointer-header":
				binary.LittleEndian.PutUint64(b[16:24], uint64(base+16))
			case "truncated":
				b = b[:39]
			case "data":
				b[40]++
			}
			r, err := decodeReply(b, target, data)
			runtime.KeepAlive(b)
			if kind == "valid" || kind == "empty" {
				if err != nil || !bytes.Equal(r.Data, data) {
					t.Fatal(r, err)
				}
			} else if kind == "status" {
				if !errors.Is(err, StatusError(11010)) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrReply) {
				t.Fatal("malformed native result accepted", err)
			}
		})
	}
}

func TestBeginValidation(t *testing.T) {
	options := Options{TTL: 64, Timeout: time.Second}
	for _, target := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "224.0.0.1", "::1"} {
		if r, err := Begin(context.Background(), winroute.Binding{}, netip.MustParseAddr(target), nil, options); err == nil || r != nil {
			t.Fatal(target, r, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Begin(ctx, winroute.Binding{}, netip.MustParseAddr("1.1.1.1"), nil, options); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativeEchoOwnershipAndCancellation(t *testing.T) {
	if os.Getenv("IRVIO_NATIVE_ECHO_TEST") != "1" {
		t.Skip("requires isolated SYSTEM VM with working physical ICMP oracle")
	}
	target := netip.MustParseAddr("1.1.1.1")
	path, err := winroute.ResolveUnderlay(target, 0, nil)
	if err != nil || path.Binding.Source.String() != "10.0.2.15" {
		t.Fatal("wrong lab path", err)
	}
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	count := func() uint32 {
		var n uint32
		ok, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
		if ok == 0 {
			t.Fatal(err)
		}
		return n
	}
	cycle := func(index int, cancel bool) {
		data := bytes.Repeat([]byte{byte(index)}, []int{0, 1, 32, 512, 1024}[index%5])
		r, err := Begin(context.Background(), path.Binding, target, data, Options{TTL: 63, DontFragment: true, Timeout: 1200 * time.Millisecond})
		if r != nil {
			defer func() {
				if err := r.Close(); err != nil {
					t.Error(err)
				}
			}()
		}
		if err != nil {
			t.Fatal("begin", err)
		}
		ctx, stop := context.WithCancel(context.Background())
		defer stop()
		if cancel {
			stop()
		}
		start := time.Now()
		result, err := r.Finish(ctx)
		if cancel {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
		} else if err != nil || !bytes.Equal(result.Data, data) {
			t.Fatal("echo mismatch", err)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatal("unbounded completion drain")
		}
		if r.handle != 0 || r.event != 0 {
			t.Fatal("native handle residue")
		}
	}
	for i := 0; i < 10; i++ {
		cycle(i, false)
	}
	before := count()
	for i := 0; i < 100; i++ {
		cycle(i, i%3 == 0)
	}
	after := count()
	if after > before+2 {
		t.Fatalf("handle growth %d -> %d", before, after)
	}
	t.Logf("100 native echo requests/cancellation: handles %d -> %d", before, after)
}
