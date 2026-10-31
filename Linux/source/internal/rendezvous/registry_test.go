// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type fixedCountryResolver struct {
	country string
	err     error
}

func (r fixedCountryResolver) Resolve(_ context.Context, address netip.Addr) (CountryObservation, error) {
	state := CountryObserved
	if r.country == "" {
		state = CountryUnknown
	}
	return CountryObservation{
		State: state, Country: r.country, ObservedIP: address, Family: addressFamily(address),
		DatabaseType: "GeoIP2-Country-fixture", DatabaseBuild: time.Unix(100, 0), ObservedAt: time.Unix(200, 0),
	}, r.err
}
func (r fixedCountryResolver) Snapshot() CountryResolverSnapshot {
	return CountryResolverSnapshot{DatabaseType: "GeoIP2-Country-fixture"}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) { clear(buffer); return len(buffer), nil }

func testIdentity(t *testing.T) (crypto.PrivKey, peer.ID) {
	t.Helper()
	privateKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	peerID, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, peerID
}

func testSignedExit(t *testing.T, key crypto.PrivKey, peerID peer.ID, now time.Time, incarnation, sequence uint64, accepting bool) []byte {
	return testSignedExitLifetime(t, key, peerID, now, incarnation, sequence, accepting, 6*time.Minute)
}

