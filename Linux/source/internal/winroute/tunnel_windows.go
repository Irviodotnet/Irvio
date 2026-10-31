// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"fmt"
	"net/netip"
)

// ResolveTunnel selects a ready source only on the caller-owned virtual interface, never the default WAN.
func ResolveTunnel(target netip.Addr, luid uint64) (Binding, error) {
	if luid == 0 || !usableUnderlay(target) || !target.IsGlobalUnicast() {
		return Binding{}, fmt.Errorf("tunnel dial requires an explicit interface and literal unicast destination")
	}
	if err := routeReady(luid); err != nil {
		return Binding{}, err
	}
	row, source, err := bestRoute(target, luid)
	if err != nil {
		return Binding{}, err
	}
	if row.Loopback != 0 || !usableUnderlay(source) || source.Is4() != target.Is4() {
		return Binding{}, fmt.Errorf("tunnel route has an unusable source or loops back to this host")
	}
	binding := Binding{LUID: row.InterfaceLuid, Index: row.InterfaceIndex, Source: source}
	if _, err := binding.validate(); err != nil {
		return Binding{}, err
	}
	return binding, nil
}
