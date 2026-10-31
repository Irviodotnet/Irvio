// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package networkstate

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errNetworkTableChanged = errors.New("Windows route and adapter tables changed during scan")

type windowsAdapter struct {
	link             Link
	index4, index6   uint32
	metric4, metric6 uint32
	mtu4, mtu6       uint32
}

type windowsScanner struct{}

func newPlatformScanner() scanner { return windowsScanner{} }

func (windowsScanner) Scan(ctx context.Context) (Snapshot, error) {
	return scanWindowsTables(ctx, readWindowsAdapters, readWindowsRoutes)
}

func scanWindowsTables(ctx context.Context, adapters func() ([]windowsAdapter, error), routes func() ([]windows.MibIpForwardRow2, error)) (Snapshot, error) {
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		a, err := adapters()
		if err != nil {
			return Snapshot{}, fmt.Errorf("read Windows adapters: %w", err)
		}
		r, err := routes()
		if err != nil {
			return Snapshot{}, fmt.Errorf("read Windows routes: %w", err)
		}
		snapshot, err := windowsSnapshot(a, r)
		if !errors.Is(err, errNetworkTableChanged) {
			return snapshot, err
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return Snapshot{}, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	return Snapshot{}, errNetworkTableChanged
}

func readWindowsAdapters() ([]windowsAdapter, error) {
	size := uint32(15 * 1024)
	for attempt := 0; attempt < 3; attempt++ {
		if size < uint32(unsafe.Sizeof(windows.IpAdapterAddresses{})) || size > 16*1024*1024 {
			return nil, fmt.Errorf("invalid adapter buffer size %d", size)
		}
		buffer := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		flags := uint32(windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST | windows.GAA_FLAG_SKIP_DNS_SERVER | windows.GAA_FLAG_INCLUDE_ALL_INTERFACES)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, first, &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if errors.Is(err, windows.ERROR_NO_DATA) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var adapters []windowsAdapter
		for a := first; a != nil; a = a.Next {
			adapter := windowsAdapter{
				link: Link{NativeID: a.Luid, Name: windows.UTF16PtrToString(a.FriendlyName),
					Up: a.OperStatus == windows.IfOperStatusUp, MTU: int(a.Mtu)},
				index4: a.IfIndex, index6: a.Ipv6IfIndex, metric4: a.Ipv4Metric, metric6: a.Ipv6Metric,
			}
			for u := a.FirstUnicastAddress; u != nil; u = u.Next {
				if prefix, ok := windowsUnicastPrefix(u); ok {
					adapter.link.Addresses = append(adapter.link.Addresses, prefix)
				}
			}
			adapters = append(adapters, adapter)
		}
		runtime.KeepAlive(buffer)
		if err := readWindowsIPInterfaces(adapters); err != nil {
			return nil, err
		}
		return adapters, nil
	}
	return nil, fmt.Errorf("adapter buffer kept changing: %w", windows.ERROR_BUFFER_OVERFLOW)
}

func readWindowsIPInterfaces(adapters []windowsAdapter) error {
	// Single-row queries avoid the pinned x/sys table stride mismatch with the Windows SDK.
	for i := range adapters {
		a := &adapters[i]
		if a.link.NativeID == 0 {
			continue
		}
		for _, family := range []uint16{windows.AF_INET, windows.AF_INET6} {
			row := windows.MibIpInterfaceRow{Family: family, InterfaceLuid: a.link.NativeID}
			if err := windows.GetIpInterfaceEntry(&row); err != nil {
				if errors.Is(err, windows.ERROR_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
					continue
				}
				return fmt.Errorf("query Windows IP interface: %w", err)
			}
			switch family {
			case windows.AF_INET:
				if row.InterfaceIndex == a.index4 {
					a.mtu4, a.metric4 = row.NlMtu, row.Metric
				}
			case windows.AF_INET6:
				if row.InterfaceIndex == a.index6 {
					a.mtu6, a.metric6 = row.NlMtu, row.Metric
				}
			}
		}
	}
	return nil
}

func windowsUnicastPrefix(u *windows.IpAdapterUnicastAddress) (netip.Prefix, bool) {
	if u.Address.Sockaddr == nil || u.ValidLifetime == 0 || u.DadState != windows.IpDadStatePreferred {
		return netip.Prefix{}, false
	}
	addr, ok := netip.AddrFromSlice(u.Address.IP())
	if !ok {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	prefix := netip.PrefixFrom(addr, int(u.OnLinkPrefixLength))
	return prefix, prefix.IsValid()
}

func readWindowsRoutes() ([]windows.MibIpForwardRow2, error) {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err != nil {
		return nil, err
	}
	if table == nil {
		return nil, fmt.Errorf("Windows returned a nil route table")
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	return append([]windows.MibIpForwardRow2(nil), table.Rows()...), nil
}

func windowsSnapshot(adapters []windowsAdapter, rows []windows.MibIpForwardRow2) (Snapshot, error) {
	byID := make(map[uint64]windowsAdapter, len(adapters))
	for _, a := range adapters {
		if a.link.NativeID != 0 {
			byID[a.link.NativeID] = a
		}
	}
	var routes []Route
	var links []Link
	linkMTUs := make(map[int]int)
	for _, row := range rows {
		if row.DestinationPrefix.PrefixLength != 0 || row.Loopback != 0 || row.ValidLifetime == 0 {
			continue
		}
		dest := windowsInetAddress(&row.DestinationPrefix.Prefix)
		if !dest.IsValid() || !dest.IsUnspecified() {
			continue
		}
		a, ok := byID[row.InterfaceLuid]
		if !ok || row.InterfaceIndex == 0 {
			return Snapshot{}, errNetworkTableChanged
		}
		index, metric, mtu := a.index6, a.metric6, a.mtu6
		if dest.Is4() {
			index, metric, mtu = a.index4, a.metric4, a.mtu4
		}
		if index != row.InterfaceIndex || mtu == 0 {
			return Snapshot{}, errNetworkTableChanged
		}
		priority := uint64(row.Metric) + uint64(metric)
		if priority > uint64(^uint(0)>>1) {
			return Snapshot{}, fmt.Errorf("Windows route metric overflows native int")
		}
		gateway := windowsInetAddress(&row.NextHop)
		if !gateway.IsValid() || familyForAddr(gateway) != familyForAddr(dest) {
			return Snapshot{}, fmt.Errorf("invalid Windows next-hop family for interface %d", index)
		}
		if gateway.IsUnspecified() {
			gateway = netip.Addr{}
		}
		a.link.Index = int(index)
		if current := linkMTUs[a.link.Index]; current == 0 || int(mtu) < current {
			linkMTUs[a.link.Index] = int(mtu)
		}
		links = append(links, a.link)
		routes = append(routes, Route{Family: familyForAddr(dest), Table: mainRouteTable,
			Priority: int(priority), LinkIndex: int(index), MTU: int(mtu), Gateway: gateway})
	}
	for i := range links {
		links[i].MTU = linkMTUs[links[i].Index]
	}
	return Normalize(routes, links)
}

func windowsInetAddress(raw *windows.RawSockaddrInet) netip.Addr {
	switch raw.Family {
	case windows.AF_INET:
		return netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(raw)).Addr)
	case windows.AF_INET6:
		return netip.AddrFrom16((*windows.RawSockaddrInet6)(unsafe.Pointer(raw)).Addr)
	default:
		return netip.Addr{}
	}
}
