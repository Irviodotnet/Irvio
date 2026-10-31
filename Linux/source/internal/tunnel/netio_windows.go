// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	tunIPHelper          = windows.NewLazySystemDLL("iphlpapi.dll")
	tunGetIPInterface    = tunIPHelper.NewProc("GetIpInterfaceEntry")
	tunSetIPInterface    = tunIPHelper.NewProc("SetIpInterfaceEntry")
	tunInitializeUnicast = tunIPHelper.NewProc("InitializeUnicastIpAddressEntry")
	tunCreateUnicast     = tunIPHelper.NewProc("CreateUnicastIpAddressEntry")
	tunDeleteUnicast     = tunIPHelper.NewProc("DeleteUnicastIpAddressEntry")
	tunSetUnicast        = tunIPHelper.NewProc("SetUnicastIpAddressEntry")
)

// The SDK uses one-byte offload bitfields; pinned x/sys uses uint32 and cannot safely round-trip this row.
type tunIPInterfaceRow struct {
	Family                               uint16
	InterfaceLuid                        uint64
	InterfaceIndex                       uint32
	MaxReassemblySize                    uint32
	InterfaceIdentifier                  uint64
	MinRouterAdvertisementInterval       uint32
	MaxRouterAdvertisementInterval       uint32
	AdvertisingEnabled                   uint8
	ForwardingEnabled                    uint8
	WeakHostSend                         uint8
	WeakHostReceive                      uint8
	UseAutomaticMetric                   uint8
	UseNeighborUnreachabilityDetection   uint8
	ManagedAddressConfigurationSupported uint8
	OtherStatefulConfigurationSupported  uint8
	AdvertiseDefaultRoute                uint8
	RouterDiscoveryBehavior              uint32
	DadTransmits                         uint32
	BaseReachableTime                    uint32
	RetransmitTime                       uint32
	PathMtuDiscoveryTimeout              uint32
	LinkLocalAddressBehavior             uint32
	LinkLocalAddressTimeout              uint32
	ZoneIndices                          [16]uint32
	SitePrefixLength                     uint32
	Metric                               uint32
	NlMtu                                uint32
	Connected                            uint8
	SupportsWakeUpPatterns               uint8
	SupportsNeighborDiscovery            uint8
	SupportsRouterDiscovery              uint8
	ReachableTime                        uint32
	TransmitOffload                      uint8
	ReceiveOffload                       uint8
	DisableDefaultRoutes                 uint8
}

type nativeWindowsLinks struct{}

func (nativeWindowsLinks) Configure(luid uint64, mtu int) (int, error) {
	iface := windows.MibIfRow2{InterfaceLuid: luid}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &iface); err != nil {
		return 0, err
	}
	for _, family := range []uint16{windows.AF_INET, windows.AF_INET6} {
		row := tunIPInterfaceRow{Family: family, InterfaceLuid: luid}
		if err := tunCallRow(tunGetIPInterface, unsafe.Pointer(&row)); err != nil {
			return 0, err
		}
		row.NlMtu = uint32(mtu)
		// Lease-owned TUN addresses are not on a shared LAN; keep readiness checks without DAD probes.
		row.DadTransmits = 0
		if family == windows.AF_INET {
			row.SitePrefixLength = 0
		}
		if err := tunCallRow(tunSetIPInterface, unsafe.Pointer(&row)); err != nil {
			return 0, err
		}
		if err := tunCallRow(tunGetIPInterface, unsafe.Pointer(&row)); err != nil {
			return 0, err
		}
		if row.DadTransmits != 0 {
			return 0, fmt.Errorf("Windows TUN DAD configuration did not reconcile")
		}
	}
	return int(iface.InterfaceIndex), nil
}

func tunCallRow(proc *windows.LazyProc, row unsafe.Pointer) error {
	result, _, _ := proc.Call(uintptr(row))
	if result != 0 {
		return fmt.Errorf("%s: %w", proc.Name, windows.Errno(result))
	}
	return nil
}

func tunAddressRow(luid uint64, prefix netip.Prefix) windows.MibUnicastIpAddressRow {
	var row windows.MibUnicastIpAddressRow
	tunInitializeUnicast.Call(uintptr(unsafe.Pointer(&row)))
	row.InterfaceLuid = luid
	row.OnLinkPrefixLength = uint8(prefix.Bits())
	row.PrefixOrigin, row.SuffixOrigin = 1, 1 // IpPrefixOriginManual / IpSuffixOriginManual.
	if prefix.Addr().Is4() {
		a := (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address))
		a.Family, a.Addr = windows.AF_INET, prefix.Addr().As4()
	} else {
		row.Address.Family, row.Address.Addr = windows.AF_INET6, prefix.Addr().As16()
	}
	return row
}

func (nativeWindowsLinks) Add(luid uint64, prefix netip.Prefix) error {
	row := tunAddressRow(luid, prefix)
	return tunCallRow(tunCreateUnicast, unsafe.Pointer(&row))
}

func (nativeWindowsLinks) Remove(luid uint64, prefix netip.Prefix) error {
	row := tunAddressRow(luid, prefix)
	err := tunCallRow(tunDeleteUnicast, unsafe.Pointer(&row))
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return nil
	}
	return err
}

func (nativeWindowsLinks) AutomaticSource(luid uint64, prefix netip.Prefix, enabled bool) error {
	row := tunAddressRow(luid, prefix)
	if err := windows.GetUnicastIpAddressEntry(&row); err != nil {
		return err
	}
	if row.InterfaceLuid != luid || row.OnLinkPrefixLength != uint8(prefix.Bits()) || row.PrefixOrigin != 1 || row.SuffixOrigin != 1 {
		return fmt.Errorf("TUN source address no longer matches its owned configuration")
	}
	row.SkipAsSource = 1
	if enabled {
		row.SkipAsSource = 0
	}
	want := row.SkipAsSource
	if err := tunCallRow(tunSetUnicast, unsafe.Pointer(&row)); err != nil {
		return err
	}
	if err := windows.GetUnicastIpAddressEntry(&row); err != nil {
		return err
	}
	if row.SkipAsSource != want {
		return fmt.Errorf("TUN automatic source selection did not reconcile")
	}
	return nil
}

func (nativeWindowsLinks) Ready(ctx context.Context, luid uint64, prefixes []netip.Prefix) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready := true
		for _, p := range prefixes {
			row := tunAddressRow(luid, p)
			if err := windows.GetUnicastIpAddressEntry(&row); err != nil {
				return err
			}
			if row.DadState != windows.IpDadStatePreferred {
				if row.DadState != windows.IpDadStateTentative {
					return fmt.Errorf("TUN address %s failed DAD (state %d)", p, row.DadState)
				}
				ready = false
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Windows TUN address readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
