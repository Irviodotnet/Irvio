// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestParseTunnelDNSServers(t *testing.T) {
	got, err := parseTunnelDNSServers([]string{"1.1.1.1", "2606:4700:4700::1111", "1.1.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2606:4700:4700::1111")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("servers = %#v, want %#v", got, want)
	}
}

func TestParseTunnelDNSServersRejectsNonLiteralAndUnsafeAddresses(t *testing.T) {
	for _, value := range []string{"resolver.example", "127.0.0.1", "::", "fe80::1", "224.0.0.1", "::ffff:1.1.1.1"} {
		t.Run(value, func(t *testing.T) {
			if _, err := parseTunnelDNSServers([]string{value}); err == nil {
				t.Fatal("expected invalid DNS server error")
			}
		})
	}
}
