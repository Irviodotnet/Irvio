// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import "hydra.local/hydra/internal/networkstate"

// UnderlayFromNetworkState uses only authoritative normalized network snapshots.
func UnderlayFromNetworkState(state networkstate.State) UnderlayState {
	result := UnderlayState{NetworkGeneration: state.Generation}
	links := make(map[int]networkstate.Link, len(state.Snapshot.Links))
	for _, link := range state.Snapshot.Links {
		links[link.Index] = link
	}
	for _, route := range state.Snapshot.Routes {
		link, ok := links[route.LinkIndex]
		if !ok || !link.Up {
			continue
		}
		usable := false
		for _, prefix := range link.Addresses {
			address := prefix.Addr()
			if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.Is4In6() || address.IsLinkLocalUnicast() {
				continue
			}
			if (route.Family == networkstate.FamilyIPv4 && address.Is4()) || (route.Family == networkstate.FamilyIPv6 && address.Is6()) {
				usable = true
				break
			}
		}
		if !usable {
			continue
		}
		switch route.Family {
		case networkstate.FamilyIPv4:
			result.IPv4Usable = true
		case networkstate.FamilyIPv6:
			result.IPv6Usable = true
		}
	}
	return result
}
