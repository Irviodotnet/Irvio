// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"errors"
	"fmt"
	"net/netip"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winthread"
	"golang.org/x/sys/windows"
)

var routeIPHelper = windows.NewLazySystemDLL("iphlpapi.dll")
var routeNativeSlot = make(chan struct{}, 1)

// One dedicated native thread owns a complete route operation, including journal and rollback.
func routeOperation(back routeBackend, fn func() error) error {
	if _, native := back.(nativeRouteBackend); !native {
		return fn()
	}
	routeNativeSlot <- struct{}{}
	defer func() { <-routeNativeSlot }()
	_, err := winthread.Call(func() (struct{}, error) { return struct{}{}, fn() })
	return err
}

const ownedVirtualInterfaceType = 53 // IF_TYPE_PROP_VIRTUAL, used by Wintun.

//go:uintptrescapes
func routeCall(name string, args ...uintptr) error {
	p := routeIPHelper.NewProc(name)
	if err := p.Find(); err != nil {
		return err
	}
	r, _, _ := p.Call(args...)
	if r != 0 && name != "InitializeIpForwardEntry" {
		return fmt.Errorf("%s: %w", name, windows.Errno(r))
	}
	return nil
}

func routeGUID(luid uint64) (windows.GUID, error) {
	var id windows.GUID
	if luid == 0 {
		return id, fmt.Errorf("tunnel route LUID is required")
	}
	err := routeCall("ConvertInterfaceLuidToGuid", uintptr(unsafe.Pointer(&luid)), uintptr(unsafe.Pointer(&id)))
	return id, err
}

type nativeRouteBackend struct{}

func (nativeRouteBackend) Ready(luid uint64) error {
	return routeReady(luid)
}

func routeReady(luid uint64) error {
	row := windows.MibIfRow2{InterfaceLuid: luid}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		return err
	}
	if row.InterfaceLuid != luid || row.Type != ownedVirtualInterfaceType || row.OperStatus != windows.IfOperStatusUp {
		return fmt.Errorf("owned route interface is not ready")
	}
	return nil
}

func (nativeRouteBackend) Resolve(id windows.GUID) (uint64, error) {
	return routeResolve(id)
}

func routeResolve(id windows.GUID) (uint64, error) {
	if id == (windows.GUID{}) {
		return 0, fmt.Errorf("tunnel route GUID is required")
	}
	var luid uint64
	if err := routeCall("ConvertInterfaceGuidToLuid", uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&luid))); err != nil {
		if errors.Is(err, windows.ERROR_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return 0, ErrRouteInterfaceGone
		}
		return 0, err
	}
	row := windows.MibIfRow2{InterfaceLuid: luid}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		return 0, err
	}
	if row.Type != ownedVirtualInterfaceType {
		return 0, fmt.Errorf("tunnel routes require an explicitly owned virtual interface")
	}
	return luid, nil
}

func ownedRouteRow(luid uint64, p netip.Prefix) (windows.MibIpForwardRow2, error) {
	var r windows.MibIpForwardRow2
	if luid == 0 || !p.IsValid() || p != p.Masked() || p.Addr().Is4In6() || p.Addr().Zone() != "" {
		return r, fmt.Errorf("invalid tunnel route")
	}
	if err := routeCall("InitializeIpForwardEntry", uintptr(unsafe.Pointer(&r))); err != nil {
		return r, err
	}
	r.InterfaceLuid = luid
	r.DestinationPrefix.Prefix = nativeAddress(p.Addr())
	r.DestinationPrefix.PrefixLength = uint8(p.Bits())
	zero := netip.IPv6Unspecified()
	if p.Addr().Is4() {
		zero = netip.IPv4Unspecified()
	}
	r.NextHop = nativeAddress(zero)
	r.SitePrefixLength, r.Metric, r.Protocol = 0, 3, windows.MIB_IPPROTO_NETMGMT
	r.Loopback, r.AutoconfigureAddress, r.Publish, r.Immortal = 0, 0, 0, 1
	return r, nil
}

func (nativeRouteBackend) Lookup(luid uint64, p netip.Prefix) (bool, bool, error) {
	return routeLookup(luid, p)
}

func routeLookup(luid uint64, p netip.Prefix) (bool, bool, error) {
	want, err := ownedRouteRow(luid, p)
	if err != nil {
		return false, false, err
	}
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(addressFamily(p.Addr()), &table); err != nil {
		return false, false, err
	}
	if table == nil {
		return false, false, fmt.Errorf("route table missing")
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	if table.NumEntries > 131072 {
		return false, false, fmt.Errorf("route table exceeds bounded inventory")
	}
	count, exact := 0, true
	for _, r := range table.Rows() {
		if r.InterfaceLuid != luid || addressFromNative(&r.DestinationPrefix.Prefix) != p.Addr() || r.DestinationPrefix.PrefixLength != uint8(p.Bits()) {
			continue
		}
		count++
		exact = exact && addressFromNative(&r.NextHop) == addressFromNative(&want.NextHop) && r.SitePrefixLength == want.SitePrefixLength && r.ValidLifetime == want.ValidLifetime && r.PreferredLifetime == want.PreferredLifetime && r.Metric == want.Metric && r.Protocol == want.Protocol && r.Loopback == 0 && r.AutoconfigureAddress == 0 && r.Publish == 0 && r.Immortal == 1 && r.Origin == 0
	}
	return count != 0, count == 1 && exact, nil
}

func (nativeRouteBackend) Add(luid uint64, p netip.Prefix) error {
	r, err := ownedRouteRow(luid, p)
	if err != nil {
		return err
	}
	return routeCall("CreateIpForwardEntry2", uintptr(unsafe.Pointer(&r)))
}

func (nativeRouteBackend) Delete(luid uint64, p netip.Prefix) error {
	return routeDelete(luid, p)
}

func routeDelete(luid uint64, p netip.Prefix) error {
	r, err := ownedRouteRow(luid, p)
	if err != nil {
		return err
	}
	err = routeCall("DeleteIpForwardEntry2", uintptr(unsafe.Pointer(&r)))
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return nil
	}
	return err
}
