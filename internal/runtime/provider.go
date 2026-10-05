// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/protocol"
)

// DiscoveryCandidateProvider adapts verified bounded Kademlia results to M9.
type DiscoveryCandidateProvider struct {
	Node *discovery.Node
	Now  func() time.Time
}

func (p *DiscoveryCandidateProvider) GetCandidates(ctx context.Context, request exitselector.CandidateRequest) (exitselector.CandidateBatch, error) {
	if p == nil || p.Node == nil || ctx == nil || request.Country == "" ||
		request.Limit < 1 || request.Limit > exitselector.MaxCandidateBatch {
		return exitselector.CandidateBatch{}, ErrInvalidConfig
	}
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	ids, err := p.Node.FindExits(ctx, string(request.Country))
	if err != nil {
		return exitselector.CandidateBatch{}, err
	}
	excluded := make(map[peer.ID]struct{}, len(request.ExcludePeerIDs)+1)
	excluded[p.Node.PeerID()] = struct{}{}
	for _, id := range request.ExcludePeerIDs {
		excluded[id] = struct{}{}
	}
	candidates := make([]exitselector.Candidate, 0, request.Limit)
	available := 0
	for _, id := range ids {
		if _, skip := excluded[id]; skip {
			continue
		}
		record, err := p.Node.Directory().Peer(ctx, id.String())
		if err != nil || record.Country != string(request.Country) ||
			record.Capabilities&protocol.CapExit == 0 || len(record.AWGPublicKey) != 32 ||
			record.AWGKeyEpoch == 0 || !record.ExpiresAt.After(now) {
			continue
		}
		announcement, err := p.Node.VerifiedAnnouncement(ctx, id)
		if err != nil || announcement.AwgKeyEpoch != record.AWGKeyEpoch {
			continue
		}
		endpoint, ok := literalControlEndpoint(announcement.Libp2PAddrs)
		if !ok {
			continue
		}
		available++
		candidate := exitselector.Candidate{
			PeerID: id, Country: record.Country, ControlEndpoint: endpoint,
			ProtocolVersion: record.ProtocolVersion, Capabilities: record.Capabilities,
			Accepting: true, Uptime: exitselector.UptimeNew,
			NetworkEpoch: candidateNetworkEpoch(announcement.SessionEpoch, announcement.Libp2PAddrs...), GeneratedAt: now, ExpiresAt: record.ExpiresAt,
		}
		if now.Sub(record.FirstSeen) >= 24*time.Hour {
			candidate.Uptime = exitselector.UptimeMature
		}
		reservoirAdd(&candidates, candidate, request.Limit, available)
	}
	return exitselector.CandidateBatch{
		Country: request.Country, AvailablePeerCount: available,
		GeneratedAt: now, Candidates: candidates,
	}, nil
}

func candidateNetworkEpoch(sessionEpoch []byte, locators ...string) uint64 {
	return protocol.NetworkPathEpoch(sessionEpoch, locators)
}

func reservoirAdd(values *[]exitselector.Candidate, candidate exitselector.Candidate, limit, seen int) {
	if len(*values) < limit {
		*values = append(*values, candidate)
		return
	}
	var payload [8]byte
	if _, err := rand.Read(payload[:]); err != nil {
		return
	}
	index := int(binary.LittleEndian.Uint64(payload[:]) % uint64(seen))
	if index < limit {
		(*values)[index] = candidate
	}
}

func literalControlEndpoint(addresses []string) (netip.AddrPort, bool) {
	var privateFallback netip.AddrPort
	for _, encoded := range addresses {
		address, err := multiaddr.NewMultiaddr(encoded)
		if err != nil {
			continue
		}
		var ip netip.Addr
		if value, err := address.ValueForProtocol(multiaddr.P_IP4); err == nil {
			ip, err = netip.ParseAddr(value)
			if err != nil {
				continue
			}
		} else if value, err := address.ValueForProtocol(multiaddr.P_IP6); err == nil {
			ip, err = netip.ParseAddr(value)
			if err != nil {
				continue
			}
		} else {
			continue
		}
		portValue, err := address.ValueForProtocol(multiaddr.P_TCP)
		if err != nil {
			continue
		}
		var port uint16
		if _, err := fmt.Sscanf(portValue, "%d", &port); err != nil || port == 0 {
			continue
		}
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.Is4In6() {
			continue
		}
		endpoint := netip.AddrPortFrom(ip, port)
		if !ip.IsPrivate() {
			return endpoint, true
		}
		if !privateFallback.IsValid() {
			privateFallback = endpoint
		}
	}
	return privateFallback, privateFallback.IsValid()
}
