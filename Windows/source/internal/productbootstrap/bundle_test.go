// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productbootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	"github.com/Irviodotnet/Irvio/internal/discovery"
	irvioprotocol "github.com/Irviodotnet/Irvio/internal/protocol"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func TestBootstrapBundleRoundTripAndPeerDeduplication(t *testing.T) {
	first := testPeerID(t)
	second := testPeerID(t)
	bundle := &localv1.BootstrapBundle{
		FormatVersion: BundleVersion, GeneratedAtUnixMs: 123,
		Nodes: []*localv1.BootstrapNode{
			testNode(first, "203.0.113.8", 4001),
			testNode(second, "2001:db8::8", 4002),
			testNode(first, "203.0.113.8", 4001),
		},
	}
	code, err := Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, "irvio-bootstrap:v1:") {
		t.Fatalf("unexpected bundle code %q", code)
	}
	decoded, err := Decode(code)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Nodes) != 2 {
		t.Fatalf("deduplicated nodes=%d", len(decoded.Nodes))
	}
	reencoded, err := Encode(decoded)
	if err != nil || reencoded != code {
		t.Fatalf("deterministic round trip equal=%v err=%v", reencoded == code, err)
	}
	addresses, err := Multiaddrs(decoded)
	if err != nil || len(addresses) != 2 {
		t.Fatalf("multiaddrs=%v err=%v", addresses, err)
	}
}

