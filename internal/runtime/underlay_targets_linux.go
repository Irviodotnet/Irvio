// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	multiaddr "github.com/multiformats/go-multiaddr"

	"hydra.local/hydra/internal/discovery"
	"hydra.local/hydra/internal/networkstate"
	"hydra.local/hydra/internal/protocol"
	"hydra.local/hydra/internal/underlay"
)

// DiscoveryUnderlayTargets uses only literal endpoints from locally verified signed announcements.
type DiscoveryUnderlayTargets struct {
	Node *discovery.Node
	Now  func() time.Time
}

func (p *DiscoveryUnderlayTargets) Targets(ctx context.Context, family networkstate.Family, limit int) ([]underlay.Target, error) {
	if p == nil || p.Node == nil || ctx == nil || limit < 1 || limit > 3 {
		return nil, ErrInvalidConfig
	}
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	peers := append([]peer.ID(nil), p.Node.Host().Peerstore().Peers()...)
	sort.Slice(peers, func(left, right int) bool { return peers[left].String() < peers[right].String() })
	result := make([]underlay.Target, 0, limit)
	seen := make(map[peer.ID]struct{}, len(peers))
	for _, remote := range peers {
		if remote == "" || remote == p.Node.PeerID() {
			continue
		}
		if _, duplicate := seen[remote]; duplicate {
			continue
		}
		seen[remote] = struct{}{}
		envelope, err := p.Node.Directory().SignedAnnouncement(ctx, remote.String())
		if err != nil {
			continue
		}
		announcement, err := protocol.VerifySignedAnnouncement(envelope, remote, now)
		if err != nil || announcement.Capabilities&protocol.CapDiscovery == 0 {
			continue
		}
		endpoint, ok := literalControlEndpointForFamily(announcement.Libp2PAddrs, family)
		if !ok {
			continue
		}
		result = append(result, underlay.Target{PeerID: remote, Endpoint: endpoint})
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func literalControlEndpointForFamily(addresses []string, family networkstate.Family) (netip.AddrPort, bool) {
	for _, encoded := range addresses {
		address, err := multiaddr.NewMultiaddr(encoded)
		if err != nil {
			continue
		}
		protocolCode := multiaddr.P_IP6
		if family == networkstate.FamilyIPv4 {
			protocolCode = multiaddr.P_IP4
		}
		value, err := address.ValueForProtocol(protocolCode)
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(value)
		if err != nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
			ip.IsMulticast() || ip.Is4In6() ||
			(family == networkstate.FamilyIPv4) != ip.Is4() {
			continue
		}
		portValue, err := address.ValueForProtocol(multiaddr.P_TCP)
		if err != nil {
			continue
		}
		port64, err := strconv.ParseUint(portValue, 10, 16)
		if err != nil || port64 == 0 {
			continue
		}
		return netip.AddrPortFrom(ip, uint16(port64)), true
	}
	return netip.AddrPort{}, false
}

type combinedUnderlayTargets struct {
	configured underlay.TargetProvider
	discovered underlay.TargetProvider
}

func (p combinedUnderlayTargets) Targets(ctx context.Context, family networkstate.Family, limit int) ([]underlay.Target, error) {
	result := make([]underlay.Target, 0, limit)
	appendFrom := func(source underlay.TargetProvider) error {
		if source == nil || len(result) == limit {
			return nil
		}
		values, err := source.Targets(ctx, family, limit-len(result))
		if err != nil {
			return err
		}
		for _, value := range values {
			duplicate := false
			for _, existing := range result {
				duplicate = duplicate || existing.PeerID != "" && existing.PeerID == value.PeerID || existing.Endpoint == value.Endpoint
			}
			if !duplicate {
				result = append(result, value)
				if len(result) == limit {
					break
				}
			}
		}
		return nil
	}
	if err := appendFrom(p.configured); err != nil {
		return nil, err
	}
	if err := appendFrom(p.discovered); err != nil && len(result) == 0 {
		return nil, err
	}
	return result, nil
}

type productionUnderlayProbe struct {
	raw    *underlay.MarkedChallengeProbe
	libp2p *underlay.Libp2pChallengeProbe
}

func (p productionUnderlayProbe) Probe(ctx context.Context, target underlay.Target) error {
	if target.PeerID != "" {
		return p.libp2p.Probe(ctx, target)
	}
	return p.raw.Probe(ctx, target)
}
