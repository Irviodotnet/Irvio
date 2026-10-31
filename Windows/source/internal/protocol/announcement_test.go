// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package protocol

import (
	"bytes"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/record"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func announcementFixture(t *testing.T, now time.Time) (crypto.PrivKey, peer.ID, *irviov1.NodeAnnouncement, []byte) {
	t.Helper()
	privateKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	peerID, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	announcement := &irviov1.NodeAnnouncement{
		ProtocolVersion: ProtocolVersion,
		NodeId:          []byte(peerID),
		SessionEpoch:    bytes.Repeat([]byte{0x42}, SessionEpochSize),
		Sequence:        1,
		Capabilities:    CapDiscovery | CapExit,
		Libp2PAddrs:     []string{"/ip4/127.0.0.1/tcp/41001", "/ip6/::1/tcp/41001"},
		Incarnation:     1,
		Country:         "DE",
		CreatedAtUnix:   now.Add(-time.Minute).Unix(),
		ExpiresAtUnix:   now.Add(time.Hour).Unix(),
	}
	signed, err := SealAnnouncement(privateKey, announcement, now)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, peerID, announcement, signed
}

func TestValidEnvelopeAccepted(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	_, peerID, want, signed := announcementFixture(t, now)
	got, err := VerifySignedAnnouncement(signed, peerID, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sequence != want.Sequence || string(got.NodeId) != string(want.NodeId) {
		t.Fatalf("unexpected announcement: %+v", got)
	}
}

func TestModifiedProtobufRejected(t *testing.T) {
	now := time.Now()
	_, peerID, _, signed := announcementFixture(t, now)
	envelope, err := record.UnmarshalEnvelope(signed)
	if err != nil {
		t.Fatal(err)
	}
	envelope.RawPayload[len(envelope.RawPayload)-1] ^= 0x01
	tampered, err := envelope.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySignedAnnouncement(tampered, peerID, now); err == nil {
		t.Fatal("modified protobuf was accepted")
	}
}

func TestModifiedSignatureRejected(t *testing.T) {
	now := time.Now()
	_, peerID, _, signed := announcementFixture(t, now)
	signed[len(signed)-1] ^= 0x01
	if _, err := VerifySignedAnnouncement(signed, peerID, now); err == nil {
		t.Fatal("modified signature was accepted")
	}
}

func TestWrongNodeIDRejected(t *testing.T) {
	now := time.Now()
	privateKey, peerID, announcement, _ := announcementFixture(t, now)
	otherKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := peer.IDFromPrivateKey(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	announcement.NodeId = []byte(otherID)
	envelope, err := record.Seal(&AnnouncementRecord{Announcement: announcement}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := envelope.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySignedAnnouncement(signed, peerID, now); err == nil {
		t.Fatal("wrong NodeID was accepted")
	}
}

func TestProviderSignerMismatchRejected(t *testing.T) {
	now := time.Now()
	_, _, _, signed := announcementFixture(t, now)
	otherKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := peer.IDFromPrivateKey(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySignedAnnouncement(signed, otherID, now); err == nil {
		t.Fatal("provider/signer mismatch was accepted")
	}
}

func TestExpiredAnnouncementRejected(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	privateKey, peerID, announcement, _ := announcementFixture(t, now)
	announcement.CreatedAtUnix = now.Add(-2 * time.Hour).Unix()
	announcement.ExpiresAtUnix = now.Add(-time.Hour).Unix()
	envelope, err := record.Seal(&AnnouncementRecord{Announcement: announcement}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := envelope.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySignedAnnouncement(signed, peerID, now); err == nil {
		t.Fatal("expired announcement was accepted")
	}
}

func TestMalformedUnsupportedAndOversizedRejected(t *testing.T) {
	now := time.Now()
	privateKey, peerID, announcement, _ := announcementFixture(t, now)
	announcement.ProtocolVersion = 99
	envelope, err := record.Seal(&AnnouncementRecord{Announcement: announcement}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := envelope.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string][]byte{
		"malformed":   []byte{0xff, 0xff, 0xff},
		"unsupported": signed,
		"oversized":   make([]byte, MaxSignedEnvelopeSize+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifySignedAnnouncement(input, peerID, now); err == nil {
				t.Fatalf("%s input was accepted", name)
			}
		})
	}
}

func TestOversizedDecodedAnnouncementRejected(t *testing.T) {
	now := time.Now()
	_, peerID, announcement, _ := announcementFixture(t, now)
	announcement.AwgPublicKey = make([]byte, MaxAnnouncementPayloadSize)
	if err := ValidateAnnouncement(announcement, peerID, now); err == nil {
		t.Fatal("oversized decoded announcement was accepted")
	}
}

func TestUnknownCapabilityBitsAllowed(t *testing.T) {
	now := time.Now()
	privateKey, peerID, announcement, _ := announcementFixture(t, now)
	announcement.Capabilities |= uint64(1) << 63
	signed, err := SealAnnouncement(privateKey, announcement, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySignedAnnouncement(signed, peerID, now); err != nil {
		t.Fatal(err)
	}
}

func TestAWGKeyBindingRequiresExactKeyAndEpoch(t *testing.T) {
	now := time.Now()
	_, peerID, announcement, _ := announcementFixture(t, now)
	for name, mutate := range map[string]func(){
		"short-key":     func() { announcement.AwgPublicKey = make([]byte, 31); announcement.AwgKeyEpoch = 1 },
		"missing-epoch": func() { announcement.AwgPublicKey = make([]byte, 32); announcement.AwgKeyEpoch = 0 },
		"missing-key":   func() { announcement.AwgPublicKey = nil; announcement.AwgKeyEpoch = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			announcement.AwgPublicKey = nil
			announcement.AwgKeyEpoch = 0
			mutate()
			if err := ValidateAnnouncement(announcement, peerID, now); err == nil {
				t.Fatal("invalid AWG binding accepted")
			}
		})
	}
	announcement.AwgPublicKey = bytes.Repeat([]byte{0x33}, 32)
	announcement.AwgKeyEpoch = 1
	if err := ValidateAnnouncement(announcement, peerID, now); err != nil {
		t.Fatal(err)
	}
}

func TestPublicControlCapabilityCannotBeSelfAsserted(t *testing.T) {
	now := time.Now()
	_, peerID, announcement, _ := announcementFixture(t, now)
	announcement.Capabilities |= CapPublicControlV4
	if err := ValidateAnnouncement(announcement, peerID, now); err == nil {
		t.Fatal("public control capability without verifier proof was accepted")
	}
	announcement.Capabilities &^= CapPublicControlV4
	announcement.PublicControlEndpoints = []*irviov1.PublicControlEndpoint{{
		Family: irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4,
		Proof:  &irviov1.PublicReachabilityProof{},
	}}
	if err := ValidateAnnouncement(announcement, peerID, now); err == nil {
		t.Fatal("public endpoint without a matching capability and quorum proof was accepted")
	}
}
