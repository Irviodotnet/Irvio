// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awgbinding

import (
	"bytes"
	"errors"
	"testing"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func signedBinding(t *testing.T, signer libp2pcrypto.PrivKey, keyByte byte, keyEpoch, sequence uint64, now time.Time) (peer.ID, []byte) {
	t.Helper()
	id, err := peer.IDFromPrivateKey(signer)
	if err != nil {
		t.Fatal(err)
	}
	announcement := &irviov1.NodeAnnouncement{
		ProtocolVersion: protocol.ProtocolVersion,
		NodeId:          []byte(id), SessionEpoch: bytes.Repeat([]byte{0x44}, protocol.SessionEpochSize),
		Sequence: sequence, Capabilities: protocol.CapDiscovery | protocol.CapExit,
		Incarnation: 1,
		Libp2PAddrs: []string{"/ip4/198.18.0.2/tcp/4001"}, Country: "DE",
		AwgPublicKey: bytes.Repeat([]byte{keyByte}, len(awg.Key{})), AwgKeyEpoch: keyEpoch,
		CreatedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
	}
	envelope, err := protocol.SealAnnouncement(signer, announcement, now)
	if err != nil {
		t.Fatal(err)
	}
	return id, envelope
}

func TestSignedBindingAndRotationFence(t *testing.T) {
	now := time.Now().UTC()
	signer, _, err := libp2pcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, firstEnvelope := signedBinding(t, signer, 7, 7, 1, now)
	first, err := Verify(firstEnvelope, id, now)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.Commit(first, now); err != nil {
		t.Fatal(err)
	}
	oldKey := first.PublicKey

	_, secondEnvelope := signedBinding(t, signer, 8, 8, 2, now)
	second, err := Verify(secondEnvelope, id, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Commit(second, now); err != nil {
		t.Fatal(err)
	}
	if err := registry.AuthorizePromotion(id, oldKey, 7, now); !errors.Is(err, ErrStaleAttempt) {
		t.Fatalf("old key promotion error = %v", err)
	}
	if err := registry.AuthorizePromotion(id, second.PublicKey, 8, now); err != nil {
		t.Fatal(err)
	}
	if err := registry.Commit(first, now); !errors.Is(err, ErrRollback) {
		t.Fatalf("rollback error = %v", err)
	}
}

func TestTamperedAndWrongPeerBindingsRejected(t *testing.T) {
	now := time.Now().UTC()
	signer, _, err := libp2pcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, envelope := signedBinding(t, signer, 9, 1, 1, now)
	tampered := append([]byte(nil), envelope...)
	tampered[len(tampered)-1] ^= 1
	if _, err := Verify(tampered, id, now); err == nil {
		t.Fatal("tampered binding accepted")
	}
	otherPrivate, _, err := libp2pcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := peer.IDFromPrivateKey(otherPrivate)
	if _, err := Verify(envelope, other, now); err == nil {
		t.Fatal("binding accepted for the wrong Irvio PeerID")
	}
}
func TestRegistryUsesDurableAnnouncementVersionAcrossProcessEpochs(t *testing.T) {
	now := time.Now().UTC()
	signer, _, err := libp2pcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, envelope := signedBinding(t, signer, 7, 4, 8, now)
	current, err := Verify(envelope, id, now)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.Commit(current, now); err != nil {
		t.Fatal(err)
	}

	restarted := current
	restarted.AnnouncementEpoch[0] ^= 0xff
	restarted.AnnouncementIncarnation++
	restarted.AnnouncementSequence = 1
	restarted.DescriptorID[0] ^= 0xff
	if err := registry.Commit(restarted, now); err != nil {
		t.Fatalf("new incarnation with reset sequence rejected: %v", err)
	}

	stale := current
	stale.AnnouncementEpoch[0] ^= 0x7f
	if err := registry.Commit(stale, now); !errors.Is(err, ErrRollback) {
		t.Fatalf("old incarnation error=%v", err)
	}

	conflict := restarted
	conflict.DescriptorID[1] ^= 0xff
	if err := registry.Commit(conflict, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-version payload conflict error=%v", err)
	}
}
