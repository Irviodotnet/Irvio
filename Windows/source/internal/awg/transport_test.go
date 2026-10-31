// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"
	"golang.org/x/crypto/curve25519"

	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestInitialUAPIConfiguresBoundedHandshakeRetry(t *testing.T) {
	mux := newDataMux(t, netip.MustParseAddr("127.0.0.1"), false)
	private, _ := testAWGKeyPair(t, 33)
	testTUN := tuntest.NewChannelTUN()
	deviceTUN := testTUN.TUN()
	t.Cleanup(func() { _ = deviceTUN.Close() })
	config := Config{
		Mux: mux, NetworkGeneration: 1, TUN: deviceTUN, PrivateKey: private,
		Obfuscation: testObfuscation(), HandshakeRetrySeconds: defaultHandshakeRetrySeconds,
	}
	if value := buildInitialUAPI(config); !strings.Contains(value, "rekey_timeout=1\n") {
		t.Fatalf("initial UAPI does not configure the bounded handshake retry: %q", value)
	}
	config.HandshakeRetrySeconds = maxHandshakeRetrySeconds + 1
	if err := config.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("oversized handshake retry error = %v", err)
	}
}

func TestRealAmneziaWGBidirectionalInnerTraffic(t *testing.T) {
	tests := []struct {
		name    string
		outerA  string
		outerB  string
		innerA  string
		innerB  string
		network icesession.NetworkType
	}{
		{name: "outer-udp4-inner-ipv4", outerA: "127.0.0.1", outerB: "127.0.0.1", innerA: "10.80.0.1", innerB: "10.80.0.2", network: icesession.NetworkUDP4},
		{name: "outer-udp6-inner-ipv6", outerA: "::1", outerB: "::1", innerA: "fd00:80::1", innerB: "fd00:80::2", network: icesession.NetworkUDP6},
		{name: "outer-udp4-inner-ipv6", outerA: "127.0.0.1", outerB: "127.0.0.1", innerA: "fd00:81::1", innerB: "fd00:81::2", network: icesession.NetworkUDP4},
		{name: "outer-udp6-inner-ipv4", outerA: "::1", outerB: "::1", innerA: "10.81.0.1", innerB: "10.81.0.2", network: icesession.NetworkUDP6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pair := newAWGTestPair(t, test.outerA, test.outerB, test.innerA, test.innerB, test.network)
			pair.establishHandshake(t)
			pair.sendBothDirections(t)
			if pair.a.Bind().Port() != pair.muxA.Port() || pair.b.Bind().Port() != pair.muxB.Port() {
				t.Fatal("AWG bind did not retain mux source port")
			}
			if pair.a.Bind().Stats().SendPackets == 0 || pair.b.Bind().Stats().ReceivePackets == 0 {
				t.Fatal("AWG did not traverse IrvioBind")
			}
		})
	}
}

func TestTransportEndpointUpdateIsGenerationFenced(t *testing.T) {
	pair := newAWGTestPair(t, "127.0.0.1", "127.0.0.1", "10.82.0.1", "10.82.0.2", icesession.NetworkUDP4)
	state, err := pair.a.SessionGeneration(pair.publicB)
	if err != nil || state != 1 {
		t.Fatalf("initial session generation=%d err=%v", state, err)
	}
	current := pair.pathB
	current.SessionGeneration = 2
	if err := pair.a.UpdatePeerEndpoint(pair.publicB, current); err != nil {
		t.Fatal(err)
	}
	stale := current
	stale.SessionGeneration = 1
	if err := pair.a.UpdatePeerEndpoint(pair.publicB, stale); !errors.Is(err, ErrStaleSession) {
		t.Fatalf("stale update error = %v", err)
	}
	wrongGeneration := current
	wrongGeneration.SessionGeneration = 3
	wrongGeneration.NetworkGeneration = 2
	if err := pair.a.UpdatePeerEndpoint(pair.publicB, wrongGeneration); !errors.Is(err, ErrWrongGeneration) {
		t.Fatalf("wrong network generation error = %v", err)
	}
	wrongPeer := current
	wrongPeer.SessionGeneration = 3
	wrongPeer.RemotePeer = testPeerID(t)
	if err := pair.a.UpdatePeerEndpoint(pair.publicB, wrongPeer); !errors.Is(err, ErrWrongPeer) {
		t.Fatalf("wrong peer error = %v", err)
	}
}

