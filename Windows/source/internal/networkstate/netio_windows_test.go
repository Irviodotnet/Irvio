// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package networkstate

import (
	"context"
	"errors"
	"net/netip"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func nativeAddress(address string) windows.RawSockaddrInet {
	a := netip.MustParseAddr(address)
	var raw windows.RawSockaddrInet
	if a.Is4() {
		p := (*windows.RawSockaddrInet4)(unsafe.Pointer(&raw))
		p.Family, p.Addr = windows.AF_INET, a.As4()
	} else {
		p := (*windows.RawSockaddrInet6)(unsafe.Pointer(&raw))
		p.Family, p.Addr = windows.AF_INET6, a.As16()
	}
	return raw
}

func nativeDefault(index uint32, luid uint64, metric uint32, gateway string) windows.MibIpForwardRow2 {
	dest := "::"
	if netip.MustParseAddr(gateway).Is4() {
		dest = "0.0.0.0"
	}
	return windows.MibIpForwardRow2{InterfaceIndex: index, InterfaceLuid: luid, Metric: metric, ValidLifetime: ^uint32(0),
		DestinationPrefix: windows.IpAddressPrefix{Prefix: nativeAddress(dest)}, NextHop: nativeAddress(gateway)}
}

func nativeAdapter(index uint32, luid uint64, metric uint32) windowsAdapter {
	return windowsAdapter{index4: index, index6: index, metric4: metric, metric6: metric, mtu4: 1500, mtu6: 1480,
		link: Link{NativeID: luid, Name: "Ethernet", Up: true, MTU: 1500,
			Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24"), netip.MustParsePrefix("2001:db8::2/64")}}}
}

func TestWindowsSnapshotEffectiveMetricsAndFamilies(t *testing.T) {
	a := []windowsAdapter{nativeAdapter(4, 400, 600), nativeAdapter(7, 700, 100)}
	r := []windows.MibIpForwardRow2{nativeDefault(4, 400, 0, "192.0.2.1"), nativeDefault(7, 700, 10, "192.0.2.254"),
		nativeDefault(7, 700, 20, "fe80::1"), nativeDefault(4, 400, 30, "::")}
	s, err := windowsSnapshot(a, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Routes) != 4 || len(s.Links) != 2 || s.Routes[0].LinkIndex != 7 || s.Routes[0].Priority != 110 {
		t.Fatalf("incorrect effective route preference: %+v", s)
	}
	if s.Routes[2].Family != FamilyIPv6 || s.Routes[2].Gateway != netip.MustParseAddr("fe80::1") || s.Routes[3].Gateway.IsValid() {
		t.Fatalf("incorrect IPv6/on-link mapping: %+v", s.Routes)
	}
	if s.Links[0].NativeID != 400 || s.Routes[0].PreferredSource.IsValid() {
		t.Fatal("LUID lost or source address invented")
	}
	if s.Links[0].MTU != 1480 || s.Routes[0].MTU != 1500 || s.Routes[2].MTU != 1480 {
		t.Fatalf("IP family MTUs lost: %+v", s)
	}
	before := s.Clone()
	a[0].mtu4 = 1490
	after, err := windowsSnapshot(a, r)
	if err != nil {
		t.Fatal(err)
	}
	if before.transportEquivalent(after) {
		t.Fatal("per-family MTU change above link minimum was lost")
	}
}

func TestWindowsSnapshotSkipsNonDefaultExpiredAndLoopback(t *testing.T) {
	r := nativeDefault(4, 400, 0, "192.0.2.1")
	for _, change := range []func(*windows.MibIpForwardRow2){
		func(r *windows.MibIpForwardRow2) { r.DestinationPrefix.PrefixLength = 24 },
		func(r *windows.MibIpForwardRow2) { r.Loopback = 1 },
		func(r *windows.MibIpForwardRow2) { r.ValidLifetime = 0 },
	} {
		row := r
		change(&row)
		s, err := windowsSnapshot(nil, []windows.MibIpForwardRow2{row})
		if err != nil || len(s.Routes) != 0 || len(s.Links) != 0 {
			t.Fatalf("unexpected snapshot: %+v %v", s, err)
		}
	}
}

func TestWindowsSnapshotRefusesReusedIndex(t *testing.T) {
	a := []windowsAdapter{nativeAdapter(4, 401, 100)}
	_, err := windowsSnapshot(a, []windows.MibIpForwardRow2{nativeDefault(4, 400, 0, "192.0.2.1")})
	if !errors.Is(err, errNetworkTableChanged) {
		t.Fatalf("reused index accepted: %v", err)
	}
}

func TestWindowsScanRetriesInconsistentTablesBoundedly(t *testing.T) {
	calls := 0
	routes := func() ([]windows.MibIpForwardRow2, error) {
		return []windows.MibIpForwardRow2{nativeDefault(4, 400, 0, "192.0.2.1")}, nil
	}
	adapters := func() ([]windowsAdapter, error) {
		calls++
		if calls == 1 {
			return nil, nil
		}
		return []windowsAdapter{nativeAdapter(4, 400, 100)}, nil
	}
	if _, err := scanWindowsTables(context.Background(), adapters, routes); err != nil || calls != 2 {
		t.Fatalf("retry result: calls=%d err=%v", calls, err)
	}
	calls = 0
	_, err := scanWindowsTables(context.Background(), func() ([]windowsAdapter, error) { calls++; return nil, nil }, routes)
	if !errors.Is(err, errNetworkTableChanged) || calls != 3 {
		t.Fatalf("unbounded/missing failure: %d %v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = scanWindowsTables(ctx, adapters, routes)
	if !errors.Is(err, context.Canceled) || calls != 3 {
		t.Fatalf("cancel ignored: %d %v", calls, err)
	}
}

func TestWindowsUnicastReadiness(t *testing.T) {
	var raw syscall.RawSockaddrAny
	ipv4 := (*windows.RawSockaddrInet4)(unsafe.Pointer(&raw))
	ipv4.Family, ipv4.Addr = windows.AF_INET, [4]byte{192, 0, 2, 2}
	u := windows.IpAdapterUnicastAddress{DadState: windows.IpDadStatePreferred, ValidLifetime: 100, OnLinkPrefixLength: 24,
		Address: windows.SocketAddress{Sockaddr: &raw, SockaddrLength: int32(unsafe.Sizeof(windows.RawSockaddrInet4{}))}}
	if prefix, ok := windowsUnicastPrefix(&u); !ok || prefix.String() != "192.0.2.2/24" {
		t.Fatalf("usable address missing: %v %v", prefix, ok)
	}
	for _, state := range []int32{windows.IpDadStateTentative, windows.IpDadStateDuplicate, windows.IpDadStateDeprecated, windows.IpDadStateInvalid} {
		u.DadState = state
		if _, ok := windowsUnicastPrefix(&u); ok {
			t.Fatalf("unready DAD %d accepted", state)
		}
	}
	u.DadState, u.ValidLifetime = windows.IpDadStatePreferred, 0
	if _, ok := windowsUnicastPrefix(&u); ok {
		t.Fatal("expired address accepted")
	}
	u.ValidLifetime, u.OnLinkPrefixLength = 100, 33
	if _, ok := windowsUnicastPrefix(&u); ok {
		t.Fatal("invalid IPv4 prefix accepted")
	}
}

func TestWindowsNativeScan(t *testing.T) {
	var ip windows.MibIpInterfaceRow
	var route windows.MibIpForwardRow2
	t.Logf("x/sys MibIpInterfaceRow size=%d metric=%d mtu=%d offload=%d; MibIpForwardRow2 size=%d", unsafe.Sizeof(ip), unsafe.Offsetof(ip.Metric), unsafe.Offsetof(ip.NlMtu), unsafe.Offsetof(ip.TransmitOffload), unsafe.Sizeof(route))
	s, err := (windowsScanner{}).Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range s.Links {
		if l.NativeID == 0 {
			t.Fatal("native LUID absent")
		}
	}
	t.Logf("native routes=%d links=%d", len(s.Routes), len(s.Links))
}
