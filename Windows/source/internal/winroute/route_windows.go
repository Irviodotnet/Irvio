// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package winroute resolves native Windows paths and pins individual sockets to an interface.
package winroute

import (
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getBestRoute2 = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetBestRoute2")

// Binding requires a strong-host source/interface pair; WFP must fence later routing-policy changes.
type Binding struct {
	LUID   uint64
	Index  uint32
	Source netip.Addr
}

// Path describes the kernel-selected path; it does not establish reachability or a public endpoint.
type Path struct {
	Binding Binding
	Name    string
	Target  netip.Addr
	Gateway netip.Addr
	Prefix  netip.Prefix
	Metric  uint64
	MTU     uint32
}

// ResolveUnderlay rejects local destinations and caller-owned tunnel LUIDs without guessing by name.
func ResolveUnderlay(target netip.Addr, constrainedLUID uint64, excludedLUIDs []uint64) (Path, error) {
	if !usableUnderlay(target) {
		return Path{}, fmt.Errorf("underlay target must be a usable literal IP")
	}
	local, err := ownsAddress(target)
	if err != nil {
		return Path{}, err
	}
	if local {
		return Path{}, fmt.Errorf("underlay target is an address of this host")
	}
	row, source, err := bestRoute(target, constrainedLUID)
	if err != nil {
		return Path{}, err
	}
	for _, excluded := range excludedLUIDs {
		if excluded != 0 && row.InterfaceLuid == excluded {
			return Path{}, fmt.Errorf("kernel underlay route uses an excluded tunnel interface")
		}
	}
	binding := Binding{LUID: row.InterfaceLuid, Index: row.InterfaceIndex, Source: source}
	if row.Loopback != 0 || !usableUnderlay(source) || source.Is4() != target.Is4() {
		return Path{}, fmt.Errorf("kernel route has an unusable underlay source")
	}
	iface, err := binding.validate()
	if err != nil {
		return Path{}, err
	}
	if iface.Type == windows.IF_TYPE_SOFTWARE_LOOPBACK {
		return Path{}, fmt.Errorf("kernel underlay route uses loopback")
	}
	ip := windows.MibIpInterfaceRow{Family: addressFamily(source), InterfaceLuid: binding.LUID}
	if err := windows.GetIpInterfaceEntry(&ip); err != nil {
		return Path{}, fmt.Errorf("query underlay IP interface: %w", err)
	}
	if ip.Connected == 0 || ip.NlMtu == 0 {
		return Path{}, fmt.Errorf("kernel underlay IP interface is unavailable")
	}
	gateway := addressFromNative(&row.NextHop)
	if !gateway.IsValid() || gateway.Is4() != target.Is4() || gateway.IsMulticast() {
		return Path{}, fmt.Errorf("kernel underlay gateway is invalid")
	}
	if gateway.IsUnspecified() {
		gateway = netip.Addr{}
	}
	prefix := netip.PrefixFrom(addressFromNative(&row.DestinationPrefix.Prefix), int(row.DestinationPrefix.PrefixLength))
	if !prefix.IsValid() || !prefix.Contains(target) {
		return Path{}, fmt.Errorf("kernel route prefix does not contain target")
	}
	return Path{Binding: binding, Name: windows.UTF16ToString(iface.Alias[:]), Target: target,
		Gateway: gateway, Prefix: prefix, Metric: uint64(row.Metric) + uint64(ip.Metric), MTU: ip.NlMtu}, nil
}

// ForSource finds exactly one ready owner of a concrete local address, including lab loopback addresses.
func ForSource(source netip.Addr) (Binding, error) {
	if !usableSource(source) {
		return Binding{}, fmt.Errorf("socket source must be a concrete unicast address")
	}
	var table *windows.MibUnicastIpAddressTable
	if err := windows.GetUnicastIpAddressTable(addressFamily(source), &table); err != nil {
		return Binding{}, fmt.Errorf("query local source owners: %w", err)
	}
	if table == nil {
		return Binding{}, fmt.Errorf("local source table missing")
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	if table.NumEntries > 131072 {
		return Binding{}, fmt.Errorf("local source table exceeds bounded inventory")
	}
	binding, err := sourceOwner(source, unsafe.Slice(&table.Table[0], table.NumEntries))
	if err != nil {
		return Binding{}, err
	}
	if _, err := binding.validate(); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

// sourceOwner rejects ambiguity and stale addresses before the live interface checks.
func sourceOwner(source netip.Addr, rows []windows.MibUnicastIpAddressRow) (Binding, error) {
	var result Binding
	for _, row := range rows {
		if addressFromNative((*windows.RawSockaddrInet)(unsafe.Pointer(&row.Address))) != source {
			continue
		}
		binding := Binding{LUID: row.InterfaceLuid, Index: row.InterfaceIndex, Source: source}
		if binding.LUID == 0 || binding.Index == 0 || row.DadState != windows.IpDadStatePreferred || row.ValidLifetime == 0 {
			return Binding{}, fmt.Errorf("socket source has an unready local owner")
		}
		if result.LUID != 0 && result != binding {
			return Binding{}, fmt.Errorf("socket source belongs to multiple interfaces")
		}
		result = binding
	}
	if result.LUID == 0 {
		return Binding{}, fmt.Errorf("socket source has no ready local interface")
	}
	return result, nil
}

func (b Binding) validate() (windows.MibIfRow2, error) {
	var row windows.MibIfRow2
	if b.LUID == 0 || b.Index == 0 || !usableSource(b.Source) {
		return row, fmt.Errorf("invalid source/interface binding")
	}
	row.InterfaceLuid = b.LUID
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		return row, fmt.Errorf("query bound interface: %w", err)
	}
	if row.InterfaceIndex != b.Index || row.OperStatus != windows.IfOperStatusUp {
		return row, fmt.Errorf("bound interface changed or is down")
	}
	u := windows.MibUnicastIpAddressRow{InterfaceLuid: b.LUID}
	*(*windows.RawSockaddrInet)(unsafe.Pointer(&u.Address)) = nativeAddress(b.Source)
	if err := windows.GetUnicastIpAddressEntry(&u); err != nil {
		return row, fmt.Errorf("query bound source: %w", err)
	}
	if u.InterfaceIndex != b.Index || u.DadState != windows.IpDadStatePreferred || u.ValidLifetime == 0 {
		return row, fmt.Errorf("bound source is not ready on this interface")
	}
	ip := windows.MibIpInterfaceRow{Family: addressFamily(b.Source), InterfaceLuid: b.LUID}
	if err := windows.GetIpInterfaceEntry(&ip); err != nil {
		return row, fmt.Errorf("query bound IP interface: %w", err)
	}
	if err := b.validateIPInterface(ip); err != nil {
		return row, err
	}
	return row, nil
}

func (b Binding) validateIPInterface(ip windows.MibIpInterfaceRow) error {
	if ip.InterfaceLuid != b.LUID || ip.InterfaceIndex != b.Index || ip.Family != addressFamily(b.Source) || ip.Connected == 0 {
		return fmt.Errorf("bound IP interface changed or is unavailable")
	}
	if ip.ForwardingEnabled != 0 || ip.WeakHostSend != 0 {
		return fmt.Errorf("socket interface pinning requires forwarding and weak-host send disabled on the bound interface")
	}
	return nil
}

func bestRoute(target netip.Addr, luid uint64) (windows.MibIpForwardRow2, netip.Addr, error) {
	destination := nativeAddress(target)
	var row windows.MibIpForwardRow2
	var source windows.RawSockaddrInet
	var constrained *uint64
	if luid != 0 {
		constrained = &luid
	}
	result, _, _ := getBestRoute2.Call(uintptr(unsafe.Pointer(constrained)), 0, 0,
		uintptr(unsafe.Pointer(&destination)), 0, uintptr(unsafe.Pointer(&row)), uintptr(unsafe.Pointer(&source)))
	runtime.KeepAlive(constrained)
	if result != 0 {
		return row, netip.Addr{}, fmt.Errorf("kernel route lookup: %w", windows.Errno(result))
	}
	if row.InterfaceLuid == 0 || row.InterfaceIndex == 0 || (luid != 0 && row.InterfaceLuid != luid) {
		return row, netip.Addr{}, fmt.Errorf("kernel route ignored the required interface")
	}
	return row, addressFromNative(&source), nil
}

func ownsAddress(target netip.Addr) (bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false, err
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			return false, err
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err == nil && prefix.Addr() == target {
				return true, nil
			}
		}
	}
	return false, nil
}

