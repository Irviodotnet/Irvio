// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"golang.org/x/sys/windows"
)

func TestForwardSDKLayout(t *testing.T) {
	var row windows.MibIpForwardRow2
	got := []uintptr{unsafe.Sizeof(row), unsafe.Offsetof(row.InterfaceLuid), unsafe.Offsetof(row.InterfaceIndex), unsafe.Offsetof(row.DestinationPrefix), unsafe.Offsetof(row.NextHop), unsafe.Offsetof(row.SitePrefixLength), unsafe.Offsetof(row.ValidLifetime), unsafe.Offsetof(row.PreferredLifetime), unsafe.Offsetof(row.Metric), unsafe.Offsetof(row.Protocol), unsafe.Offsetof(row.Loopback), unsafe.Offsetof(row.AutoconfigureAddress), unsafe.Offsetof(row.Publish), unsafe.Offsetof(row.Immortal), unsafe.Offsetof(row.Age), unsafe.Offsetof(row.Origin)}
	want := []uintptr{104, 0, 8, 12, 44, 72, 76, 80, 84, 88, 92, 93, 94, 95, 96, 100}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("route SDK ABI field %d: %d != %d", i, got[i], want[i])
		}
	}
}

func forwardCall(name string, row *windows.MibIpForwardRow2) error {
	p := windows.NewLazySystemDLL("iphlpapi.dll").NewProc(name)
	if err := p.Find(); err != nil {
		return err
	}
	r, _, _ := p.Call(uintptr(unsafe.Pointer(row)))
	if r != 0 && name != "InitializeIpForwardEntry" {
		return fmt.Errorf("%s: %w", name, windows.Errno(r))
	}
	return nil
}

// This disposable-VM experiment never replaces/deletes a physical interface route.
func TestNativeSplitRoutesPreservePinnedUnderlay(t *testing.T) {
	if os.Getenv("IRVIO_ROUTE_NATIVE_TEST") != "1" {
		t.Skip("requires disposable Windows VM, Guest Agent and mini-PC echo oracle")
	}
	target := netip.MustParseAddr("192.168.23.2")
	physical, err := ResolveUnderlay(target, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if physical.Binding.Source.String() != "10.0.2.15" {
		t.Fatal("not the isolated QEMU lab")
	}
	controlEcho := func(stage string) {
		t.Helper()
		dialer := net.Dialer{Timeout: 3 * time.Second, LocalAddr: &net.UDPAddr{IP: net.IP(physical.Binding.Source.AsSlice())}, Control: physical.Binding.Control}
		conn, err := dialer.DialContext(context.Background(), "udp4", "192.168.23.2:49495")
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		payload := []byte("irvio-nat-wfp-route-" + stage)
		if _, err := conn.Write(payload); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 512)
		n, err := conn.Read(buffer)
		if err != nil || !bytes.Equal(buffer[:n], payload) {
			t.Fatalf("physical control echo %s: %v", stage, err)
		}
		t.Logf("physical pinned echo %s PASS", stage)
	}
	controlEcho("baseline")
	owner, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-W05-Route", Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.246.1/24"), netip.MustParsePrefix("fd77:246::1/64")}})
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
	t.Cleanup(func() { device.Close() })
	var added []windows.MibIpForwardRow2
	cleanup := func() {
		t.Helper()
		for i := len(added) - 1; i >= 0; i-- {
			row := added[i]
			if err := forwardCall("DeleteIpForwardEntry2", &row); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
				t.Error(err)
			}
			if err := windows.GetIpForwardEntry2(&row); !errors.Is(err, windows.ERROR_NOT_FOUND) {
				t.Errorf("owned route remains: %v", err)
			}
		}
		added = nil
	}
	t.Cleanup(cleanup)
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"} {
		p := netip.MustParsePrefix(prefix)
		var row windows.MibIpForwardRow2
		if err := forwardCall("InitializeIpForwardEntry", &row); err != nil {
			t.Fatal(err)
		}
		row.InterfaceLuid = owner.LUID()
		row.DestinationPrefix.Prefix = nativeAddress(p.Addr())
		row.DestinationPrefix.PrefixLength = uint8(p.Bits())
		zero := netip.IPv6Unspecified()
		if p.Addr().Is4() {
			zero = netip.IPv4Unspecified()
		}
		row.NextHop = nativeAddress(zero)
		row.SitePrefixLength, row.Metric, row.Protocol = 0, 3, windows.MIB_IPPROTO_NETMGMT
		row.Loopback, row.AutoconfigureAddress, row.Publish, row.Immortal = 0, 0, 0, 1
		if err := forwardCall("CreateIpForwardEntry2", &row); err != nil {
			t.Fatal(err)
		}
		added = append(added, row)
		actual := row
		if err := windows.GetIpForwardEntry2(&actual); err != nil {
			t.Fatal(err)
		}
		if actual.InterfaceLuid != row.InterfaceLuid || actual.Metric != row.Metric || actual.Protocol != row.Protocol || actual.Publish != 0 || actual.Loopback != 0 || actual.AutoconfigureAddress != 0 {
			t.Fatalf("unexpected native route: %+v", actual)
		}
		t.Logf("owned split route %s metric=%d protocol=%d origin=%d lifetime=%d immortal=%d", prefix, actual.Metric, actual.Protocol, actual.Origin, actual.ValidLifetime, actual.Immortal)
	}
	for _, target := range []netip.Addr{target, netip.MustParseAddr("203.0.113.25"), netip.MustParseAddr("2001:db8::25")} {
		row, _, err := bestRoute(target, 0)
		if err != nil || row.InterfaceLuid != owner.LUID() {
			t.Fatalf("ordinary route for %s not on TUN: %v", target, err)
		}
	}
	after, err := ResolveUnderlay(target, physical.Binding.LUID, []uint64{owner.LUID()})
	if err != nil || after.Binding != physical.Binding || after.Gateway != physical.Gateway {
		t.Fatalf("constrained physical path changed: %v", err)
	}
	controlEcho("split-installed")
	for _, address := range []string{"203.0.113.25", "2001:db8::25"} {
		network := "udp4"
		if netip.MustParseAddr(address).Is6() {
			network = "udp6"
		}
		conn, err := net.DialTimeout(network, net.JoinHostPort(address, "49200"), 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		payload := []byte("irvio-route-tun-" + network)
		found := make(chan error, 1)
		go func() {
			buf := make([]byte, 65535)
			sizes := []int{0}
			for {
				_, err := device.Read([][]byte{buf}, sizes, 0)
				if err != nil {
					found <- err
					return
				}
				if bytes.HasSuffix(buf[:sizes[0]], payload) {
					found <- nil
					return
				}
			}
		}()
		_, err = conn.Write(payload)
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-found:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("ordinary UDP did not reach Wintun")
		}
		t.Logf("ordinary %s routed to TUN PASS", network)
	}
	cleanup()
	after, err = ResolveUnderlay(target, 0, []uint64{owner.LUID()})
	if err != nil || after.Binding != physical.Binding || after.Gateway != physical.Gateway {
		t.Fatalf("default path not restored: %v", err)
	}
	controlEcho("restored")
}
