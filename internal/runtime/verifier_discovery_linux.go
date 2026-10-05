// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/Irviodotnet/Irvio/internal/bootstrap"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/reachability"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

// discoverPublicVerifiers requires distinct authenticated PeerIDs and public IPv4 addresses without weakening M11 quorum.
func discoverPublicVerifiers(ctx context.Context, node *discovery.Node, bootstrapAddresses []string, allowTest bool) ([]string, error) {
	lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
	ids, discoveryErr := node.FindRendezvous(lookup, "")
	cancel()
	result := make([]string, 0, reachability.DefaultVerifierLimit)
	seenIP := make(map[netip.Addr]struct{}, reachability.DefaultVerifierLimit)
	seenPeer := make(map[peer.ID]struct{}, reachability.DefaultVerifierLimit)
	for _, id := range ids {
		if id == node.PeerID() {
			continue
		}
		signed, readErr := node.Directory().SignedAnnouncement(ctx, id.String())
		if readErr != nil {
			continue
		}
		announcement, verifyErr := protocol.VerifySignedAnnouncement(signed, id, time.Now())
		if verifyErr != nil || announcement.Capabilities&protocol.CapRendezvous == 0 {
			continue
		}
		value, ip, ok := publicVerifierIPv4(id, announcement)
		if !ok {
			continue
		}
		if _, duplicate := seenIP[ip]; duplicate {
			continue
		}
		seenIP[ip] = struct{}{}
		seenPeer[id] = struct{}{}
		result = append(result, value)
		if len(result) == reachability.DefaultVerifierLimit {
			break
		}
	}
	if len(result) < reachability.DefaultVerifierLimit {
		seeds, err := bootstrap.Parse(bootstrapAddresses)
		if err != nil {
			return nil, fmt.Errorf("parse verifier bootstrap fallback: %w", err)
		}
		for _, seed := range seeds {
			if seed.ID == node.PeerID() {
				continue
			}
			if _, exists := seenPeer[seed.ID]; exists {
				continue
			}
			for _, address := range seed.Addrs {
				value, ip, ok := directIPv4Verifier(seed.ID, address, allowTest)
				if !ok {
					continue
				}
				if _, duplicate := seenIP[ip]; duplicate {
					continue
				}
				seenIP[ip] = struct{}{}
				seenPeer[seed.ID] = struct{}{}
				result = append(result, value)
				break
			}
			if len(result) == reachability.DefaultVerifierLimit {
				break
			}
		}
	}
	if len(result) < reachability.DefaultQuorum {
		return nil, fmt.Errorf("only %d independent Rendezvous/bootstrap verifier peers found; need %d (Rendezvous lookup: %v)", len(result), reachability.DefaultQuorum, discoveryErr)
	}
	return result, nil
}

func directIPv4Verifier(id peer.ID, address ma.Multiaddr, allowTest bool) (string, netip.Addr, bool) {
	parts := ma.Split(address)
	if len(parts) != 2 || parts[0].Protocols()[0].Code != ma.P_IP4 || parts[1].Protocols()[0].Code != ma.P_TCP {
		return "", netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(parts[0].Value())
	if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() ||
		!allowTest && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
			verifierNonPublicIPv4(ip)) {
		return "", netip.Addr{}, false
	}
	port := parts[1].Value()
	if port == "" || port == "0" {
		return "", netip.Addr{}, false
	}
	return fmt.Sprintf("/ip4/%s/tcp/%s/p2p/%s", ip, port, id), ip, true
}

func verifierNonPublicIPv4(ip netip.Addr) bool {
	for _, prefix := range [...]netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("240.0.0.0/4"),
	} {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func publicVerifierIPv4(id peer.ID, announcement *irviov1.NodeAnnouncement) (string, netip.Addr, bool) {
	if announcement == nil || announcement.Capabilities&protocol.CapPublicControlV4 == 0 {
		return "", netip.Addr{}, false
	}
	for _, endpoint := range announcement.PublicControlEndpoints {
		ip, ok := netip.AddrFromSlice(endpoint.GetObservedIp())
		if !ok || !ip.Is4() || endpoint.GetExternalControlPort() == 0 || endpoint.GetExternalControlPort() > 65535 {
			continue
		}
		return fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", ip, endpoint.GetExternalControlPort(), id), ip, true
	}
	return "", netip.Addr{}, false
}
