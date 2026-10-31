// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import (
	"net/netip"
	"os"
	"runtime"
	"slices"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestUserConditionRoundTrip(t *testing.T) {
	for _, user := range []UserMatch{SystemOrAdministrator, DHCPService} {
		r := Rule{Key: windows.GUID{Data1: 50}, Layer: ConnectV4, Action: Permit, User: user}
		n, err := makeNative(r, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		got, err := readRule(&n.Filter)
		runtime.KeepAlive(n)
		if err != nil || got != r {
			t.Fatalf("user condition %+v %v", got, err)
		}
		n.UserBlob = bytesBlob([]byte("not a security descriptor"))
		if _, err := readRule(&n.Filter); err == nil {
			t.Fatal("unknown descriptor accepted")
		}
		runtime.KeepAlive(n)
		r.Layer = TransportV4
		if r.Validate() == nil {
			t.Fatal("user restriction silently ignored at transport")
		}
	}
	if (Rule{Key: windows.GUID{Data1: 50}, Layer: ConnectV4, Action: Permit, User: 99}).Validate() == nil {
		t.Fatal("unknown user mode accepted")
	}
}

func TestLoopbackAndICMPRoundTrip(t *testing.T) {
	for _, r := range []Rule{
		{Key: windows.GUID{Data1: 21}, Layer: ConnectV4, Action: Permit, LoopbackOnly: true},
		{Key: windows.GUID{Data1: 22}, Layer: TransportV6, Action: Permit, Protocol: 58, ICMP: true, ICMPType: 135, ICMPCode: 0},
		{Key: windows.GUID{Data1: 23}, Layer: ConnectV4, Action: Block, Protocol: 1, ICMP: true, ICMPType: 0, ICMPCode: 0},
	} {
		n, err := makeNative(r, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		slices.Reverse(n.Conditions)
		got, err := readRule(&n.Filter)
		runtime.KeepAlive(n)
		if err != nil || got != r {
			t.Fatalf("loopback/ICMP roundtrip %+v %v", got, err)
		}
	}
	for _, r := range []Rule{
		{Key: windows.GUID{Data1: 24}, Layer: ConnectV4, Action: Permit, Protocol: 58, ICMP: true, ICMPType: 135},
		{Key: windows.GUID{Data1: 25}, Layer: ConnectV6, Action: Permit, Protocol: 58, ICMPType: 135},
		{Key: windows.GUID{Data1: 26}, Layer: ConnectV6, Action: Permit, Protocol: 58, ICMP: true, LocalPort: 135},
	} {
		if err := r.Validate(); err == nil {
			t.Fatal("invalid ICMP condition accepted")
		}
	}
}

func testConfig() Config {
	return Config{Provider: windows.GUID{Data1: 1}, Sublayer: windows.GUID{Data1: 2}, Name: "IRVIO WFP test", Marker: "irvio-wfp-test-v1", ServiceName: "IRVIO-W05-Owner", Weight: 49000}
}

func TestScopedRuleRoundTrip(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	app, err := ApplicationIDForPath(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range []windows.GUID{ConnectV4, ConnectV6, TransportV4, TransportV6, AcceptV4, AcceptV6} {
		r := Rule{Key: windows.GUID{Data1: 9}, Layer: layer, Action: Permit, LocalInterface: 0xabcdef0123456789, Protocol: 17, RemotePort: 3478}
		r.LocalPort = 51820
		r.LocalAddress = netip.MustParseAddr("192.0.2.1")
		if layer == ConnectV6 || layer == TransportV6 || layer == AcceptV6 {
			r.LocalAddress = netip.MustParseAddr("2001:db8::1")
		}
		if layer == ConnectV4 || layer == ConnectV6 || layer == AcceptV4 || layer == AcceptV6 {
			r.Application = app
			r.User = SystemOrAdministrator
		}
		n, err := makeNative(r, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		got, err := readRule(&n.Filter)
		runtime.KeepAlive(n)
		if err != nil || got != r {
			t.Fatalf("scope changed: %+v %v", got, err)
		}
	}
}

func TestRejectInvalidScopes(t *testing.T) {
	for _, id := range []ApplicationID{"", "odd", "a\x00b\x00", "a\x00\x00\x00b\x00\x00\x00"} {
		if id.validate() == nil {
			t.Fatal("malformed application ID accepted")
		}
	}
	if _, err := ApplicationIDForPath("relative.exe"); err == nil {
		t.Fatal("relative application path accepted")
	}
	r := Rule{Key: windows.GUID{Data1: 7}, Layer: TransportV4, Action: Permit, Application: "a\x00\x00\x00"}
	if _, err := makeNative(r, testConfig()); err == nil {
		t.Fatal("application restriction silently dropped at transport layer")
	}
	r.Layer = ConnectV4
	r.LocalInterface = 1
	n, err := makeNative(r, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	n.Interface = 0
	if _, err := readRule(&n.Filter); err == nil {
		t.Fatal("explicit zero LUID interpreted as absent condition")
	}
	n.Interface = 1
	n.AppBlob.Size = 0
	if _, err := readRule(&n.Filter); err == nil {
		t.Fatal("empty native application interpreted as wildcard")
	}
}

func TestSDKLayout(t *testing.T) {
	var f filter
	var c condition
	var e enumTemplate
	var s session
	var p provider
	var sl sublayer
	var l layer
	for name, pair := range map[string][2]uintptr{
		"nativeLayer": {unsafe.Sizeof(l), 72}, "layerFields": {unsafe.Offsetof(l.Fields), 40}, "layerDefaultSublayer": {unsafe.Offsetof(l.DefaultSublayer), 48}, "layerID": {unsafe.Offsetof(l.ID), 64},
		"provider": {unsafe.Sizeof(p), 64}, "providerData": {unsafe.Offsetof(p.Data), 40}, "providerService": {unsafe.Offsetof(p.Service), 56},
		"sublayer": {unsafe.Sizeof(sl), 72}, "sublayerProvider": {unsafe.Offsetof(sl.Provider), 40}, "sublayerWeight": {unsafe.Offsetof(sl.Weight), 64},
		"session": {unsafe.Sizeof(s), 72}, "sessionFlags": {unsafe.Offsetof(s.Flags), 32}, "sessionTimeout": {unsafe.Offsetof(s.WaitMS), 36},
		"filter": {unsafe.Sizeof(f), 200}, "providerPointer": {unsafe.Offsetof(f.Provider), 40}, "layer": {unsafe.Offsetof(f.Layer), 64},
		"weight": {unsafe.Offsetof(f.Weight), 96}, "conditions": {unsafe.Offsetof(f.Conditions), 120}, "action": {unsafe.Offsetof(f.Action), 128},
		"context": {unsafe.Offsetof(f.Context), 152}, "filterID": {unsafe.Offsetof(f.ID), 176}, "effectiveWeight": {unsafe.Offsetof(f.EffectiveWeight), 184},
		"condition": {unsafe.Sizeof(c), 40}, "conditionValue": {unsafe.Offsetof(c.Value), 24}, "value": {unsafe.Sizeof(value{}), 16}, "blob": {unsafe.Sizeof(blob{}), 16},
		"enumTemplate": {unsafe.Sizeof(e), 72}, "enumLayer": {unsafe.Offsetof(e.Layer), 8}, "enumConditions": {unsafe.Offsetof(e.Conditions), 48}, "enumCallout": {unsafe.Offsetof(e.Callout), 64},
		"v4Mask": {unsafe.Sizeof(v4Mask{}), 8}, "v6Mask": {unsafe.Sizeof(v6Mask{}), 17},
	} {
		if pair[0] != pair[1] {
			t.Fatalf("%s: got %d expected %d", name, pair[0], pair[1])
		}
	}
}

func TestRuleNativeRoundTrip(t *testing.T) {
	for _, p := range []string{"0.0.0.0/0", "192.0.2.0/24", "192.0.2.3/32", "::/0", "2001:db8::/64", "2001:db8::1/128"} {
		r := Rule{Key: windows.GUID{Data1: 3}, Layer: ConnectV4, Action: Block, Priority: 0x10000000001, Remote: netip.MustParsePrefix(p), Protocol: 6, RemotePort: 49494}
		if !r.Remote.Addr().Is4() {
			r.Layer = ConnectV6
		}
		n, err := makeNative(r, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		got, err := readRule(&n.Filter)
		runtime.KeepAlive(n)
		if err != nil || got != r {
			t.Fatalf("%s: got %+v error %v", p, got, err)
		}
	}
}

func TestRejectInvalidRules(t *testing.T) {
	base := Rule{Key: windows.GUID{Data1: 3}, Layer: ConnectV4, Action: Block, Remote: netip.MustParsePrefix("192.0.2.0/24"), Protocol: 6, RemotePort: 80}
	for _, change := range []func(*Rule){
		func(r *Rule) { r.Key = windows.GUID{} }, func(r *Rule) { r.Layer = windows.GUID{} }, func(r *Rule) { r.Action = 0 },
		func(r *Rule) { r.Remote = netip.MustParsePrefix("192.0.2.1/24") }, func(r *Rule) { r.Remote = netip.MustParsePrefix("::/0") },
		func(r *Rule) { r.Protocol = 0 }, func(r *Rule) { r.Protocol = 1 },
		func(r *Rule) { r.LocalAddress = netip.MustParseAddr("::1") },
		func(r *Rule) { r.LocalAddress = netip.MustParseAddr("::ffff:192.0.2.1") },
		func(r *Rule) {
			r.Layer = ConnectV6
			r.Remote = netip.Prefix{}
			r.LocalAddress = netip.MustParseAddr("fe80::1%3")
		},
	} {
		r := base
		change(&r)
		if _, err := makeNative(r, testConfig()); err == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	n, err := makeNative(base, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	n.Conditions[0].Match = 1
	if _, err = readRule(&n.Filter); err == nil {
		t.Fatal("accepted unexpected match operator")
	}
	n.Conditions[0].Match = 0
	n.Filter.Flags |= 4
	if _, err = readRule(&n.Filter); err == nil {
		t.Fatal("accepted unexpected filter flags")
	}
}

func TestUnusedNativeUnionPaddingIsNotPolicy(t *testing.T) {
	r := Rule{Key: windows.GUID{Data1: 3}, Layer: ConnectV4, Action: Block, Priority: 1}
	n, err := makeNative(r, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	n.Filter.Context[1] = ^uint64(0)
	n.Filter.Action.Key = windows.GUID{Data1: 0xdeadbeef}
	n.Filter.Flags |= 0x40
	got, err := readRule(&n.Filter)
	runtime.KeepAlive(n)
	if err != nil || got != r {
		t.Fatalf("unused union storage interpreted as active policy: %+v %v", got, err)
	}
	n.Filter.Context[0] = 1
	if _, err = readRule(&n.Filter); err == nil {
		t.Fatal("unexpected active rawContext accepted")
	}
}
