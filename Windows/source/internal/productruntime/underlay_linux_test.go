// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package productruntime

import (
	"net"
	"net/netip"
	"os"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestUnderlayPathUsesKernelSelectedRoute(t *testing.T) {
	target := netip.MustParseAddr("203.0.113.10")
	path, err := underlayPathFromRoute(target, 0x48594452, netlink.Route{
		Type: unix.RTN_UNICAST, LinkIndex: 9, Src: net.ParseIP("192.0.2.9"), Gw: net.ParseIP("192.0.2.1"),
	}, &netlink.LinkAttrs{Index: 9, Name: "enp5s0", Flags: net.FlagUp})
	if err != nil {
		t.Fatal(err)
	}
	if path.IfName != "enp5s0" || path.IfIndex != 9 || path.Source.String() != "192.0.2.9" ||
		path.Gateway.String() != "192.0.2.1" || path.RouteMark != 0x48594452 {
		t.Fatalf("unexpected path: %+v", path)
	}
}

func TestUnderlayPathRejectsLocalRoutesAndLoopbackLinks(t *testing.T) {
	// A public preferred source cannot make RTN_LOCAL or loopback suitable for WAN.
	target := netip.MustParseAddr("203.0.113.10")
	for _, test := range []struct {
		name  string
		route netlink.Route
		link  netlink.LinkAttrs
	}{
		{"own public IP", netlink.Route{Type: unix.RTN_LOCAL, LinkIndex: 1, Src: net.ParseIP(target.String())}, netlink.LinkAttrs{Name: "lo", Flags: net.FlagUp | net.FlagLoopback}},
		{"loopback flag", netlink.Route{Type: unix.RTN_UNICAST, LinkIndex: 1, Src: net.ParseIP("192.0.2.9")}, netlink.LinkAttrs{Name: "renamed-loopback", Flags: net.FlagUp | net.FlagLoopback}},
		{"loopback name", netlink.Route{Type: unix.RTN_UNICAST, LinkIndex: 1, Src: net.ParseIP("192.0.2.9")}, netlink.LinkAttrs{Name: "lo", Flags: net.FlagUp}},
		{"local route on physical link", netlink.Route{Type: unix.RTN_LOCAL, LinkIndex: 9, Src: net.ParseIP(target.String())}, netlink.LinkAttrs{Name: "eth0", Flags: net.FlagUp}},
		{"broadcast route", netlink.Route{Type: unix.RTN_BROADCAST, LinkIndex: 9, Src: net.ParseIP("192.0.2.9")}, netlink.LinkAttrs{Name: "eth0", Flags: net.FlagUp}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if path, err := underlayPathFromRoute(target, 0, test.route, &test.link); err == nil {
				t.Fatalf("accepted a non-WAN route: %+v", path)
			}
		})
	}
}

func TestResolveBootstrapUnderlaySkipsOwnAddressRealKernel(t *testing.T) {
	selfText := os.Getenv("IRVIO_TEST_UNDERLAY_SELF_IPV4")
	if selfText == "" {
		t.Skip("set IRVIO_TEST_UNDERLAY_SELF_IPV4 and IRVIO_TEST_UNDERLAY_REMOTE_IPV4 for read-only kernel route validation")
	}
	self, err := netip.ParseAddr(selfText)
	if err != nil || !self.Is4() {
		t.Fatalf("invalid self IPv4: %q", selfText)
	}
	remote, err := netip.ParseAddr(os.Getenv("IRVIO_TEST_UNDERLAY_REMOTE_IPV4"))
	if err != nil || !remote.Is4() || self == remote {
		t.Fatal("a distinct remote IPv4 is required")
	}
	if path, err := ResolveUnderlay(self, 0); err == nil {
		t.Fatalf("accepted own address as WAN: %+v", path)
	}
	expected, err := ResolveUnderlay(remote, 0)
	if err != nil {
		t.Fatal(err)
	}
	path, err := ResolveBootstrapUnderlay([]netip.AddrPort{
		netip.AddrPortFrom(self, 3478), netip.AddrPortFrom(remote, 3478),
	}, 0)
	if err != nil || path != expected {
		t.Fatalf("did not fall back to remote WAN: path=%+v expected=%+v err=%v", path, expected, err)
	}
	if _, err := ResolveBootstrapUnderlay([]netip.AddrPort{netip.AddrPortFrom(self, 3478)}, 0); err == nil {
		t.Fatal("self-only bootstrap pool must fail, not select loopback")
	}
	t.Logf("self rejected; selected remote %s via %s (source %s)", remote, path.IfName, path.Source)
}

func TestUnderlayPathRejectsUnusableOrAmbiguousFacts(t *testing.T) {
	target := netip.MustParseAddr("203.0.113.10")
	valid := netlink.Route{Type: unix.RTN_UNICAST, LinkIndex: 9, Src: net.ParseIP("192.0.2.9")}
	for _, test := range []struct {
		name  string
		route netlink.Route
		link  *netlink.LinkAttrs
	}{
		{"missing source", netlink.Route{Type: unix.RTN_UNICAST, LinkIndex: 9}, &netlink.LinkAttrs{Name: "eth0", Flags: net.FlagUp}},
		{"wrong source family", netlink.Route{Type: unix.RTN_UNICAST, LinkIndex: 9, Src: net.ParseIP("2001:db8::9")}, &netlink.LinkAttrs{Name: "eth0", Flags: net.FlagUp}},
		{"tunnel output", valid, &netlink.LinkAttrs{Name: "hydra0", Flags: net.FlagUp}},
		{"down output", valid, &netlink.LinkAttrs{Name: "eth0"}},
		{"missing link", valid, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := underlayPathFromRoute(target, 0, test.route, test.link); err == nil {
				t.Fatal("expected explicit resolution error")
			}
		})
	}
	if usableTarget(netip.MustParseAddr("127.0.0.1")) || usableTarget(netip.MustParseAddr("ff02::1")) {
		t.Fatal("local and multicast addresses must not be underlay targets")
	}
}

func TestUnderlayPathAcceptsOnLinkIPv4AndIPv6(t *testing.T) {
	for _, test := range []struct{ target, source string }{
		{"203.0.113.10", "192.0.2.9"},
		{"2001:db8::10", "2001:db8::9"},
	} {
		t.Run(test.target, func(t *testing.T) {
			path, err := underlayPathFromRoute(netip.MustParseAddr(test.target), 0, netlink.Route{
				Type: unix.RTN_UNICAST, LinkIndex: 9, Src: net.ParseIP(test.source),
			}, &netlink.LinkAttrs{Name: "renamed-wan", Flags: net.FlagUp})
			if err != nil || path.Source.String() != test.source || path.Gateway.IsValid() {
				t.Fatalf("valid on-link route rejected: path=%+v err=%v", path, err)
			}
		})
	}
}
