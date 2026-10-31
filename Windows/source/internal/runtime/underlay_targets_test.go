// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/underlay"
)

func TestDiscoveryUnderlayTargetsRequireVerifiedAnnouncementAndCompleteChallenge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	key := make([]byte, len(awg.Key{}))
	for index := range key {
		key[index] = byte(index + 1)
	}
	server, err := discovery.Start(ctx, discovery.Config{
		DataDir: filepath.Join(t.TempDir(), "profile"), ListenPort: 0, DHTMode: "server", Logger: logger,
		AWGPublicKey: key, AWGKeyEpoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := discovery.Start(ctx, discovery.Config{
		DataDir: filepath.Join(t.TempDir(), "profile"), ListenPort: 0, DHTMode: "server", Logger: logger,
		AWGPublicKey: key, AWGKeyEpoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.Host().Peerstore().AddAddrs(server.PeerID(), server.Host().Addrs(), time.Minute)
	if err := client.Host().Connect(ctx, peer.AddrInfo{ID: server.PeerID(), Addrs: server.Host().Addrs()}); err != nil {
		t.Fatal(err)
	}
	provider := &DiscoveryUnderlayTargets{Node: client, Now: time.Now}
	before, err := provider.Targets(ctx, networkstate.FamilyIPv4, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatal("unverified peer was exposed as an underlay oracle target")
	}
	if err := client.AuthenticatePeer(ctx, server.PeerID()); err != nil {
		t.Fatal(err)
	}
	targets, err := provider.Targets(ctx, networkstate.FamilyIPv4, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].PeerID != server.PeerID() || !targets[0].Endpoint.Addr().Is4() {
		t.Fatalf("verified targets=%+v", targets)
	}
	service, err := underlay.NewLibp2pChallengeService(server.Host())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	probe, err := underlay.NewLibp2pChallengeProbe(client.Host())
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.Probe(ctx, targets[0]); err != nil {
		t.Fatal(err)
	}
}

func TestLiteralControlEndpointRejectsLocalOnlyAddresses(t *testing.T) {
	t.Parallel()

	endpoint, ok := literalControlEndpoint([]string{
		"/ip4/127.0.0.1/tcp/42002",
		"/ip6/::1/tcp/42002",
		"/ip6/fe80::1/tcp/42002",
		"/ip4/198.18.77.2/tcp/42002",
	})
	if !ok || endpoint != netip.MustParseAddrPort("198.18.77.2:42002") {
		t.Fatalf("control endpoint = %s, %v", endpoint, ok)
	}
}

func TestLiteralControlEndpointRejectsOnlyLocalAddresses(t *testing.T) {
	t.Parallel()

	if endpoint, ok := literalControlEndpoint([]string{
		"/ip4/127.0.0.1/tcp/42002",
		"/ip6/::1/tcp/42002",
		"/ip6/fe80::1/tcp/42002",
	}); ok {
		t.Fatalf("accepted local-only endpoint %s", endpoint)
	}
}

func TestUnderlayTargetEndpointRejectsLocalOnlyAddresses(t *testing.T) {
	t.Parallel()

	endpoint, ok := literalControlEndpointForFamily([]string{
		"/ip4/127.0.0.1/tcp/42002",
		"/ip4/169.254.1.1/tcp/42002",
		"/ip4/198.18.77.2/tcp/42002",
	}, networkstate.FamilyIPv4)
	if !ok || endpoint != netip.MustParseAddrPort("198.18.77.2:42002") {
		t.Fatalf("IPv4 underlay endpoint = %s, %v", endpoint, ok)
	}
	if endpoint, ok := literalControlEndpointForFamily([]string{"/ip6/::1/tcp/42002", "/ip6/fe80::1/tcp/42002"}, networkstate.FamilyIPv6); ok {
		t.Fatalf("accepted local-only IPv6 underlay endpoint %s", endpoint)
	}
}
