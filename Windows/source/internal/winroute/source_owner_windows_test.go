// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"errors"
	"net/netip"
	"os"
	"testing"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"golang.org/x/sys/windows"
)

func TestUnicastTableLayout(t *testing.T) {
	var table windows.MibUnicastIpAddressTable
	if unsafe.Offsetof(table.Table) != 8 || unsafe.Sizeof(table.Table[0]) != 80 {
		t.Fatal("unicast table differs from x64 Windows SDK layout")
	}
}

func TestNativeSourceOwnerLifecycle(t *testing.T) {
	if os.Getenv("IRVIO_SOURCE_OWNER_NATIVE") != "1" {
		t.Skip("requires disposable elevated Windows VM and signed Wintun")
	}
	prefix := netip.MustParsePrefix("10.253.243.1/24")
	if _, err := ForSource(prefix.Addr()); err == nil {
		t.Fatal("fixture address is already in use")
	}
	create := func(name string) *tunnel.Windows {
		t.Helper()
		owner, err := tunnel.NewWindows(tunnel.Config{Name: name, Addresses: []netip.Prefix{prefix}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := owner.Close(); err != nil {
				t.Error(err)
			}
		})
		return owner
	}
	one := create("IRVIO-SrcA")
	want := Binding{LUID: one.LUID(), Index: uint32(one.IfIndex()), Source: prefix.Addr()}
	if got, err := ForSource(prefix.Addr()); err != nil || got != want {
		t.Fatalf("new owner=%+v err=%v", got, err)
	}
	two, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-SrcB", Addresses: []netip.Prefix{prefix}})
	if err != nil {
		if !errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
			t.Fatal(err)
		}
		t.Log("kernel rejects duplicate source provisioning; duplicate-table rejection is covered separately")
	} else {
		t.Cleanup(func() {
			if err := two.Close(); err != nil {
				t.Error(err)
			}
		})
		if _, err := ForSource(prefix.Addr()); err == nil {
			t.Fatal("native duplicate owners accepted")
		}
		if err := two.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := ForSource(prefix.Addr()); err != nil || got != want {
		t.Fatalf("remaining owner=%+v err=%v", got, err)
	}
	if err := one.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ForSource(prefix.Addr()); err == nil {
		t.Fatal("removed address owner accepted")
	}
	if _, err := want.validate(); err == nil {
		t.Fatal("stale binding accepted after adapter removal")
	}
	t.Log("new source, remaining owner and removed adapter fencing PASS")
}

func TestSourceOwnerRejectsAmbiguityAndStaleRows(t *testing.T) {
	for _, value := range []string{"192.0.2.1", "2001:db8::1"} {
		t.Run(value, func(t *testing.T) {
			source := netip.MustParseAddr(value)
			row := windows.MibUnicastIpAddressRow{InterfaceLuid: 123, InterfaceIndex: 7,
				DadState: windows.IpDadStatePreferred, ValidLifetime: 60}
			*(*windows.RawSockaddrInet)(unsafe.Pointer(&row.Address)) = nativeAddress(source)
			want := Binding{LUID: 123, Index: 7, Source: source}
			if got, err := sourceOwner(source, []windows.MibUnicastIpAddressRow{row}); err != nil || got != want {
				t.Fatalf("owner=%+v err=%v", got, err)
			}
			if _, err := sourceOwner(source, nil); err == nil {
				t.Fatal("missing owner accepted")
			}
			if _, err := sourceOwner(netip.MustParseAddr("192.0.2.2"), []windows.MibUnicastIpAddressRow{row}); err == nil {
				t.Fatal("unrelated source accepted")
			}
			for _, mutate := range []func(*windows.MibUnicastIpAddressRow){
				func(r *windows.MibUnicastIpAddressRow) { r.InterfaceLuid++ },
				func(r *windows.MibUnicastIpAddressRow) { r.InterfaceIndex++ },
				func(r *windows.MibUnicastIpAddressRow) { r.InterfaceLuid = 0 },
				func(r *windows.MibUnicastIpAddressRow) { r.InterfaceIndex = 0 },
				func(r *windows.MibUnicastIpAddressRow) { r.DadState = windows.IpDadStateTentative },
				func(r *windows.MibUnicastIpAddressRow) { r.ValidLifetime = 0 },
			} {
				other := row
				mutate(&other)
				for _, rows := range [][]windows.MibUnicastIpAddressRow{{row, other}, {other, row}} {
					if _, err := sourceOwner(source, rows); err == nil {
						t.Fatalf("ambiguous/stale rows accepted: %+v", rows)
					}
				}
			}
		})
	}
}
