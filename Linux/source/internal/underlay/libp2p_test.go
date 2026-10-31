// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package underlay

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	multiaddr "github.com/multiformats/go-multiaddr"
)

func TestLibp2pChallengeAuthenticatesBoundedRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	service, err := NewLibp2pChallengeService(server)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	endpoint := endpointFromMultiaddr(t, server.Addrs()[0])
	probe, err := NewLibp2pChallengeProbe(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.Probe(ctx, Target{PeerID: server.ID(), Endpoint: endpoint}); err != nil {
		t.Fatal(err)
	}
	wrong := peer.ID("not-the-authenticated-server")
	if err := probe.Probe(ctx, Target{PeerID: wrong, Endpoint: endpoint}); err == nil {
		t.Fatal("challenge accepted a different authenticated PeerID")
	}
}

func endpointFromMultiaddr(t *testing.T, address multiaddr.Multiaddr) netip.AddrPort {
	t.Helper()
	ipValue, err := address.ValueForProtocol(multiaddr.P_IP4)
	if err != nil {
		t.Fatal(err)
	}
	ip, err := netip.ParseAddr(ipValue)
	if err != nil {
		t.Fatal(err)
	}
	portValue, err := address.ValueForProtocol(multiaddr.P_TCP)
	if err != nil {
		t.Fatal(err)
	}
	port, err := netip.ParseAddrPort("127.0.0.1:" + portValue)
	if err != nil {
		t.Fatal(err)
	}
	return netip.AddrPortFrom(ip, port.Port())
}
