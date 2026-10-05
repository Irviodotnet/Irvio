// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package networkstate

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
)

// Normalize retains only default-route links in deterministic order.
func Normalize(routes []Route, links []Link) (Snapshot, error) {
	normalizedRoutes, relevant, err := normalizeRoutes(routes)
	if err != nil {
		return Snapshot{}, err
	}
	normalizedLinks, err := normalizeLinks(links, relevant)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Routes: normalizedRoutes, Links: normalizedLinks}, nil
}

func normalizeRoutes(routes []Route) ([]Route, map[int]struct{}, error) {
	normalized := make([]Route, 0, len(routes))
	relevant := make(map[int]struct{}, len(routes))
	seen := make(map[Route]struct{}, len(routes))
	for _, route := range routes {
		if err := validateFamily(route.Family); err != nil {
			return nil, nil, err
		}
		if route.LinkIndex <= 0 {
			return nil, nil, fmt.Errorf("default route has invalid link index %d", route.LinkIndex)
		}
		if route.Table == 0 {
			route.Table = mainRouteTable
		}
		if route.Table != mainRouteTable {
			continue
		}
		if route.Gateway.IsValid() {
			if route.Gateway.Is4In6() || route.Gateway.IsUnspecified() || route.Gateway.IsMulticast() || familyForAddr(route.Gateway) != route.Family {
				return nil, nil, fmt.Errorf("invalid %s default-route gateway %s", route.Family, route.Gateway)
			}
		}
		if route.PreferredSource.IsValid() && (route.PreferredSource.Is4In6() || route.PreferredSource.IsUnspecified() ||
			route.PreferredSource.IsMulticast() || familyForAddr(route.PreferredSource) != route.Family) {
			return nil, nil, fmt.Errorf("invalid %s default-route preferred source %s", route.Family, route.PreferredSource)
		}
		if _, ok := seen[route]; ok {
			continue
		}
		seen[route] = struct{}{}
		normalized = append(normalized, route)
		relevant[route.LinkIndex] = struct{}{}
	}
	slices.SortFunc(normalized, compareRoutes)
	return normalized, relevant, nil
}

func compareRoutes(a, b Route) int {
	if n := cmp.Compare(a.Family, b.Family); n != 0 {
		return n
	}
	if n := cmp.Compare(a.Priority, b.Priority); n != 0 {
		return n
	}
	if n := cmp.Compare(a.LinkIndex, b.LinkIndex); n != 0 {
		return n
	}
	if n := a.Gateway.Compare(b.Gateway); n != 0 {
		return n
	}
	return a.PreferredSource.Compare(b.PreferredSource)
}

func normalizeLinks(links []Link, relevant map[int]struct{}) ([]Link, error) {
	byIndex := make(map[int]Link, len(relevant))
	for _, link := range links {
		if _, ok := relevant[link.Index]; !ok {
			continue
		}
		if link.Index <= 0 || link.Name == "" || link.MTU <= 0 {
			return nil, fmt.Errorf("invalid link metadata for index %d", link.Index)
		}
		addresses, err := normalizeAddresses(link.Addresses)
		if err != nil {
			return nil, fmt.Errorf("normalize link %s: %w", link.Name, err)
		}
		link.Addresses = addresses
		if existing, ok := byIndex[link.Index]; ok {
			if existing.Name != link.Name || existing.Up != link.Up || existing.MTU != link.MTU {
				return nil, fmt.Errorf("conflicting metadata for link index %d", link.Index)
			}
			existing.Addresses = append(existing.Addresses, link.Addresses...)
			existing.Addresses, err = normalizeAddresses(existing.Addresses)
			if err != nil {
				return nil, err
			}
			byIndex[link.Index] = existing
			continue
		}
		byIndex[link.Index] = link
	}

	normalized := make([]Link, 0, len(relevant))
	for index := range relevant {
		link, ok := byIndex[index]
		if !ok {
			return nil, fmt.Errorf("default route refers to missing link index %d", index)
		}
		normalized = append(normalized, link)
	}
	slices.SortFunc(normalized, func(a, b Link) int { return cmp.Compare(a.Index, b.Index) })
	return normalized, nil
}

func normalizeAddresses(addresses []netip.Prefix) ([]netip.Prefix, error) {
	normalized := make([]netip.Prefix, 0, len(addresses))
	seen := make(map[netip.Prefix]struct{}, len(addresses))
	for _, prefix := range addresses {
		addr := prefix.Addr()
		if !prefix.IsValid() || addr.Is4In6() {
			return nil, fmt.Errorf("invalid address prefix %s", prefix)
		}
		if addr.IsUnspecified() || addr.IsMulticast() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
			continue
		}
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		normalized = append(normalized, prefix)
	}
	slices.SortFunc(normalized, comparePrefixes)
	return normalized, nil
}

func comparePrefixes(a, b netip.Prefix) int {
	if n := cmp.Compare(a.Addr().BitLen(), b.Addr().BitLen()); n != 0 {
		return n
	}
	if n := a.Addr().Compare(b.Addr()); n != 0 {
		return n
	}
	return cmp.Compare(a.Bits(), b.Bits())
}

// Equal reports whether two normalized snapshots describe the same state.
func (s Snapshot) Equal(other Snapshot) bool {
	if !slices.Equal(s.Routes, other.Routes) || len(s.Links) != len(other.Links) {
		return false
	}
	for i := range s.Links {
		a, b := s.Links[i], other.Links[i]
		if a.Index != b.Index || a.Name != b.Name || a.Up != b.Up || a.MTU != b.MTU || !slices.Equal(a.Addresses, b.Addresses) {
			return false
		}
	}
	return true
}

// transportEquivalent ignores metric magnitudes, but preserves route preference and equal-cost groups.
func (s Snapshot) transportEquivalent(other Snapshot) bool {
	if len(s.Routes) != len(other.Routes) {
		return false
	}
	a, b := s.Clone(), other.Clone()
	for i := range a.Routes {
		if i > 0 && s.Routes[i].Family == s.Routes[i-1].Family &&
			(s.Routes[i].Priority == s.Routes[i-1].Priority) != (other.Routes[i].Priority == other.Routes[i-1].Priority) {
			return false
		}
		a.Routes[i].Priority, b.Routes[i].Priority = 0, 0
	}
	return a.Equal(b)
}

// Clone returns a deep copy suitable for transfer across ownership boundaries.
func (s Snapshot) Clone() Snapshot {
	clone := Snapshot{Routes: slices.Clone(s.Routes), Links: make([]Link, len(s.Links))}
	for i, link := range s.Links {
		clone.Links[i] = link
		clone.Links[i].Addresses = slices.Clone(link.Addresses)
	}
	return clone
}
