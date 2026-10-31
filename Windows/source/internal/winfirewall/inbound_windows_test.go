// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func TestPhysicalInboundScope(t *testing.T) {
	for _, s := range []string{"0.0.0.0:4000", "127.0.0.1:4000", "[::ffff:1.1.1.1]:4000", "[fe80::1%3]:4000", "1.1.1.1:0"} {
		if validInboundEndpoint(netip.MustParseAddrPort(s), 17) {
			t.Fatal("unsafe endpoint", s)
		}
	}
	for _, proto := range []uint32{0, 1, 58, 256} {
		if validInboundEndpoint(netip.MustParseAddrPort("192.168.23.2:4000"), proto) {
			t.Fatal("unsafe protocol", proto)
		}
	}
	for _, s := range []string{"192.168.23.2:4000", "1.1.1.1:3478", "[2001:db8::1]:51820"} {
		for _, proto := range []uint32{6, 17} {
			if !validInboundEndpoint(netip.MustParseAddrPort(s), proto) {
				t.Fatal("valid endpoint refused", s)
			}
		}
	}
}

func TestPhysicalInboundCOMAndJournal(t *testing.T) {
	b, err := winroute.ForSource(netip.MustParseAddr("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	row := windows.MibIfRow2{InterfaceLuid: b.LUID}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []uint32{6, 17} {
		scope := controlScope{Owner: windows.GUID{Data1: 123}, Tunnel: row.InterfaceGuid, Application: `C:\IRVIO\irviod.exe`, Interface: windows.UTF16ToString(row.Alias[:]), Endpoint: netip.MustParseAddrPort("192.168.23.2:49507"), Physical: true, Protocol: protocol}
		if err := validateControlRecord(controlRecord{Version: 1, Rule: &scope}); err != nil {
			t.Fatal(err)
		}
		err := withRules(func(_ *object) error {
			rule, err := create(&classRule, &iidRule)
			if err != nil {
				return err
			}
			defer rule.release()
			c := scope.control()
			if err := c.configure(rule); err != nil {
				return err
			}
			if err := c.verify(rule); err != nil {
				return err
			}
			if err := rule.putString(18, "49508"); err != nil {
				return err
			}
			if !errors.Is(c.verify(rule), ErrConflict) {
				t.Fatal("changed physical port accepted")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		scope.Pool = netip.MustParsePrefix("192.168.23.0/24")
		if validateControlRecord(controlRecord{Version: 1, Rule: &scope}) == nil {
			t.Fatal("mixed tunnel/physical scope accepted")
		}
	}
}
