// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type bindingDirectory struct{ envelope []byte }

func (d *bindingDirectory) SignedAnnouncement(context.Context, string) ([]byte, error) {
	return append([]byte(nil), d.envelope...), nil
}

func signedBinding(t *testing.T, private crypto.PrivKey, id peer.ID, epoch uint64, key byte, now time.Time) []byte {
	t.Helper()
	public := make([]byte, 32)
	for index := range public {
		public[index] = key + byte(index)
	}
	sessionEpoch := make([]byte, 16)
	for index := range sessionEpoch {
		sessionEpoch[index] = byte(index + 1)
	}
	message := &irviov1.NodeAnnouncement{
		ProtocolVersion: protocol.ProtocolVersion, NodeId: []byte(id),
		SessionEpoch: sessionEpoch, Sequence: epoch, Capabilities: protocol.CapExit,
		Libp2PAddrs: []string{"/ip4/198.51.100.10/tcp/4001"},
		Incarnation: 1,
		Country:     "DE", AwgPublicKey: public, AwgKeyEpoch: epoch,
		CreatedAtUnix: now.Add(-time.Second).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
	}
	envelope, err := protocol.SealAnnouncement(private, message, now)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}
func signedBindingVersion(t *testing.T, private crypto.PrivKey, id peer.ID, incarnation, sequence, keyEpoch uint64, key byte, now time.Time) []byte {
	t.Helper()
	public := make([]byte, 32)
	for index := range public {
		public[index] = key + byte(index)
	}
	sessionEpoch := make([]byte, 16)
	for index := range sessionEpoch {
		sessionEpoch[index] = byte(index + 1)
	}
	message := &irviov1.NodeAnnouncement{
		ProtocolVersion: protocol.ProtocolVersion, NodeId: []byte(id),
		SessionEpoch: sessionEpoch, Sequence: sequence, Capabilities: protocol.CapExit,
		Libp2PAddrs: []string{"/ip4/198.51.100.10/tcp/4001"},
		Incarnation: incarnation,
		Country:     "DE", AwgPublicKey: public, AwgKeyEpoch: keyEpoch,
		CreatedAtUnix: now.Add(-time.Second).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
	}
	envelope, err := protocol.SealAnnouncement(private, message, now)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestBindingResolverVerifiesStaleLocatorWithoutRollingBackPromotionFence(t *testing.T) {
	private, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	registry := awgbinding.NewRegistry()
	resolver := BindingResolver{Registry: registry, Now: func() time.Time { return now }}
	currentSigned := signedBindingVersion(t, private, id, 3, 8, 4, 7, now)
	current, err := resolver.ResolveSigned(currentSigned, id)
	if err != nil {
		t.Fatal(err)
	}
	locatorSigned := signedBindingVersion(t, private, id, 3, 7, 4, 7, now)
	locator, err := resolver.VerifyLocator(locatorSigned, id)
	if err != nil {
		t.Fatalf("verify stale candidate locator: %v", err)
	}
	if locator.Key != current.Key || locator.Binding.KeyEpoch != current.Binding.KeyEpoch {
		t.Fatal("locator did not retain the authenticated AWG binding")
	}
	stored, ok := registry.Current(id)
	if !ok || stored.AnnouncementIncarnation != 3 || stored.AnnouncementSequence != 8 {
		t.Fatalf("locator changed promotion fence: %+v", stored)
	}
	if _, err := resolver.ResolveSigned(locatorSigned, id); !errors.Is(err, awgbinding.ErrRollback) {
		t.Fatalf("authoritative stale descriptor error=%v", err)
	}
}

func TestBindingResolverFencesLateOldKeyPromotion(t *testing.T) {
	private, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	directory := &bindingDirectory{envelope: signedBinding(t, private, id, 7, 7, now)}
	resolver := BindingResolver{Directory: directory, Registry: awgbinding.NewRegistry(), Now: func() time.Time { return now }}
	old, err := resolver.Resolve(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	directory.envelope = signedBinding(t, private, id, 8, 8, now)
	current, err := resolver.Resolve(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolver.AuthorizePromotion(old); err == nil {
		t.Fatal("old key epoch won a late promotion race")
	}
	if err := resolver.AuthorizePromotion(current); err != nil {
		t.Fatal(err)
	}
	if old.Key == current.Key || current.Binding.KeyEpoch != 8 {
		t.Fatal("rotation did not produce a distinct authenticated AWG key")
	}
}

func TestBindingResolverRefreshesExpiredAuthenticatedAnnouncement(t *testing.T) {
	private, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Unix(10_000, 0).UTC()
	now := issued.Add(2 * time.Hour)
	directory := &bindingDirectory{envelope: signedBinding(t, private, id, 1, 1, issued)}
	refreshes := 0
	resolver := BindingResolver{
		Directory: directory, Registry: awgbinding.NewRegistry(), Now: func() time.Time { return now },
		Refresh: func(_ context.Context, remote peer.ID) error {
			if remote != id {
				t.Fatalf("refresh peer=%s want=%s", remote, id)
			}
			refreshes++
			directory.envelope = signedBinding(t, private, id, 2, 2, now)
			return nil
		},
	}
	binding, err := resolver.Resolve(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 || binding.Binding.KeyEpoch != 2 {
		t.Fatalf("refreshes=%d binding_epoch=%d", refreshes, binding.Binding.KeyEpoch)
	}
}

func TestBindingResolverRefreshesNewerAuthenticatedKeyBeforeReconnect(t *testing.T) {
	private, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	directory := &bindingDirectory{envelope: signedBinding(t, private, id, 7, 7, now)}
	resolver := BindingResolver{
		Directory: directory, Registry: awgbinding.NewRegistry(), Now: func() time.Time { return now },
		Refresh: func(context.Context, peer.ID) error {
			directory.envelope = signedBinding(t, private, id, 8, 8, now)
			return nil
		},
	}
	current, err := resolver.Resolve(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if current.Binding.KeyEpoch != 8 {
		t.Fatalf("reconnect used cached key epoch %d after authenticated rotation", current.Binding.KeyEpoch)
	}
}