func usableSource(a netip.Addr) bool {
	return a.IsValid() && !a.Is4In6() && !a.IsUnspecified() && !a.IsMulticast() && !a.IsLinkLocalUnicast() && a.Zone() == ""
}

func usableUnderlay(a netip.Addr) bool { return usableSource(a) && !a.IsLoopback() }

func addressFamily(a netip.Addr) uint16 {
	if a.Is4() {
		return windows.AF_INET
	}
	return windows.AF_INET6
}

func nativeAddress(a netip.Addr) windows.RawSockaddrInet {
	var raw windows.RawSockaddrInet
	if a.Is4() {
		v := (*windows.RawSockaddrInet4)(unsafe.Pointer(&raw))
		v.Family, v.Addr = windows.AF_INET, a.As4()
	} else {
		v := (*windows.RawSockaddrInet6)(unsafe.Pointer(&raw))
		v.Family, v.Addr = windows.AF_INET6, a.As16()
	}
	return raw
}

func addressFromNative(raw *windows.RawSockaddrInet) netip.Addr {
	switch raw.Family {
	case windows.AF_INET:
		return netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(raw)).Addr)
	case windows.AF_INET6:
		return netip.AddrFrom16((*windows.RawSockaddrInet6)(unsafe.Pointer(raw)).Addr)
	default:
		return netip.Addr{}
	}
}
