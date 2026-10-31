// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

func TestParse(t *testing.T) {
	privateKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	input := "/ip4/127.0.0.1/tcp/41000/p2p/" + id.String()
	infos, err := Parse([]string{input})
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].ID != id || len(infos[0].Addrs) != 1 {
		t.Fatalf("unexpected bootstrap infos: %+v", infos)
	}
	infos, err = Parse([]string{input, input})
	if err != nil || len(infos) != 1 || len(infos[0].Addrs) != 1 {
		t.Fatalf("duplicate PeerID/address was not collapsed: infos=%+v err=%v", infos, err)
	}
	if _, err := Parse([]string{"/ip4/127.0.0.1/tcp/41000"}); err == nil {
		t.Fatal("address without PeerID was accepted")
	}
}

func TestConnectEmptySetKeepsNodeAvailable(t *testing.T) {
	h := newBootstrapHost(t)
	if err := ConnectWithPolicy(context.Background(), h, nil, testLogger(), testConnectPolicy()); err != nil {
		t.Fatalf("empty bootstrap set failed: %v", err)
	}
}

func TestConnectSucceedsWithOneLivePeer(t *testing.T) {
	client := newBootstrapHost(t)
	live := newBootstrapHost(t)
	if err := ConnectWithPolicy(context.Background(), client, []peer.AddrInfo{hostInfo(live)}, testLogger(), testConnectPolicy()); err != nil {
		t.Fatalf("live bootstrap failed: %v", err)
	}
}

func TestConnectSucceedsWithFourDeadAndOneLivePeer(t *testing.T) {
	client := newBootstrapHost(t)
	live := newBootstrapHost(t)
	peers := make([]peer.AddrInfo, 0, 5)
	for range 4 {
		peers = append(peers, deadPeer(t))
	}
	peers = append(peers, hostInfo(live))
	if err := ConnectWithPolicy(context.Background(), client, peers, testLogger(), testConnectPolicy()); err != nil {
		t.Fatalf("one live bootstrap among dead peers failed: %v", err)
	}
}

func TestConnectAllDeadFailsWithinBound(t *testing.T) {
	h := newBootstrapHost(t)
	started := time.Now()
	err := ConnectWithPolicy(context.Background(), h, []peer.AddrInfo{deadPeer(t), deadPeer(t)}, testLogger(), testConnectPolicy())
	if err == nil {
		t.Fatal("all-dead bootstrap set succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("all-dead bootstrap exceeded bounded deadline: %s", elapsed)
	}
}

func newBootstrapHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func hostInfo(h host.Host) peer.AddrInfo {
	return peer.AddrInfo{ID: h.ID(), Addrs: h.Addrs()}
}

func deadPeer(t *testing.T) peer.AddrInfo {
	t.Helper()
	privateKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	address, err := multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/1")
	if err != nil {
		t.Fatal(err)
	}
	return peer.AddrInfo{ID: id, Addrs: []multiaddr.Multiaddr{address}}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConnectPolicy() ConnectPolicy {
	return ConnectPolicy{MaxConcurrency: 4, PeerTimeout: 200 * time.Millisecond, OverallTimeout: 500 * time.Millisecond}
}
