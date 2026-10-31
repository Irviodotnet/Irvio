// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"google.golang.org/protobuf/proto"

	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/reachability"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type testInboundChallenge struct{ key crypto.PrivKey }

func (c testInboundChallenge) Challenge(ctx context.Context, target peer.AddrInfo, challenge *irviov1.ReachabilityChallenge) error {
	owner, err := libp2p.New(
		libp2p.Identity(c.key), libp2p.NoListenAddrs, libp2p.NoTransports,
		libp2p.Transport(tcptransport.NewTCPTransport), libp2p.DisableRelay(),
	)
	if err != nil {
		return err
	}
	defer owner.Close()
	if err := owner.Connect(ctx, target); err != nil {
		return err
	}
	stream, err := owner.NewStream(ctx, target.ID, reachability.ChallengeProtocolID)
	if err != nil {
		return err
	}
	defer stream.Close()
	if err := testReachabilityWrite(stream, challenge); err != nil {
		return err
	}
	response := new(irviov1.ReachabilityChallengeResponse)
	if err := testReachabilityRead(stream, response); err != nil {
		return err
	}
	if !bytes.Equal(response.AttemptId, challenge.AttemptId) || !bytes.Equal(response.Nonce, challenge.Nonce) {
		return reachability.ErrIdentityMismatch
	}
	return nil
}

func testReachabilityWrite(writer io.Writer, message proto.Message) error {
	payload, err := proto.Marshal(message)
	if err != nil {
		return err
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err = writer.Write(payload)
	return err
}

func testReachabilityRead(reader io.Reader, message proto.Message) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	payload := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return err
	}
	return proto.Unmarshal(payload, message)
}

func TestPublicRolePublishesOnlyQuorumVerifiedEndpointAndDisableClearsIt(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	start := func() *discovery.Node {
		node, err := discovery.Start(context.Background(), discovery.Config{
			DataDir: t.TempDir(), ListenPort: 0, DHTMode: "server", Logger: logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = node.Close() })
		return node
	}
	subject, first, second := start(), start(), start()
	base := PublicRoleConfig{
		OperationTimeout: 5 * time.Second, ProofTTL: 30 * time.Second,
		ReverifyLead: 10 * time.Second, RetryDelay: 10 * time.Second,
		AllowTestAddresses: true, Logger: logger,
	}
	base.Challenger = testInboundChallenge{key: first.IdentityPrivateKey()}
	firstRole, err := newPublicRole(context.Background(), first, base)
	if err != nil {
		t.Fatal(err)
	}
	defer firstRole.Close()
	base.Challenger = testInboundChallenge{key: second.IdentityPrivateKey()}
	secondRole, err := newPublicRole(context.Background(), second, base)
	if err != nil {
		t.Fatal(err)
	}
	defer secondRole.Close()

	base.Requested = true
	base.Challenger = testInboundChallenge{key: subject.IdentityPrivateKey()}
	expectedPort := externalPort(subject, irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4)
	address4 := func(node *discovery.Node) string {
		for _, address := range node.ListenAddresses() {
			if strings.Contains(address, "/ip4/") {
				return address
			}
		}
		t.Fatal("node has no IPv4 listener")
		return ""
	}
	_, unavailablePublicKey, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	unavailablePeer, err := peer.IDFromPublicKey(unavailablePublicKey)
	if err != nil {
		t.Fatal(err)
	}
	base.VerifierAddresses = []string{
		address4(first), address4(second),
		fmt.Sprintf("/ip4/127.0.0.1/tcp/1/p2p/%s", unavailablePeer),
	}
	role, err := newPublicRole(context.Background(), subject, base)
	if err != nil {
		t.Fatal(err)
	}
	defer role.Close()
	if role.config.ExternalPortIPv4 != expectedPort || role.config.ExternalPortIPv6 != 0 {
		t.Fatalf("automatic family ports: IPv4=%d IPv6=%d", role.config.ExternalPortIPv4, role.config.ExternalPortIPv6)
	}
	state := networkstate.State{Generation: 7, ObservedAt: time.Now()}
	if err := role.Enable(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	status := role.Status()
	if status.IPv4.State != reachability.StateVerified || status.IPv4.Proof.Endpoint.NetworkGeneration != 7 ||
		status.IPv4.Proof.Endpoint.Address.Port() != expectedPort {
		t.Fatalf("unexpected public-role status: %+v", status)
	}
	announcement := subject.LocalAnnouncement()
	if announcement.Capabilities&protocol.CapPublicControlV4 == 0 || len(announcement.PublicControlEndpoints) != 1 {
		t.Fatalf("verified capability was not signed into announcement: %+v", announcement)
	}
	if err := protocol.ValidateAnnouncement(announcement, subject.PeerID(), time.Now()); err != nil {
		t.Fatalf("published announcement is invalid: %v", err)
	}
	network := &fakeNetwork{changes: make(chan networkstate.State, 1), errors: make(chan error, 1)}
	if err := role.WatchNetwork(network); err != nil {
		t.Fatal(err)
	}
	network.changes <- networkstate.State{Generation: 8, ObservedAt: time.Now()}
	controller := role.controllers[irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4]
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for role.Status().IPv4.Proof.Endpoint.NetworkGeneration != 8 {
		select {
		case <-deadline.Done():
			t.Fatalf("standalone public-role watcher did not reverify generation 8: %+v", role.Status())
		case <-controller.Events():
		}
	}
	if err := role.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	announcement = subject.LocalAnnouncement()
	if announcement.Capabilities&(protocol.CapPublicControlV4|protocol.CapPublicControlV6) != 0 ||
		len(announcement.PublicControlEndpoints) != 0 {
		t.Fatalf("disabled role remained advertised: %+v", announcement)
	}
}