func TestTriggerHandshakeRekeysAfterRemoteDeviceReplacement(t *testing.T) {
	pair := newAWGTestPair(t, "127.0.0.1", "127.0.0.1", "10.83.0.1", "10.83.0.2", icesession.NetworkUDP4)
	pair.establishHandshake(t)
	previous, err := pair.a.PeerStats(pair.publicB)
	if err != nil || previous.LastHandshake.IsZero() {
		t.Fatalf("initial handshake: stats=%+v err=%v", previous, err)
	}
	if err := pair.b.Close(); err != nil {
		t.Fatal(err)
	}
	replacementTUN := tuntest.NewChannelTUN()
	replacement, err := NewTransport(Config{
		Mux: pair.muxB, NetworkGeneration: 1, TUN: replacementTUN.TUN(), PrivateKey: pair.privateB,
		Obfuscation: testObfuscation(),
		Peers: []PeerConfig{{
			RemotePeer: pair.peerA, PublicKey: pair.publicA, Endpoint: pair.pathA,
			AllowedIPs: []netip.Prefix{netip.PrefixFrom(pair.innerA, pair.innerA.BitLen())},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	if err := pair.a.TriggerHandshake(pair.publicB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	refreshed, err := pair.a.WaitHandshake(ctx, pair.publicB)
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed.LastHandshake.After(previous.LastHandshake) {
		t.Fatalf("replacement device reused stale Noise session: old=%s new=%s", previous.LastHandshake, refreshed.LastHandshake)
	}
	if _, err := replacement.WaitHandshake(ctx, pair.publicA); err != nil {
		t.Fatal(err)
	}
	sendInner(t, pair.tunA, replacementTUN, innerPacket(pair.innerA, pair.innerB, 9))
}

func TestTransportRotatesKeysWithoutReplacingMux(t *testing.T) {
	pair := newAWGTestPair(t, "127.0.0.1", "127.0.0.1", "10.84.0.1", "10.84.0.2", icesession.NetworkUDP4)
	pair.establishHandshake(t)
	portA, portB := pair.a.Bind().Port(), pair.b.Bind().Port()
	privateB, publicB := testAWGKeyPair(t, 44)
	if err := pair.a.ReplacePeerKey(pair.publicB, publicB, pair.peerB); err != nil {
		t.Fatal(err)
	}
	if err := pair.b.UpdatePrivateKey(privateB); err != nil {
		t.Fatal(err)
	}
	if err := pair.a.TriggerHandshake(publicB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pair.a.WaitHandshakeActive(ctx, publicB); err != nil {
		t.Fatal(err)
	}
	if _, err := pair.b.WaitHandshake(ctx, pair.publicA); err != nil {
		t.Fatal(err)
	}
	sendInner(t, pair.tunA, pair.tunB, innerPacket(pair.innerA, pair.innerB, 11))
	if pair.a.Bind().Port() != portA || pair.b.Bind().Port() != portB {
		t.Fatalf("key rotation replaced IrvioBind ports: before=%d/%d after=%d/%d", portA, portB, pair.a.Bind().Port(), pair.b.Bind().Port())
	}
	if _, err := pair.a.PeerStats(pair.publicB); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("retired AWG key remains installed: %v", err)
	}
}

func TestWaitHandshakeRetriesCrossedKeyRotation(t *testing.T) {
	pair := newAWGTestPair(t, "127.0.0.1", "127.0.0.1", "10.85.0.1", "10.85.0.2", icesession.NetworkUDP4)
	pair.establishHandshake(t)
	privateB, publicB := testAWGKeyPair(t, 45)
	if err := pair.a.StagePeerKeyRotation(pair.publicB, publicB, pair.peerB); err != nil {
		t.Fatal(err)
	}
	before := pair.b.Bind().Stats().ReceivePackets
	if err := pair.a.TriggerHandshake(publicB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for pair.b.Bind().Stats().ReceivePackets == before {
		select {
		case <-ctx.Done():
			t.Fatal("first rotation initiation did not reach the old-key peer")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := pair.b.PrepareHandshake(pair.publicA); err != nil {
		t.Fatal(err)
	}
	if err := pair.b.UpdatePrivateKey(privateB); err != nil {
		t.Fatal(err)
	}
	if _, err := pair.a.WaitHandshakeActive(ctx, publicB); err != nil {
		t.Fatal(err)
	}
	if _, err := pair.b.WaitHandshake(ctx, pair.publicA); err != nil {
		t.Fatal(err)
	}
	if err := pair.a.FinalizePeerKeyRotation(pair.publicB, publicB, pair.peerB); err != nil {
		t.Fatal(err)
	}
	if _, err := pair.a.PeerStats(pair.publicB); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("retired key remained after finalize: %v", err)
	}
	sendInner(t, pair.tunA, pair.tunB, innerPacket(pair.innerA, pair.innerB, 12))
}

func TestPinnedUpstreamTransportPaddingBoundary(t *testing.T) {
	mux := newDataMux(t, netip.MustParseAddr("127.0.0.1"), false)
	private, _ := testAWGKeyPair(t, 11)
	config := Config{
		Mux: mux, NetworkGeneration: 1, TUN: tuntest.NewChannelTUN().TUN(), PrivateKey: private,
		Obfuscation: testObfuscation(),
	}
	config.Obfuscation.TransportPadding = 25
	if _, err := NewTransport(config); !errors.Is(err, ErrTransportPaddingUnsupported) {
		t.Fatalf("nonzero S4 error = %v", err)
	}
}

func TestPeerAllowsInternetCatchAllPrefixes(t *testing.T) {
	pair := newAWGTestPair(t, "127.0.0.1", "127.0.0.1", "10.89.0.1", "10.89.0.2", icesession.NetworkUDP4)
	peerConfig := PeerConfig{
		RemotePeer: pair.pathB.RemotePeer,
		PublicKey:  pair.publicB,
		Endpoint:   pair.pathB,
		AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")},
	}
	if err := validatePeerConfig(peerConfig, pair.pathB.NetworkGeneration); err != nil {
		t.Fatalf("catch-all AllowedIPs rejected: %v", err)
	}
	peerConfig.AllowedIPs = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1")}
	if err := validatePeerConfig(peerConfig, pair.pathB.NetworkGeneration); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("non-default unspecified prefix error=%v", err)
	}
}

type awgTestPair struct {
	muxA, muxB        *socketmux.Mux
	tunA, tunB        *tuntest.ChannelTUN
	a, b              *Transport
	privateA, publicA Key
	privateB, publicB Key
	peerA, peerB      stringPeer
	innerA, innerB    netip.Addr
	pathA, pathB      SelectedEndpoint
}

type stringPeer = peer.ID

func newAWGTestPair(t *testing.T, outerAText, outerBText, innerAText, innerBText string, network icesession.NetworkType) *awgTestPair {
	t.Helper()
	outerA := netip.MustParseAddr(outerAText)
	outerB := netip.MustParseAddr(outerBText)
	muxA := newDataMux(t, outerA, false)
	muxB := newDataMux(t, outerB, false)
	return newAWGTestPairWithMuxes(t, muxA, muxB, outerA, outerB, innerAText, innerBText, network)
}

func newAWGTestPairWithMuxes(
	t *testing.T,
	muxA, muxB *socketmux.Mux,
	outerA, outerB netip.Addr,
	innerAText, innerBText string,
	network icesession.NetworkType,
) *awgTestPair {
	t.Helper()
	privateA, publicA := testAWGKeyPair(t, 1)
	privateB, publicB := testAWGKeyPair(t, 2)
	peerA, peerB := testPeerID(t), testPeerID(t)
	endpointA := localEndpoint(t, muxA, outerA)
	endpointB := localEndpoint(t, muxB, outerB)
	pathA := syntheticSelectedEndpoint(peerA, endpointA, network, 1)
	pathB := syntheticSelectedEndpoint(peerB, endpointB, network, 1)
	innerA, innerB := netip.MustParseAddr(innerAText), netip.MustParseAddr(innerBText)
	tunA, tunB := tuntest.NewChannelTUN(), tuntest.NewChannelTUN()
	transportA, err := NewTransport(Config{
		Mux: muxA, NetworkGeneration: 1, TUN: tunA.TUN(), PrivateKey: privateA,
		Obfuscation: testObfuscation(),
		Peers:       []PeerConfig{{RemotePeer: peerB, PublicKey: publicB, Endpoint: pathB, AllowedIPs: []netip.Prefix{netip.PrefixFrom(innerB, innerB.BitLen())}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transportA.Close() })
	transportB, err := NewTransport(Config{
		Mux: muxB, NetworkGeneration: 1, TUN: tunB.TUN(), PrivateKey: privateB,
		Obfuscation: testObfuscation(),
		Peers:       []PeerConfig{{RemotePeer: peerA, PublicKey: publicA, Endpoint: pathA, AllowedIPs: []netip.Prefix{netip.PrefixFrom(innerA, innerA.BitLen())}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transportB.Close() })
	return &awgTestPair{
		muxA: muxA, muxB: muxB, tunA: tunA, tunB: tunB, a: transportA, b: transportB,
		privateA: privateA, publicA: publicA, privateB: privateB, publicB: publicB,
		peerA: peerA, peerB: peerB, innerA: innerA, innerB: innerB, pathA: pathA, pathB: pathB,
	}
}

func (p *awgTestPair) establishHandshake(t *testing.T) {
	t.Helper()
	if err := p.a.TriggerHandshake(p.publicB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	aStats, err := p.a.WaitHandshake(ctx, p.publicB)
	if err != nil {
		t.Fatalf("wait A handshake: %v", err)
	}
	bStats, err := p.b.WaitHandshake(ctx, p.publicA)
	if err != nil {
		t.Fatalf("wait B handshake: %v", err)
	}
	if aStats.LastHandshake.IsZero() || bStats.LastHandshake.IsZero() {
		t.Fatal("AWG handshake timestamp missing")
	}
}

func (p *awgTestPair) sendBothDirections(t *testing.T) {
	t.Helper()
	packetAB := innerPacket(p.innerA, p.innerB, 1)
	sendInner(t, p.tunA, p.tunB, packetAB)
	packetBA := innerPacket(p.innerB, p.innerA, 2)
	sendInner(t, p.tunB, p.tunA, packetBA)
}

func sendInner(t *testing.T, source, destination *tuntest.ChannelTUN, packet []byte) {
	t.Helper()
	select {
	case source.Outbound <- append([]byte(nil), packet...):
	case <-time.After(time.Second):
		t.Fatal("inject inner packet timed out")
	}
	select {
	case received := <-destination.Inbound:
		if string(received) != string(packet) {
			t.Fatalf("decrypted inner packet mismatch: got=%x want=%x", received, packet)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("decrypted inner packet timed out")
	}
}

func innerPacket(source, destination netip.Addr, sequence byte) []byte {
	if source.Is4() {
		packet := make([]byte, 28)
		packet[0] = 0x45
		binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
		packet[8] = 64
		packet[9] = 17
		copy(packet[12:16], source.AsSlice())
		copy(packet[16:20], destination.AsSlice())
		packet[27] = sequence
		return packet
	}
	packet := make([]byte, 48)
	packet[0] = 0x60
	binary.BigEndian.PutUint16(packet[4:6], 8)
	packet[6] = 17
	packet[7] = 64
	copy(packet[8:24], source.AsSlice())
	copy(packet[24:40], destination.AsSlice())
	packet[47] = sequence
	return packet
}

func localEndpoint(t *testing.T, mux *socketmux.Mux, address netip.Addr) netip.AddrPort {
	t.Helper()
	family := socketmux.IPv4
	if address.Is6() {
		family = socketmux.IPv6
	}
	endpoint, ok := mux.LocalAddr(family)
	if !ok {
		t.Fatalf("mux has no %s endpoint", family)
	}
	return endpoint
}

func syntheticSelectedEndpoint(remote stringPeer, endpoint netip.AddrPort, network icesession.NetworkType, generation connsupervisor.SessionGeneration) SelectedEndpoint {
	return SelectedEndpoint{
		NetworkGeneration: networkstate.Generation(1), SessionGeneration: generation,
		RemotePeer: remote, Address: endpoint,
		Candidate: iceconnect.CandidateSnapshot{
			Type: ice.CandidateTypeHost, Network: network, Address: endpoint.Addr(), Port: endpoint.Port(), Component: 1,
		},
	}
}

func testAWGKeyPair(t *testing.T, seed byte) (Key, Key) {
	t.Helper()
	var private Key
	for index := range private {
		private[index] = seed + byte(index)
	}
	publicBytes, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	var public Key
	copy(public[:], publicBytes)
	return private, public
}

func testObfuscation() ObfuscationConfig {
	return ObfuscationConfig{
		JunkPacketCount: 1, JunkMin: 24, JunkMax: 24,
		InitPadding: 16, ResponsePadding: 16, CookiePadding: 8, TransportPadding: 0,
		InitHeader: 101, ResponseHeader: 202, CookieHeader: 303, TransportHeader: 404,
		SignaturePackets: [5]string{"<b 0x4859445241>"},
	}
}
