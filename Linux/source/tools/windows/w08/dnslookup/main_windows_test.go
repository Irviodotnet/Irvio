// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestAddressRecords(t *testing.T) {
	a := &windows.DNSRecord{Type: windows.DNS_TYPE_A, Length: 4, Dw: 1}
	copy(a.Data[:], []byte{203, 0, 113, 25})
	cname := &windows.DNSRecord{Type: windows.DNS_TYPE_CNAME, Next: a, Dw: 1}
	got, err := addresses(cname)
	if err != nil || len(got) != 1 || got[0] != "203.0.113.25" {
		t.Fatal(got, err)
	}
	a.Length = 3
	if _, err := addresses(a); err == nil {
		t.Fatal("short A accepted")
	}
	a.Length, a.Dw = 4, 3
	if _, err := addresses(a); err == nil {
		t.Fatal("additional-only accepted")
	}
	a.Dw, a.Next = 1, cname
	if _, err := addresses(a); err == nil {
		t.Fatal("cyclic records accepted")
	}
	if queryOptions&0x8 == 0 {
		t.Fatal("cache bypass absent")
	}
}
