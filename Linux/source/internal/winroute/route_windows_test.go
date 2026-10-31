// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPinnedSDKLayouts(t *testing.T) {
	var iface windows.MibIfRow2
	var unicast windows.MibUnicastIpAddressRow
	t.Logf("MibIfRow2 size=%d oper_status=%d; MibUnicastIpAddressRow size=%d dad_state=%d", unsafe.Sizeof(iface), unsafe.Offsetof(iface.OperStatus), unsafe.Sizeof(unicast), unsafe.Offsetof(unicast.DadState))
	if unsafe.Sizeof(iface) != 1352 || unsafe.Offsetof(iface.OperStatus) != 1156 || unsafe.Sizeof(unicast) != 80 || unsafe.Offsetof(unicast.DadState) != 64 {
		t.Fatal("native structures differ from the verified x64 Windows SDK layout")
	}
}

func TestBindingRejectsInvalidSourcesAndStaleIdentity(t *testing.T) {
	for _, source := range []string{"0.0.0.0", "::", "224.0.0.1", "fe80::1", "::ffff:127.0.0.1", "192.0.2.111"} {
		if _, err := ForSource(netip.MustParseAddr(source)); err == nil {
			t.Fatalf("source %s accepted", source)
		}
	}
	b, err := ForSource(netip.MustParseAddr("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Binding){
		func(b *Binding) { b.Index++ }, func(b *Binding) { b.LUID = 0 },
		func(b *Binding) { b.Source = netip.MustParseAddr("192.0.2.111") },
	} {
		invalid := b
		change(&invalid)
		if _, err := invalid.validate(); err == nil {
			t.Fatalf("invalid binding accepted: %+v", invalid)
		}
	}
	if err := b.Control("udp6", "", nil); err == nil {
		t.Fatal("family mismatch accepted")
	}
	if err := b.Control("tcp", "", nil); err == nil {
		t.Fatal("ambiguous family accepted")
	}
}

func TestBindingRejectsRoutingModesThatInvalidateSourceFence(t *testing.T) {
	for _, source := range []string{"192.0.2.1", "2001:db8::1"} {
		b := Binding{LUID: 123, Index: 6, Source: netip.MustParseAddr(source)}
		valid := windows.MibIpInterfaceRow{Family: addressFamily(b.Source), InterfaceLuid: b.LUID, InterfaceIndex: b.Index, Connected: 1}
		if err := b.validateIPInterface(valid); err != nil {
			t.Fatal(err)
		}
		for _, change := range []func(*windows.MibIpInterfaceRow){
			func(r *windows.MibIpInterfaceRow) { r.ForwardingEnabled = 1 },
			func(r *windows.MibIpInterfaceRow) { r.WeakHostSend = 1 },
			func(r *windows.MibIpInterfaceRow) { r.Connected = 0 },
			func(r *windows.MibIpInterfaceRow) { r.InterfaceLuid++ },
			func(r *windows.MibIpInterfaceRow) { r.InterfaceIndex++ },
			func(r *windows.MibIpInterfaceRow) { r.Family = windows.AF_UNSPEC },
		} {
			invalid := valid
			change(&invalid)
			if err := b.validateIPInterface(invalid); err == nil {
				t.Fatalf("unsafe IP interface accepted: %+v", invalid)
			}
		}
	}
}

func TestBoundTCPAndUDPLoopback(t *testing.T) {
	for _, source := range []string{"127.0.0.1", "::1"} {
		t.Run(source, func(t *testing.T) {
			b, err := ForSource(netip.MustParseAddr(source))
			if err != nil {
				t.Fatal(err)
			}
			suffix := "6"
			if b.Source.Is4() {
				suffix = "4"
			}
			listen := net.ListenConfig{Control: b.Control}
			udp, err := listen.ListenPacket(context.Background(), "udp"+suffix, net.JoinHostPort(source, "0"))
			if err != nil {
				t.Fatal(err)
			}
			defer udp.Close()
			raw, err := udp.(*net.UDPConn).SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			index, err := SocketInterface(raw, b.Source.Is4())
			if err != nil || index != b.Index {
				t.Fatalf("native egress index=%d want=%d err=%v", index, b.Index, err)
			}
			if _, err := udp.WriteTo([]byte("bound"), udp.LocalAddr()); err != nil {
				t.Fatal(err)
			}
			udp.SetReadDeadline(time.Now().Add(time.Second))
			buffer := make([]byte, 32)
			if n, _, err := udp.ReadFrom(buffer); err != nil || string(buffer[:n]) != "bound" {
				t.Fatalf("bound UDP: %q %v", buffer, err)
			}
			listener, err := net.Listen("tcp"+suffix, net.JoinHostPort(source, "0"))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			dialer := net.Dialer{Control: b.Control, LocalAddr: &net.TCPAddr{IP: net.IP(b.Source.AsSlice())}, Timeout: time.Second}
			conn, err := dialer.Dial("tcp"+suffix, listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			accepted, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			accepted.Close()
		})
	}
}

func TestUnderlayRejectsOwnAddressAndInvalidTarget(t *testing.T) {
	for _, target := range []string{"0.0.0.0", "127.0.0.1", "::1", "ff02::1", "fe80::1"} {
		if _, err := ResolveUnderlay(netip.MustParseAddr(target), 0, nil); err == nil {
			t.Fatalf("target %s accepted", target)
		}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			t.Fatal(err)
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ResolveUnderlay(prefix.Addr(), 0, nil); err == nil {
				t.Fatalf("own address %s accepted as WAN target", address)
			}
		}
	}
}

func TestNativeUnderlayTarget(t *testing.T) {
	value := os.Getenv("IRVIO_WINROUTE_TARGET")
	if value == "" {
		t.Skip("set IRVIO_WINROUTE_TARGET to the lab oracle literal IP")
	}
	target := netip.MustParseAddr(value)
	path, err := ResolveUnderlay(target, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native path %+v", path)
	constrained, err := ResolveUnderlay(target, path.Binding.LUID, nil)
	if err != nil || constrained.Binding != path.Binding {
		t.Fatalf("constrained path: %+v %v", constrained, err)
	}
	if _, err := ResolveUnderlay(target, 0, []uint64{path.Binding.LUID}); err == nil {
		t.Fatal("excluded interface used")
	}
	dialer := net.Dialer{Control: path.Binding.Control, LocalAddr: &net.TCPAddr{IP: net.IP(path.Binding.Source.AsSlice())}, Timeout: 3 * time.Second}
	conn, err := dialer.Dial("tcp4", fmt.Sprintf("%s:49494", target))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.0\r\nHost: lab\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 16)
	n, err := conn.Read(buffer)
	if err != nil || n < 5 || string(buffer[:5]) != "HTTP/" {
		t.Fatalf("oracle response %q %v", buffer[:n], err)
	}
}
