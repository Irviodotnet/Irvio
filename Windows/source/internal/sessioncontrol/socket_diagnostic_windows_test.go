// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitnat"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func TestTCPPortDiagnosticJournalBounds(t *testing.T) {
	d := &tunnelListenDiagnostic{cause: windows.WSAEACCES, elapsed: time.Second}
	d.v4.Code, d.v6.Code = ^uint32(0), ^uint32(0)
	d.v4.Matches, d.v6.Matches = ^uint32(0), ^uint32(0)
	for i := 0; i < tcpDiagnosticMaxRows; i++ {
		d.v4.Owners = append(d.v4.Owners, tcpPortOwner{"other-local", ^uint32(0), ^uint32(0)})
		d.v6.Owners = append(d.v6.Owners, tcpPortOwner{"wildcard", ^uint32(0), ^uint32(0)})
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	LogListenDiagnostics(logger, fmt.Errorf("outer: %w", d))
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte{'\n'})
	if len(lines) != 18 {
		t.Fatalf("unexpected diagnostic count: %d", len(lines))
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		reason, ok := record["reason"].(string)
		if !ok || len(reason) > 192 || len(record) != 4 {
			t.Fatalf("journal-unfriendly record: %s", line)
		}
	}
	if !errors.Is(d, windows.WSAEACCES) {
		t.Fatal("lost original cause")
	}
	output.Reset()
	LogListenDiagnostics(logger, errors.New("unrelated"))
	LogListenDiagnostics(nil, d)
	if output.Len() != 0 {
		t.Fatal("unexpected diagnostic")
	}
}

func TestTCPPortSnapshotParser(t *testing.T) {
	for _, family := range []uint32{windows.AF_INET, windows.AF_INET6} {
		rowSize, address, port, state, pid := 24, 4, 8, 0, 20
		target := netip.MustParseAddrPort("10.253.248.1:51822")
		if family == windows.AF_INET6 {
			rowSize, address, port, state, pid = 56, 0, 20, 48, 52
			target = netip.MustParseAddrPort("[fd77:248::1]:51822")
		}
		buffer := make([]byte, 4+12*rowSize)
		binary.LittleEndian.PutUint32(buffer, 12)
		for i := 0; i < 12; i++ {
			row := buffer[4+i*rowSize:]
			if i != 0 {
				copy(row[address:], target.Addr().AsSlice())
			}
			binary.BigEndian.PutUint16(row[port:], 51822)
			binary.LittleEndian.PutUint32(row[state:], 2)
			binary.LittleEndian.PutUint32(row[pid:], uint32(123+i))
		}
		binary.BigEndian.PutUint16(buffer[4+11*rowSize+port:], 51823)
		got := parseTCPPortSnapshot(buffer, family, target)
		if got.Code != 0 || got.Matches != 11 || len(got.Owners) != 8 || got.Owners[0] != (tcpPortOwner{"wildcard", 2, 123}) || got.Owners[1] != (tcpPortOwner{"target", 2, 124}) {
			t.Fatalf("family %d: %+v", family, got)
		}
		for _, corrupt := range [][]byte{nil, buffer[:3], buffer[:len(buffer)-1]} {
			if got := parseTCPPortSnapshot(corrupt, family, target); got.Code != uint32(windows.ERROR_INVALID_DATA) || len(got.Owners) != 0 {
				t.Fatalf("accepted malformed table: %+v", got)
			}
		}
		binary.LittleEndian.PutUint32(buffer, ^uint32(0))
		if got := parseTCPPortSnapshot(buffer, family, target); got.Code == 0 {
			t.Fatal("accepted overflowing row count")
		}
	}
	if got := parseTCPPortSnapshot(make([]byte, 4), 999, netip.AddrPort{}); got.Code == 0 {
		t.Fatal("accepted invalid family/endpoint")
	}
	cause := errors.New("unrelated error")
	if diagnoseTunnelListen(cause, netip.AddrPort{}) != cause {
		t.Fatal("modified unrelated failure")
	}
}

func TestNativeSessionBindConflictDiagnostic(t *testing.T) {
	owner := sessionNativeOwner(t)
	for _, tc := range []struct{ network, wildcard, target string }{
		{"tcp4", "0.0.0.0:0", "10.253.248.1"},
		{"tcp6", "[::]:0", "fd77:248::1"},
	} {
		t.Run(tc.network, func(t *testing.T) {
			lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
				var optionErr error
				err := raw.Control(func(fd uintptr) {
					optionErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, ^windows.SO_REUSEADDR, 1)
				})
				return errors.Join(err, optionErr)
			}}
			blocker, err := lc.Listen(context.Background(), tc.network, tc.wildcard)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { blocker.Close() })
			target := netip.AddrPortFrom(netip.MustParseAddr(tc.target), blocker.Addr().(*net.TCPAddr).AddrPort().Port())
			listener, err := ListenTunnel(context.Background(), tc.network, target.String(), owner.Name())
			if listener != nil {
				listener.Close()
				t.Fatal("exclusive wildcard did not reject conflicting listener")
			}
			if !errors.Is(err, windows.WSAEACCES) && !errors.Is(err, windows.WSAEADDRINUSE) {
				t.Fatalf("missing original Winsock cause: %v", err)
			}
			if !strings.Contains(err.Error(), "AddressClass:wildcard") || !strings.Contains(err.Error(), "TCP port observation") {
				t.Fatalf("missing synchronous conflict observation: %v", err)
			}
			family := uint32(windows.AF_INET)
			if tc.network == "tcp6" {
				family = windows.AF_INET6
			}
			snapshot := readTCPPortSnapshot(family, target)
			found := false
			for _, row := range snapshot.Owners {
				found = found || (row.PID == uint32(os.Getpid()) && row.State == 2 && row.AddressClass == "wildcard")
			}
			if snapshot.Code != 0 || !found {
				t.Fatalf("owner missing: %+v", snapshot)
			}
			t.Log(err)
			if err := blocker.Close(); err != nil {
				t.Fatal(err)
			}
			listener, err = ListenTunnel(context.Background(), tc.network, target.String(), owner.Name())
			if err != nil {
				t.Fatal(err)
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeSessionNATReservationDifferentSource(t *testing.T) {
	source := os.Getenv("IRVIO_WINDOWS_NAT_PORT_SOURCE")
	if source == "" {
		t.Skip("requires isolated physical IPv4 source")
	}
	owner := sessionNativeOwner(t)
	binding, err := winroute.ForSource(netip.MustParseAddr(source))
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := exitnat.ReserveWindows(context.Background(), exitnat.Path{Generation: 1, Interface: binding.LUID, Source: binding.Source}, exitnat.TCP)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reservation.Close(); err != nil {
			t.Error(err)
		}
	})
	target := netip.AddrPortFrom(netip.MustParseAddr("10.253.248.1"), reservation.Port())
	listener, err := ListenTunnel(context.Background(), "tcp4", target.String(), owner.Name())
	if err != nil {
		t.Fatalf("physical-source reservation blocked distinct TUN source: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