func testSignedExitLifetime(t *testing.T, key crypto.PrivKey, peerID peer.ID, now time.Time, incarnation, sequence uint64, accepting bool, lifetime time.Duration) []byte {
	t.Helper()
	message := &irviov1.NodeAnnouncement{
		ProtocolVersion: protocol.ProtocolVersion, NodeId: []byte(peerID),
		SessionEpoch: bytes.Repeat([]byte{0x33}, protocol.SessionEpochSize),
		Incarnation:  incarnation, Sequence: sequence,
		Capabilities: protocol.CapDiscovery | protocol.CapExit, Country: "DE", EgressIpv4: true,
		AwgPublicKey: bytes.Repeat([]byte{0x44}, 32), AwgKeyEpoch: 1,
		CreatedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(-time.Minute).Add(lifetime).Unix(),
		AcceptingNewSessions: accepting, SessionFeatures: []string{"session-establish-v1", "session-control-v1"},
	}
	signed, err := protocol.SealAnnouncement(key, message, now)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func testSignedCapabilities(t *testing.T, key crypto.PrivKey, peerID peer.ID, now time.Time, incarnation, sequence uint64, accepting bool, capabilities uint64) []byte {
	t.Helper()
	message := &irviov1.NodeAnnouncement{
		ProtocolVersion: protocol.ProtocolVersion, NodeId: []byte(peerID),
		SessionEpoch: bytes.Repeat([]byte{0x33}, protocol.SessionEpochSize),
		Incarnation:  incarnation, Sequence: sequence, Capabilities: capabilities,
		Country: "DE", EgressIpv4: true, AwgPublicKey: bytes.Repeat([]byte{0x44}, 32),
		AwgKeyEpoch: 1, CreatedAtUnix: now.Add(-time.Minute).Unix(),
		ExpiresAtUnix: now.Add(5 * time.Minute).Unix(), AcceptingNewSessions: accepting,
		SessionFeatures: []string{"session-establish-v1", "session-control-v1"},
	}
	signed, err := protocol.SealAnnouncement(key, message, now)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestRegistryRejectsDescriptorLifetimeBeyondPolicy(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	registry, _ := testRegistry(t, &now, fixedCountryResolver{country: "DE"})
	exitKey, exitPeer := testIdentity(t)
	_, err := registry.Register(context.Background(), exitPeer, RegistrationID{1}, ChannelToken{1},
		testSignedExitLifetime(t, exitKey, exitPeer, now, 1, 1, true, 10*time.Minute+time.Second),
		netip.MustParseAddr("198.51.100.10"))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("overlong descriptor error=%v", err)
	}
}

func testRegistry(t *testing.T, now *time.Time, resolver CountryResolver) (*Registry, peer.ID) {
	t.Helper()
	serverKey, serverPeer := testIdentity(t)
	config := DefaultConfig()
	config.HeartbeatInterval, config.RegistrationStale, config.RegistrationExpiry = time.Second, 3*time.Second, 5*time.Second
	config.ObservedIPPolicy.AllowDocumentation = true
	config.Now = func() time.Time { return *now }
	registry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: serverPeer, ServerIncarnation: 7, Signer: serverKey, Resolver: resolver,
		ControlAddresses:  []string{"/ip4/203.0.113.7/tcp/4001/p2p/" + serverPeer.String()},
		ServerPublicProof: []byte("bounded-proof"), Random: zeroReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry, serverPeer
}

func TestRegistrationReplacementFencesLateOwnerEvents(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	registry, serverPeer := testRegistry(t, &now, fixedCountryResolver{country: "DE"})
	exitKey, exitPeer := testIdentity(t)
	observed := netip.MustParseAddr("198.51.100.21")
	oldID, _ := NewRegistrationID(nil)
	oldChannel, _ := NewChannelToken(nil)
	old, err := registry.Register(context.Background(), exitPeer, oldID, oldChannel, testSignedExit(t, exitKey, exitPeer, now, 3, 1, true), observed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyReceipt(old.SignedReceipt.SignedEnvelope, serverPeer, now); err != nil {
		t.Fatalf("verify receipt: %v", err)
	}

	newID, _ := NewRegistrationID(nil)
	newChannel, _ := NewChannelToken(nil)
	current, err := registry.Register(context.Background(), exitPeer, newID, newChannel, testSignedExit(t, exitKey, exitPeer, now, 3, 1, true), observed)
	if err != nil {
		t.Fatal(err)
	}
	if current.Owner.RegistrationID != newID {
		t.Fatal("replacement did not become current")
	}
	if _, err := registry.Heartbeat(old.Owner, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late heartbeat: %v", err)
	}
	if err := registry.Unregister(old.Owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late unregister: %v", err)
	}
	if registry.ChannelClosed(old.Owner) {
		t.Fatal("late close removed replacement")
	}
	if _, ok := registry.Lookup(exitPeer); !ok {
		t.Fatal("replacement disappeared")
	}
	if got, err := registry.Sample("DE", 10, nil); err != nil || len(got) != 1 {
		t.Fatalf("sample = %d, %v", len(got), err)
	}
}

func TestRegistrationStaleReviveAndExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	registry, _ := testRegistry(t, &now, fixedCountryResolver{country: "DE"})
	exitKey, exitPeer := testIdentity(t)
	id, _ := NewRegistrationID(nil)
	channel, _ := NewChannelToken(nil)
	registration, err := registry.Register(context.Background(), exitPeer, id, channel, testSignedExit(t, exitKey, exitPeer, now, 1, 1, true), netip.MustParseAddr("198.51.100.10"))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Second)
	if got, err := registry.Sample("DE", 1, nil); err != nil || len(got) != 0 {
		t.Fatalf("stale sampled: %d, %v", len(got), err)
	}
	if snapshot := registry.Snapshot(); snapshot.Stale != 1 {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	if _, err := registry.Heartbeat(registration.Owner, 1); err != nil {
		t.Fatalf("revive: %v", err)
	}
	if _, err := registry.Heartbeat(registration.Owner, 1); !errors.Is(err, ErrStale) {
		t.Fatalf("duplicate heartbeat: %v", err)
	}
	now = now.Add(6 * time.Second)
	if _, ok := registry.Lookup(exitPeer); ok {
		t.Fatal("expired registration remained")
	}
}

func TestDescriptorConflictAndUnknownCountry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	registry, _ := testRegistry(t, &now, fixedCountryResolver{err: ErrCountryDegraded})
	exitKey, exitPeer := testIdentity(t)
	id, _ := NewRegistrationID(nil)
	channel, _ := NewChannelToken(nil)
	registration, err := registry.Register(context.Background(), exitPeer, id, channel, testSignedExit(t, exitKey, exitPeer, now, 2, 4, true), netip.MustParseAddr("198.51.100.10"))
	if err != nil {
		t.Fatal(err)
	}
	if registration.Country.State != CountryUnknown {
		t.Fatalf("country state = %v", registration.Country.State)
	}
	if got, err := registry.Sample("DE", 1, nil); err != nil || len(got) != 0 {
		t.Fatalf("unknown sampled: %d, %v", len(got), err)
	}
	conflicting := testSignedExit(t, exitKey, exitPeer, now, 2, 4, false)
	if _, err := registry.Update(context.Background(), registration.Owner, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	newer := testSignedExit(t, exitKey, exitPeer, now, 2, 5, false)
	updated, err := registry.Update(context.Background(), registration.Owner, newer)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Descriptor.Version.Sequence != 5 || updated.SignedReceipt.Receipt.Sequence != 2 {
		t.Fatalf("update: %+v", updated)
	}
}