func TestBootstrapBundleValidation(t *testing.T) {
	id := testPeerID(t)
	tests := []struct {
		name string
		code string
		want error
	}{
		{name: "malformed", code: BundlePrefix + "***", want: ErrInvalidBundle},
		{name: "legacy prefix rejected", code: "hydra-bootstrap:v1:AAAA", want: ErrInvalidBundle},
		{name: "unsupported", code: "irvio-bootstrap:v2:AAAA", want: ErrUnsupportedBundle},
		{name: "oversized", code: strings.Repeat("x", MaxBundleCodeBytes+1), want: ErrBundleTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode(test.code)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
	invalid := testNode(id, "0.0.0.0", 4001)
	_, err := Normalize(&localv1.BootstrapBundle{FormatVersion: BundleVersion, Nodes: []*localv1.BootstrapNode{invalid}})
	if !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("unspecified endpoint: %v", err)
	}
	overflow := &localv1.BootstrapBundle{
		FormatVersion: BundleVersion,
		Nodes: []*localv1.BootstrapNode{
			{PeerId: id.String(), Endpoints: []*localv1.BootstrapEndpoint{
				testEndpoint("192.0.2.1", 4001), testEndpoint("192.0.2.2", 4001),
				testEndpoint("192.0.2.3", 4001), testEndpoint("192.0.2.4", 4001),
			}},
			testNode(id, "192.0.2.5", 4001),
		},
	}
	if _, err := Normalize(overflow); !errors.Is(err, ErrBundleTooLarge) {
		t.Fatalf("merged endpoint overflow: %v", err)
	}
}

func TestIdleImportPersistsWithoutJoiningNetwork(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := New(Config{DataDir: directory, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	bundle := &localv1.BootstrapBundle{FormatVersion: BundleVersion,
		Nodes: []*localv1.BootstrapNode{testNode(testPeerID(t), "89.124.71.34", 4000)}}
	code, err := Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.Import(code)
	if err != nil || state.ConfiguredCount != 1 || state.CurrentlyReachableCount != 0 {
		t.Fatalf("idle import state=%+v err=%v", state, err)
	}
	addresses, err := manager.StartupAddresses()
	if err != nil || len(addresses) != 1 {
		t.Fatalf("startup addresses=%v err=%v", addresses, err)
	}
	reopened, err := New(Config{DataDir: directory, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.State().ConfiguredCount; got != 1 {
		t.Fatalf("persisted bootstrap count=%d", got)
	}
}

func TestBootstrapStoreRetainsWholeBundleAcrossImportsAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	config := Config{DataDir: path, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	manager, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	combined := &localv1.BootstrapBundle{FormatVersion: BundleVersion}
	for batch := 0; batch < 2; batch++ {
		bundle := &localv1.BootstrapBundle{FormatVersion: BundleVersion}
		for range 3 {
			node := testNode(testPeerID(t), "198.51.100.9", 4000)
			node.Endpoints = append(node.Endpoints, testEndpoint("2001:db8::9", 4000))
			bundle.Nodes = append(bundle.Nodes, node)
			combined.Nodes = append(combined.Nodes, node)
		}
		code, err := Encode(bundle)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Import(code); err != nil {
			t.Fatal(err)
		}
	}
	manager.Close()
	reopened, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !proto.Equal(mustNormalize(t, combined), reopened.persisted) {
		t.Fatal("bundle lost or changed peers/endpoints across imports and restart")
	}
	addresses, err := reopened.StartupAddresses()
	if err != nil || len(addresses) != 12 || reopened.State().CurrentlyReachableCount != 0 {
		t.Fatalf("idle bundle addresses=%d err=%v", len(addresses), err)
	}
}

func TestHotImportPersistsAndAuthenticatesIntroducer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	introducer, err := discovery.Start(ctx, discovery.Config{
		DataDir: filepath.Join(t.TempDir(), "introducer"), ListenPort: 0, Logger: logger, DHTMode: "server",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer introducer.Close()
	clientDir := filepath.Join(t.TempDir(), "client")
	client, err := discovery.Start(ctx, discovery.Config{
		DataDir: clientDir, ListenPort: 0, Logger: logger, DHTMode: "server",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	manager, err := New(Config{DataDir: clientDir, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Attach(ctx, client); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if phase := manager.State().Phase; phase != localv1.BootstrapPhase_BOOTSTRAP_PHASE_EMPTY {
		t.Fatalf("fresh state=%v", phase)
	}
	// Use dialable loopback bootstrap addresses, not wildcard listeners, in this local test.
	address, err := introducer.IPv4LoopbackAddress()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := FromMultiaddrs([]string{address}, uint64(time.Now().UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	code, err := Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Import(code); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	importGeneration := manager.generation
	manager.mu.Unlock()
	if _, err := manager.Import(code); err != nil {
		t.Fatalf("repeat import: %v", err)
	}
	manager.mu.Lock()
	repeatedGeneration := manager.generation
	manager.mu.Unlock()
	if repeatedGeneration != importGeneration {
		t.Fatalf("repeat import restarted join: generation %d -> %d", importGeneration, repeatedGeneration)
	}
	deadline := time.NewTimer(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for client.Host().Network().Connectedness(introducer.PeerID()) != network.Connected {
		select {
		case <-deadline.C:
			t.Fatal("imported introducer was not authenticated")
		case <-ticker.C:
		}
	}
	reloaded, err := New(Config{DataDir: clientDir, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	addresses, err := reloaded.StartupAddresses()
	if err != nil || len(addresses) == 0 {
		t.Fatalf("persisted addresses=%v err=%v", addresses, err)
	}
}

func TestShareEligibilityRequiresPublicDiscoveryEndpoint(t *testing.T) {
	id := testPeerID(t)
	announcement := &irviov1.NodeAnnouncement{
		Capabilities: irvioprotocol.CapDiscovery | irvioprotocol.CapPublicControlV4,
		PublicControlEndpoints: []*irviov1.PublicControlEndpoint{{
			ObservedIp: netip.MustParseAddr("203.0.113.12").AsSlice(), ExternalControlPort: 4001,
		}},
	}
	if eligibleNode(id, announcement, false) != nil {
		t.Fatal("documentation endpoint escaped production Share Irvio policy")
	}
	if node := eligibleNode(id, announcement, true); node == nil || len(node.Endpoints) != 1 {
		t.Fatalf("valid test-policy public introducer=%v", node)
	}
	announcement.Capabilities &^= irvioprotocol.CapDiscovery
	if eligibleNode(id, announcement, true) != nil {
		t.Fatal("non-discovery peer became a bootstrap introducer")
	}
}

func TestBootstrapCodeContainsNoPrivateMaterial(t *testing.T) {
	bundle := &localv1.BootstrapBundle{
		FormatVersion: BundleVersion,
		Nodes:         []*localv1.BootstrapNode{testNode(testPeerID(t), "203.0.113.10", 4001)},
	}
	code, err := Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private", "password", "credential", "lease", "awg"} {
		if bytes.Contains(bytes.ToLower([]byte(code)), []byte(forbidden)) {
			t.Fatalf("bundle contains %q", forbidden)
		}
	}
}

func testPeerID(t *testing.T) peer.ID {
	t.Helper()
	privateKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func testNode(id peer.ID, address string, port uint32) *localv1.BootstrapNode {
	return &localv1.BootstrapNode{
		PeerId:    id.String(),
		Endpoints: []*localv1.BootstrapEndpoint{testEndpoint(address, port)},
	}
}

func testEndpoint(address string, port uint32) *localv1.BootstrapEndpoint {
	ip := netip.MustParseAddr(address)
	return &localv1.BootstrapEndpoint{Ip: append([]byte(nil), ip.AsSlice()...), Port: port}
}

func mustNormalize(t *testing.T, bundle *localv1.BootstrapBundle) *localv1.BootstrapBundle {
	t.Helper()
	value, err := Normalize(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
