// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package reachability

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	ma "github.com/multiformats/go-multiaddr"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type testChallenger struct {
	key   crypto.PrivKey
	calls atomic.Int32
}

func (c *testChallenger) Challenge(ctx context.Context, target peer.AddrInfo, challenge *irviov1.ReachabilityChallenge) error {
	c.calls.Add(1)
	owner, err := newTestHost(c.key, false)
	if err != nil {
		return err
	}
	defer owner.Close()
	if err := owner.Connect(ctx, target); err != nil {
		return ErrUnreachable
	}
	if len(owner.Network().ConnsToPeer(target.ID)) != 1 {
		return ErrIdentityMismatch
	}
	stream, err := owner.NewStream(ctx, target.ID, ChallengeProtocolID)
	if err != nil {
		return ErrUnreachable
	}
	defer stream.Close()
	if err := writeFrame(stream, challenge); err != nil {
		return err
	}
	response := new(irviov1.ReachabilityChallengeResponse)
	if err := readFrame(stream, response); err != nil {
		return ErrUnreachable
	}
	if !bytes.Equal(response.AttemptId, challenge.AttemptId) || !bytes.Equal(response.Nonce, challenge.Nonce) {
		return ErrIdentityMismatch
	}
	return nil
}

func newTestHost(key crypto.PrivKey, listen bool) (host.Host, error) {
	return newTestHostAddress(key, listen, "/ip4/127.0.0.1/tcp/0")
}

func newTestHostAddress(key crypto.PrivKey, listen bool, address string) (host.Host, error) {
	options := []libp2p.Option{
		libp2p.Identity(key), libp2p.NoTransports,
		libp2p.Transport(tcptransport.NewTCPTransport), libp2p.DisableRelay(),
	}
	if listen {
		options = append(options, libp2p.ListenAddrStrings(address))
	} else {
		options = append(options, libp2p.NoListenAddrs)
	}
	return libp2p.New(options...)
}

func newTestHostAddresses(key crypto.PrivKey, addresses ...string) (host.Host, error) {
	return libp2p.New(
		libp2p.Identity(key), libp2p.NoTransports,
		libp2p.Transport(tcptransport.NewTCPTransport), libp2p.DisableRelay(),
		libp2p.ListenAddrStrings(addresses...),
	)
}

