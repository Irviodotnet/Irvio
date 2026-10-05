// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package transportgen

import (
	"fmt"
	"net/netip"

	"hydra.local/hydra/internal/networkstate"
)

// PreferredBindPlanner chooses the best normalized default route's first usable address per family.
type PreferredBindPlanner struct{}

// Plan chooses one explicit address per available IP family.
func (PreferredBindPlanner) Plan(snapshot networkstate.Snapshot) (BindPlan, error) {
	links := make(map[int]networkstate.Link, len(snapshot.Links))
	for _, link := range snapshot.Links {
		links[link.Index] = link
	}
	var plan BindPlan
	for _, route := range snapshot.Routes {
		link, ok := links[route.LinkIndex]
		if !ok || !link.Up {
			continue
		}
		addresses := link.Addresses
		if route.PreferredSource.IsValid() {
			for _, prefix := range addresses {
				if prefix.Addr() == route.PreferredSource {
					switch route.Family {
					case networkstate.FamilyIPv4:
						if !plan.IPv4.IsValid() {
							plan.IPv4 = route.PreferredSource
						}
					case networkstate.FamilyIPv6:
						if !plan.IPv6.IsValid() {
							plan.IPv6 = route.PreferredSource
						}
					}
					break
				}
			}
		}
		for _, prefix := range addresses {
			addr := prefix.Addr()
			switch route.Family {
			case networkstate.FamilyIPv4:
				if !plan.IPv4.IsValid() && addr.Is4() && !addr.Is4In6() {
					plan.IPv4 = addr
				}
			case networkstate.FamilyIPv6:
				if !plan.IPv6.IsValid() && addr.Is6() && !addr.Is4In6() {
					plan.IPv6 = addr
				}
			}
		}
	}
	if !plan.IPv4.IsValid() && !plan.IPv6.IsValid() {
		return BindPlan{}, fmt.Errorf("%w: no usable default-route address", ErrInvalidPlan)
	}
	return plan, nil
}

func validatePlan(plan BindPlan, snapshot networkstate.Snapshot, allowLoopback bool) error {
	addresses := []netip.Addr{plan.IPv4, plan.IPv6}
	valid := 0
	for index, addr := range addresses {
		if !addr.IsValid() {
			continue
		}
		valid++
		if addr.IsUnspecified() || addr.IsMulticast() || addr.Is4In6() || addr.IsLinkLocalUnicast() || (addr.IsLoopback() && !allowLoopback) {
			return fmt.Errorf("%w: unusable address %s", ErrInvalidPlan, addr)
		}
		if index == 0 && !addr.Is4() || index == 1 && !addr.Is6() {
			return fmt.Errorf("%w: address in wrong family slot", ErrInvalidPlan)
		}
		if addr.IsLoopback() && allowLoopback {
			continue
		}
		if !snapshotHasAddress(snapshot, addr) {
			return fmt.Errorf("%w: address %s is not in the snapshot", ErrInvalidPlan, addr)
		}
	}
	if valid == 0 {
		return fmt.Errorf("%w: no address", ErrInvalidPlan)
	}
	return nil
}

func snapshotHasAddress(snapshot networkstate.Snapshot, wanted netip.Addr) bool {
	for _, link := range snapshot.Links {
		if !link.Up {
			continue
		}
		for _, prefix := range link.Addresses {
			if prefix.Addr() == wanted {
				return true
			}
		}
	}
	return false
}
