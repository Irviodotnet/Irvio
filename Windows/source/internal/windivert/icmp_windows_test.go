// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windivert

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func nativeErrorPacket(source netip.Addr, port uint16, protocol byte) []byte {
	q := testUDP(source, netip.MustParseAddr("198.51.100.77"), port, 443, nil)
	if protocol == 6 {
		q[9] = protocol
		binary.BigEndian.PutUint16(q[2:4], 40)
		q[10], q[11] = 0, 0
		binary.BigEndian.PutUint16(q[10:12], icmpTestChecksum(q[:20]))
	}
	b := make([]byte, 28+len(q))
	b[0], b[8], b[9], b[20], b[21] = 0x45, 64, 1, 3, 4
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	copy(b[12:16], netip.MustParseAddr("10.0.2.2").AsSlice())
	copy(b[16:20], source.AsSlice())
	binary.BigEndian.PutUint16(b[26:28], 1280)
	copy(b[28:], q)
	binary.BigEndian.PutUint16(b[10:12], icmpTestChecksum(b[:20]))
	binary.BigEndian.PutUint16(b[22:24], icmpTestChecksum(b[20:]))
	return b
}

func icmpTestChecksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) > 0 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&65535 + sum>>16
	}
	return ^uint16(sum)
}

func TestNativeICMPErrorFilter(t *testing.T) {
	m := ioModule(t)
	eval, err := m.dll.FindProc("WinDivertHelperEvalFilter")
	if err != nil {
		t.Fatal(err)
	}
	binding := winroute.Binding{Index: 9, Source: netip.MustParseAddr("203.0.113.9")}
	for _, protocol := range []byte{6, 17} {
		filter := append([]byte(replyFilter(binding, protocol, 40000)), 0)
		for _, change := range []string{"match", "port", "protocol", "quoted-source", "recipient", "interface", "outbound", "redirect", "echo", "truncated", "options"} {
			t.Run(fmt.Sprint(protocol, "-", change), func(t *testing.T) {
				b := nativeErrorPacket(binding.Source, 40000, protocol)
				a := nativeAddress{IfIndex: binding.Index}
				switch change {
				case "port":
					b[49]++
				case "protocol":
					b[37] ^= 6 ^ 17
				case "quoted-source":
					b[43]++
				case "recipient":
					b[19]++
				case "interface":
					a.IfIndex++
				case "outbound":
					a.Flags = 1 << 17
				case "redirect":
					b[20] = 5
				case "echo":
					b[20] = 0
				case "truncated":
					b = b[:48]
					binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
				case "options":
					b[28] = 0x46
				}
				ok, _, callErr := eval.Call(uintptr(unsafe.Pointer(&filter[0])), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&a)))
				runtime.KeepAlive(filter)
				runtime.KeepAlive(b)
				runtime.KeepAlive(a)
				if callErr != syscall.Errno(0) || (ok != 0) != (change == "match") {
					t.Fatal("filter mismatch", ok, callErr)
				}
			})
		}
	}
}

func TestNativeICMPErrorCapture(t *testing.T) {
	m := ioModule(t)
	source, err := netip.ParseAddr(os.Getenv("IRVIO_DIVERT_IO_SOURCE"))
	if err != nil {
		t.Skip("requires explicit isolated lab NIC")
	}
	b, err := winroute.ForSource(source)
	if err != nil {
		t.Fatal(err)
	}
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
	defer receiver.Close()
	// Lab-only inbound injection cannot send outside this VM and owns no receive filter.
	injector, err := m.openIO("false", 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer injector.Close()
	packet := nativeErrorPacket(source, port, 17)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a := nativeAddress{IfIndex: b.Index}
	if n, err := injector.transfer(ctx, packet, &a, true); err != nil || n != len(packet) {
		t.Fatal("inject ICMP", n, err)
	}
	reply, err := receiver.ReceiveIPv4(ctx)
	if err != nil || !bytes.Equal(reply, packet) {
		t.Fatal("exact owned ICMP capture", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if n, _, err := conn.ReadFrom(make([]byte, 256)); n != 0 || err == nil {
		t.Fatal("captured ICMP also reached reserved host socket")
	}
	t.Log("native ICMP fragmentation-needed capture PASS; exact exclusive UDP tuple; unrelated filters excluded")
}