func TestFamilyRequesterPinsIPv6DespiteExistingIPv4Connection(t *testing.T) {
	subjectKey, _, _ := crypto.GenerateEd25519Key(nil)
	subject, err := newTestHostAddresses(subjectKey, "/ip4/127.0.0.1/tcp/0", "/ip6/::1/tcp/0")
	if err != nil {
		t.Fatal(err)
	}
	defer subject.Close()
	verifierKey, _, _ := crypto.GenerateEd25519Key(nil)
	verifier, err := newTestHostAddresses(verifierKey, "/ip4/127.0.0.1/tcp/0", "/ip6/::1/tcp/0")
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	service, err := NewService(ServiceConfig{
		Host: verifier, PrivateKey: verifierKey, Challenger: &testChallenger{key: verifierKey},
		ObservedIP: ObservedIPPolicy{AllowLoopback: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	var ipv4 []ma.Multiaddr
	for _, address := range verifier.Addrs() {
		if _, err := address.ValueForProtocol(ma.P_IP4); err == nil {
			ipv4 = append(ipv4, address)
		}
	}
	connectHosts(t, subject, &hostWithAddresses{Host: verifier, addresses: ipv4})
	requester, err := NewFamilyRequester(subjectKey, 0, []peer.AddrInfo{{ID: verifier.ID(), Addrs: verifier.Addrs()}})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientConfig{Host: subject, Requester: requester})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	signed, err := client.Request(ctx, verifier.ID(), irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6,
		tcpPortFamily(t, subject, ma.P_IP6), 3, 4)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	value, err := VerifyAttestation(signed, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	observed, _ := netip.AddrFromSlice(value.ObservedIp)
	if observed != netip.IPv6Loopback() {
		t.Fatalf("observed family endpoint=%s", observed)
	}
}

type hostWithAddresses struct {
	host.Host
	addresses []ma.Multiaddr
}

func (h *hostWithAddresses) Addrs() []ma.Multiaddr { return h.addresses }

func TestFreshInboundAuthenticatedIPv6Verification(t *testing.T) {
	subjectKey, _, _ := crypto.GenerateEd25519Key(nil)
	subject, err := newTestHostAddress(subjectKey, true, "/ip6/::1/tcp/0")
	if err != nil {
		t.Fatal(err)
	}
	defer subject.Close()
	client, err := NewClient(ClientConfig{Host: subject})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var attestations []*irviov1.SignedReachabilityAttestation
	for index := 0; index < 2; index++ {
		key, _, _ := crypto.GenerateEd25519Key(nil)
		verifier, err := newTestHostAddress(key, true, "/ip6/::1/tcp/0")
		if err != nil {
			t.Fatal(err)
		}
		defer verifier.Close()
		service, err := NewService(ServiceConfig{
			Host: verifier, PrivateKey: key, Challenger: &testChallenger{key: key},
			ObservedIP: ObservedIPPolicy{AllowLoopback: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		connectHosts(t, subject, verifier)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		attestation, err := client.Request(ctx, verifier.ID(), irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6,
			tcpPort(t, subject), 8, 10)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		attestations = append(attestations, attestation)
	}
	proof, err := BuildProof(subject.ID(), attestations, DefaultProofPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if proof.Endpoint.Family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6 ||
		proof.Endpoint.Address.Addr() != netip.IPv6Loopback() {
		t.Fatalf("IPv6 proof=%+v", proof)
	}
}

func keyAndHost(t *testing.T) (crypto.PrivKey, host.Host) {
	t.Helper()
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := newTestHost(key, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	return key, owner
}

func connectHosts(t *testing.T, from, to host.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := from.Connect(ctx, peer.AddrInfo{ID: to.ID(), Addrs: to.Addrs()}); err != nil {
		t.Fatal(err)
	}
}

func tcpPort(t *testing.T, owner host.Host) uint16 {
	t.Helper()
	for _, address := range owner.Addrs() {
		value, err := address.ValueForProtocol(ma.P_TCP)
		if err != nil {
			continue
		}
		port, err := strconv.ParseUint(value, 10, 16)
		if err == nil {
			return uint16(port)
		}
	}
	t.Fatal("host has no TCP address")
	return 0
}

func tcpPortFamily(t *testing.T, owner host.Host, family int) uint16 {
	t.Helper()
	for _, address := range owner.Addrs() {
		if _, err := address.ValueForProtocol(family); err != nil {
			continue
		}
		value, err := address.ValueForProtocol(ma.P_TCP)
		if err != nil {
			continue
		}
		port, err := strconv.ParseUint(value, 10, 16)
		if err == nil {
			return uint16(port)
		}
	}
	t.Fatal("host has no TCP address for requested family")
	return 0
}

func TestFreshInboundAuthenticatedVerificationAndQuorum(t *testing.T) {
	_, subject := keyAndHost(t)
	client, err := NewClient(ClientConfig{Host: subject})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var attestations []*irviov1.SignedReachabilityAttestation
	for index := 0; index < 2; index++ {
		key, verifier := keyAndHost(t)
		challenger := &testChallenger{key: key}
		service, err := NewService(ServiceConfig{
			Host: verifier, PrivateKey: key, Challenger: challenger,
			ObservedIP: ObservedIPPolicy{AllowLoopback: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = service.Close() })
		connectHosts(t, subject, verifier)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		signed, err := client.Request(ctx, verifier.ID(), irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4, tcpPort(t, subject), 7, 9)
		cancel()
		if err != nil {
			t.Fatalf("verifier %d: %v", index, err)
		}
		if challenger.calls.Load() != 1 {
			t.Fatalf("fresh inbound calls=%d", challenger.calls.Load())
		}
		attestations = append(attestations, signed)
	}
	proof, err := BuildProof(subject.ID(), attestations, DefaultProofPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if proof.Subject != subject.ID() || proof.Endpoint.NetworkGeneration != 7 || proof.Endpoint.PublicEpoch != 9 || len(proof.Attestations) != 2 {
		t.Fatalf("proof=%+v", proof)
	}
}

func TestProofRejectsDuplicateVerifierTamperingAndGenerationMix(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	subjectKey, _, _ := crypto.GenerateEd25519Key(nil)
	subject, _ := peer.IDFromPrivateKey(subjectKey)
	verifierKey, _, _ := crypto.GenerateEd25519Key(nil)
	verifier, _ := peer.IDFromPrivateKey(verifierKey)
	makeSigned := func(generation uint64) *irviov1.SignedReachabilityAttestation {
		value, err := signAttestation(verifierKey, &irviov1.ReachabilityAttestation{
			ProtocolVersion: ProtocolVersion, SubjectPeerId: []byte(subject), VerifierPeerId: []byte(verifier),
			Family: irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4, ObservedIp: netip.MustParseAddr("203.0.113.8").AsSlice(),
			ExternalControlPort: 45001, NetworkGeneration: generation, PublicEpoch: 2,
			AttemptId:      bytes.Repeat([]byte{byte(generation)}, AttemptIDSize),
			VerifiedAtUnix: now.Unix(), ExpiresAtUnix: now.Add(time.Minute).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	policy := ProofPolicy{Quorum: 2, Now: func() time.Time { return now }}
	if _, err := BuildProof(subject, []*irviov1.SignedReachabilityAttestation{makeSigned(1), makeSigned(1)}, policy); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("duplicate verifier error=%v", err)
	}
	secondKey, _, _ := crypto.GenerateEd25519Key(nil)
	second, _ := peer.IDFromPrivateKey(secondKey)
	secondSigned, _ := signAttestation(secondKey, &irviov1.ReachabilityAttestation{
		ProtocolVersion: ProtocolVersion, SubjectPeerId: []byte(subject), VerifierPeerId: []byte(second),
		Family: irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4, ObservedIp: netip.MustParseAddr("203.0.113.8").AsSlice(),
		ExternalControlPort: 45001, NetworkGeneration: 2, PublicEpoch: 2,
		AttemptId: bytes.Repeat([]byte{3}, AttemptIDSize), VerifiedAtUnix: now.Unix(), ExpiresAtUnix: now.Add(time.Minute).Unix(),
	})
	if _, err := BuildProof(subject, []*irviov1.SignedReachabilityAttestation{makeSigned(1), secondSigned}, policy); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("mixed generation error=%v", err)
	}
	tampered := makeSigned(1)
	tampered.Attestation[len(tampered.Attestation)-1] ^= 1
	if _, err := VerifyAttestation(tampered, now); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("tampered signature error=%v", err)
	}
}

func TestObservedIPProductionPolicyRejectsPrivateAndDocumentation(t *testing.T) {
	for _, value := range []string{
		"0.1.2.3", "127.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1", "198.18.0.1",
		"240.0.0.1", "100::1", "2001:2::1", "2001:db8::1", "fe80::1",
	} {
		if err := validateObservedIP(netip.MustParseAddr(value), ObservedIPPolicy{}); err == nil {
			t.Fatalf("production policy accepted %s", value)
		}
	}
	if err := validateObservedIP(netip.MustParseAddr("203.0.113.9"), ObservedIPPolicy{AllowDocumentation: true}); err != nil {
		t.Fatal(err)
	}
}

func TestWrongAdvertisedPortCannotProduceAttestation(t *testing.T) {
	verifierKey, verifier := keyAndHost(t)
	_, subject := keyAndHost(t)
	client, _ := NewClient(ClientConfig{Host: subject})
	defer client.Close()
	service, _ := NewService(ServiceConfig{
		Host: verifier, PrivateKey: verifierKey, Challenger: &testChallenger{key: verifierKey},
		ObservedIP: ObservedIPPolicy{AllowLoopback: true}, DialTimeout: 500 * time.Millisecond,
	})
	defer service.Close()
	connectHosts(t, subject, verifier)
	wrong := tcpPort(t, subject) + 1
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Request(ctx, verifier.ID(), irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4, wrong, 1, 1); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("wrong port error=%v", err)
	}
}

func TestFreshChallengeFromWrongVerifierIdentityIsRejected(t *testing.T) {
	verifierKey, verifier := keyAndHost(t)
	wrongKey, _, _ := crypto.GenerateEd25519Key(nil)
	_, subject := keyAndHost(t)
	client, _ := NewClient(ClientConfig{Host: subject})
	defer client.Close()
	service, err := NewService(ServiceConfig{
		Host: verifier, PrivateKey: verifierKey, Challenger: &testChallenger{key: wrongKey},
		ObservedIP: ObservedIPPolicy{AllowLoopback: true}, DialTimeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	connectHosts(t, subject, verifier)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Request(ctx, verifier.ID(), irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4,
		tcpPort(t, subject), 1, 1); err == nil {
		t.Fatal("challenge from an unexpected verifier identity was accepted")
	}
}

func TestTargetInfoNeverAcceptsRequesterSuppliedIP(t *testing.T) {
	key, _, _ := crypto.GenerateEd25519Key(nil)
	id, _ := peer.IDFromPrivateKey(key)
	info, err := targetInfo(id, netip.MustParseAddr("203.0.113.17"), 45001)
	if err != nil || len(info.Addrs) != 1 || info.Addrs[0].String() != "/ip4/203.0.113.17/tcp/45001" {
		t.Fatalf("target=%+v error=%v", info, err)
	}
	if fmt.Sprint(info.ID) != id.String() {
		t.Fatal("target identity changed")
	}
}

func TestAttemptAndNonceReplayAreRejectedAndLimitsAreBounded(t *testing.T) {
	_, subject := keyAndHost(t)
	client, err := NewClient(ClientConfig{Host: subject, MaxPending: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	verifierKey, _, _ := crypto.GenerateEd25519Key(nil)
	verifier, _ := peer.IDFromPrivateKey(verifierKey)
	var attempt AttemptID
	attempt[0] = 1
	client.pending[attempt] = &pendingAttempt{verifier: verifier, expires: time.Now().Add(time.Minute)}
	if !client.consumeChallenge(attempt, verifier, time.Now()) {
		t.Fatal("first nonce challenge was rejected")
	}
	if client.consumeChallenge(attempt, verifier, time.Now()) {
		t.Fatal("replayed nonce challenge was accepted")
	}
	var expired AttemptID
	expired[0] = 2
	client.pending[expired] = &pendingAttempt{verifier: verifier, expires: time.Now().Add(-time.Second)}
	if client.consumeChallenge(expired, verifier, time.Now()) {
		t.Fatal("expired challenge was accepted")
	}

	service := &Service{
		config:  ServiceConfig{MaxActive: 1, MaxPerPeer: 1, MaxPerIP: 1, AttemptTTL: time.Minute},
		perPeer: make(map[peer.ID]int), perIP: make(map[netip.Addr]int), seen: make(map[AttemptID]time.Time),
	}
	now := time.Now()
	address := netip.MustParseAddr("203.0.113.9")
	if !service.acquire(verifier, address, attempt, now) {
		t.Fatal("first bounded attempt was rejected")
	}
	if service.acquire(verifier, address, attempt, now) {
		t.Fatal("replayed active attempt was accepted")
	}
	service.release(verifier, address)
	if service.acquire(verifier, address, attempt, now) {
		t.Fatal("single-use attempt was accepted after release")
	}
}

func TestAttestationExpiryAndFamilyMismatchAreRejected(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	subjectKey, _, _ := crypto.GenerateEd25519Key(nil)
	subject, _ := peer.IDFromPrivateKey(subjectKey)
	verifierKey, _, _ := crypto.GenerateEd25519Key(nil)
	verifier, _ := peer.IDFromPrivateKey(verifierKey)
	signed, err := signAttestation(verifierKey, &irviov1.ReachabilityAttestation{
		ProtocolVersion: ProtocolVersion, SubjectPeerId: []byte(subject), VerifierPeerId: []byte(verifier),
		Family:     irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6,
		ObservedIp: netip.MustParseAddr("203.0.113.8").AsSlice(), ExternalControlPort: 45001,
		NetworkGeneration: 1, PublicEpoch: 1, AttemptId: bytes.Repeat([]byte{1}, AttemptIDSize),
		VerifiedAtUnix: now.Add(-2 * time.Minute).Unix(), ExpiresAtUnix: now.Add(-time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAttestation(signed, now); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("family mismatch error=%v", err)
	}
	signed, err = signAttestation(verifierKey, &irviov1.ReachabilityAttestation{
		ProtocolVersion: ProtocolVersion, SubjectPeerId: []byte(subject), VerifierPeerId: []byte(verifier),
		Family:     irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4,
		ObservedIp: netip.MustParseAddr("203.0.113.8").AsSlice(), ExternalControlPort: 45001,
		NetworkGeneration: 1, PublicEpoch: 1, AttemptId: bytes.Repeat([]byte{2}, AttemptIDSize),
		VerifiedAtUnix: now.Add(-2 * time.Minute).Unix(), ExpiresAtUnix: now.Add(-time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAttestation(signed, now); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired attestation error=%v", err)
	}
}
