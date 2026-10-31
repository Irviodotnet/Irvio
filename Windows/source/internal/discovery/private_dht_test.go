// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/reachability"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
)

func privateDHTNode(t *testing.T, bootstrap ...string) *Node {
	t.Helper()
	node, err := Start(context.Background(), Config{DataDir: discoveryTestDir(t), ListenPort: 0, DHTMode: "server", Bootstrap: bootstrap, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { node.Close() })
	return node
}

func privateTestProof(t *testing.T, subject peer.ID) reachability.Proof {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	var attestations []*irviov1.SignedReachabilityAttestation
	for i := 0; i < 2; i++ {
		key, _, err := crypto.GenerateEd25519Key(nil)
		if err != nil {
			t.Fatal(err)
		}
		verifier, err := peer.IDFromPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		value := &irviov1.ReachabilityAttestation{ProtocolVersion: reachability.ProtocolVersion, SubjectPeerId: []byte(subject), VerifierPeerId: []byte(verifier), Family: irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4, ObservedIp: netip.MustParseAddr("203.0.113.7").AsSlice(), ExternalControlPort: 4000, NetworkGeneration: 1, PublicEpoch: 1, AttemptId: bytes.Repeat([]byte{byte(i + 1)}, reachability.AttemptIDSize), VerifiedAtUnix: now.Unix(), ExpiresAtUnix: now.Add(time.Minute).Unix()}
		payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		signature, err := key.Sign(append([]byte("hydra/reachability-attestation/1.0.0\x00"), payload...))
		if err != nil {
			t.Fatal(err)
		}
		public, err := crypto.MarshalPublicKey(key.GetPublic())
		if err != nil {
			t.Fatal(err)
		}
		attestations = append(attestations, &irviov1.SignedReachabilityAttestation{Attestation: payload, Signature: signature, VerifierPublicKey: public})
	}
	proof, err := reachability.BuildProof(subject, attestations, reachability.DefaultProofPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func TestPrivateOwnersRealDHTAcrossBootstrapAndWithdrawal(t *testing.T) {
	bootstrap := privateDHTNode(t)
	var address string
	for _, value := range bootstrap.Host().Addrs() {
		if strings.HasPrefix(value.String(), "/ip4/127.0.0.1/tcp/") {
			address = value.String() + "/p2p/" + bootstrap.PeerID().String()
			break
		}
	}
	if address == "" {
		t.Fatal("missing bootstrap loopback address")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	locator := [32]byte{17}
	owners := []*Node{privateDHTNode(t, address), privateDHTNode(t, address)}
	for _, owner := range owners {
		if err := owner.SetPrivateRendezvousReady(true); err == nil {
			t.Fatal("private readiness bypassed M11")
		}
		if err := owner.UpdatePublicReachability([]reachability.Proof{privateTestProof(t, owner.PeerID())}); err != nil {
			t.Fatal(err)
		}
		if err := owner.ActivateRendezvousCapability(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if err := owner.SetPrivateRendezvousReady(true); err != nil {
			t.Fatal(err)
		}
		if err := owner.ProvidePrivateOwner(ctx, locator); err != nil {
			t.Fatal(err)
		}
	}
	client := privateDHTNode(t, address)
	if len(client.bootstrapPeers) != 1 || client.bootstrapPeers[0].ID != bootstrap.PeerID() {
		t.Fatal("client has hidden bootstrap owners")
	}
	result, err := client.FindPrivateOwners(ctx, locator)
	if err != nil || len(result) != 2 || !slices.Contains(result, owners[0].PeerID()) || !slices.Contains(result, owners[1].PeerID()) {
		t.Fatalf("cross-bootstrap discovery: %v %v", result, err)
	}
	if err := owners[0].SetPrivateRendezvousReady(false); err != nil {
		t.Fatal(err)
	}
	if err := owners[0].ProvidePrivateOwner(ctx, locator); err == nil {
		t.Fatal("withdrawn owner could refresh private provider record")
	}
	result, err = client.FindPrivateOwners(ctx, locator)
	if err != nil || len(result) != 1 || result[0] != owners[1].PeerID() {
		t.Fatalf("withdrawn owner survived stale provider record: %v %v", result, err)
	}
	if err := owners[1].UpdatePublicReachability(nil); err != nil {
		t.Fatal(err)
	}
	if containsFeature(owners[1].LocalAnnouncement().SessionFeatures, PrivateRendezvousFeature) {
		t.Fatal("private feature survived proof withdrawal")
	}
	result, err = client.FindPrivateOwners(ctx, locator)
	if len(result) != 0 || !errors.Is(err, ErrPrivateLookupIncomplete) {
		t.Fatalf("unvalidated owners accepted: %v %v", result, err)
	}
}
