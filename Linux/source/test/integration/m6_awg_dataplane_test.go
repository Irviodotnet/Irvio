// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package integration

import (
	"context"
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/crypto/curve25519"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestM6_FullAuthenticatedSignalingICEAndEncryptedAWG(t *testing.T) {
	tests := []struct {
		name            string
		network         icesession.NetworkType
		signalingFamily string
		innerInitiator  netip.Addr
		innerResponder  netip.Addr
	}{
		{
			name: "outer-udp4-inner-ipv6", network: icesession.NetworkUDP4, signalingFamily: "ip4",
			innerInitiator: netip.MustParseAddr("fd00:98::1"), innerResponder: netip.MustParseAddr("fd00:98::2"),
		},
		{
			name: "outer-udp6-inner-ipv4", network: icesession.NetworkUDP6, signalingFamily: "ip6",
			innerInitiator: netip.MustParseAddr("10.98.0.1"), innerResponder: netip.MustParseAddr("10.98.0.2"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runFullIrvioDirect(t, []icesession.NetworkType{test.network}, test.signalingFamily, test.network, func(result *fullIrvioDirectResult) {
				pathToResponder, err := awg.SelectedEndpointFromICE(result.InitiatorResult, 1, 1, result.ResponderPeer)
				if err != nil {
					t.Fatal(err)
				}
				pathToInitiator, err := awg.SelectedEndpointFromICE(result.ResponderResult, 1, 1, result.InitiatorPeer)
				if err != nil {
					t.Fatal(err)
				}
				privateA, publicA := m6KeyPair(t, 17)
				privateB, publicB := m6KeyPair(t, 57)
				tunA, tunB := tuntest.NewChannelTUN(), tuntest.NewChannelTUN()
				transportA := m6Transport(t, result.InitiatorStack, tunA, privateA, result.ResponderPeer, publicB, pathToResponder, test.innerResponder)
				transportB := m6Transport(t, result.ResponderStack, tunB, privateB, result.InitiatorPeer, publicA, pathToInitiator, test.innerInitiator)
				if err := transportA.TriggerHandshake(publicB); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := transportA.WaitHandshake(ctx, publicB); err != nil {
					t.Fatal(err)
				}
				if _, err := transportB.WaitHandshake(ctx, publicA); err != nil {
					t.Fatal(err)
				}
				m6SendInner(t, tunA, tunB, m6InnerPacket(test.innerInitiator, test.innerResponder, 1))
				m6SendInner(t, tunB, tunA, m6InnerPacket(test.innerResponder, test.innerInitiator, 2))

				if got := transportA.Bind().Stats().LastSend; got != result.InitiatorResult.RemoteEndpoint {
					t.Fatalf("initiator AWG endpoint=%s selected ICE endpoint=%s", got, result.InitiatorResult.RemoteEndpoint)
				}
				if got := transportB.Bind().Stats().LastSend; got != result.ResponderResult.RemoteEndpoint {
					t.Fatalf("responder AWG endpoint=%s selected ICE endpoint=%s", got, result.ResponderResult.RemoteEndpoint)
				}
				currentA, err := result.InitiatorICE.CurrentResult()
				if err != nil {
					t.Fatal(err)
				}
				currentB, err := result.ResponderICE.CurrentResult()
				if err != nil {
					t.Fatal(err)
				}
				if currentA.ApplicationBytesSent != 0 || currentA.ApplicationBytesRecv != 0 ||
					currentB.ApplicationBytesSent != 0 || currentB.ApplicationBytesRecv != 0 {
					t.Fatal("encrypted application traffic passed through ice.Conn")
				}
				if result.InitiatorStack.mux.Stats().DataEnqueued == 0 || result.ResponderStack.mux.Stats().DataEnqueued == 0 {
					t.Fatal("AWG datagrams did not use IrvioSocketMux Data")
				}
			})
		})
	}
}

func TestM6_FullTransportGenerationMigrationEncryptedDataPlane(t *testing.T) {
	runFullTransportGenerationMigration(t, true)
}

func TestM6_SameNetworkFreshICESessionUpdatesAWGEndpoint(t *testing.T) {
	pair := newLifecyclePair(t, icesession.NetworkUDP4)
	supervisor := pair.newSupervisor(t)
	firstEvent := lifecycleEvent(t, supervisor.Events(), connsupervisor.EventConnected)
	first := pair.record(firstEvent.SessionGeneration)
	privateA, publicA := m6KeyPair(t, 33)
	privateB, publicB := m6KeyPair(t, 66)
	tunA, tunB := tuntest.NewChannelTUN(), tuntest.NewChannelTUN()
	innerA := netip.MustParseAddr("10.100.0.1")
	innerB := netip.MustParseAddr("10.100.0.2")
	pathA, err := awg.SelectedEndpointFromICE(
		first.session.initiator.Result(), 1, firstEvent.SessionGeneration, pair.remotePeer,
	)
	if err != nil {
		t.Fatal(err)
	}
	pathB, err := awg.SelectedEndpointFromICE(
		first.session.responder.Result(), 1, firstEvent.SessionGeneration, pair.localPeer,
	)
	if err != nil {
		t.Fatal(err)
	}
	transportA := m6TransportForMux(t, pair.initiator.mux, 1, tunA, privateA, pair.remotePeer, publicB, pathA, innerB)
	transportB := m6TransportForMux(t, pair.responder.mux, 1, tunB, privateB, pair.localPeer, publicA, pathB, innerA)
	establishM6Handshake(t, transportA, transportB, publicB, publicA)
	m6SendInner(t, tunA, tunB, m6InnerPacket(innerA, innerB, 1))
	m6SendInner(t, tunB, tunA, m6InnerPacket(innerB, innerA, 2))
	bindA, bindB := transportA.Bind(), transportB.Bind()
	muxA, muxB := pair.initiator.mux, pair.responder.mux

	if err := supervisor.Restart(connsupervisor.ReasonExplicitRestart); err != nil {
		t.Fatal(err)
	}
	secondEvent := lifecycleEvent(t, supervisor.Events(), connsupervisor.EventRestartSucceeded)
	second := pair.record(secondEvent.SessionGeneration)
	if secondEvent.SessionGeneration <= firstEvent.SessionGeneration || first.sessionID == second.sessionID ||
		first.ufrag == second.ufrag || first.password == second.password || first.agent == second.agent {
		t.Fatal("same-network reconnect did not create a fresh ICE session generation")
	}
	pathA2, err := awg.SelectedEndpointFromICE(
		second.session.initiator.Result(), 1, secondEvent.SessionGeneration, pair.remotePeer,
	)
	if err != nil {
		t.Fatal(err)
	}
	pathB2, err := awg.SelectedEndpointFromICE(
		second.session.responder.Result(), 1, secondEvent.SessionGeneration, pair.localPeer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := transportA.UpdatePeerEndpoint(publicB, pathA2); err != nil {
		t.Fatal(err)
	}
	if err := transportB.UpdatePeerEndpoint(publicA, pathB2); err != nil {
		t.Fatal(err)
	}
	if err := transportA.UpdatePeerEndpoint(publicB, pathA); err == nil {
		t.Fatal("stale ICE session generation replaced the current AWG endpoint")
	}
	establishM6Handshake(t, transportA, transportB, publicB, publicA)
	m6SendInner(t, tunA, tunB, m6InnerPacket(innerA, innerB, 3))
	m6SendInner(t, tunB, tunA, m6InnerPacket(innerB, innerA, 4))
	if transportA.Bind() != bindA || transportB.Bind() != bindB ||
		pair.initiator.mux != muxA || pair.responder.mux != muxB ||
		transportA.Bind().Port() != muxA.Port() || transportB.Bind().Port() != muxB.Port() {
		t.Fatal("same-network reconnect recreated a shared AWG bind or Irvio mux")
	}
	if !first.session.closed.Load() || second.session.closed.Load() {
		t.Fatal("make-before-break promotion closed the wrong ICE generation")
	}
}

func m6Transport(
	t *testing.T,
	stack *m5bStack,
	tunDevice *tuntest.ChannelTUN,
	private awg.Key,
	remotePeer peer.ID,
	remotePublic awg.Key,
	endpoint awg.SelectedEndpoint,
	allowed netip.Addr,
) *awg.Transport {
	t.Helper()
	return m6TransportForMux(t, stack.mux, networkstate.Generation(1), tunDevice, private, remotePeer, remotePublic, endpoint, allowed)
}

func m6TransportForMux(
	t *testing.T,
	mux *socketmux.Mux,
	networkGeneration networkstate.Generation,
	tunDevice *tuntest.ChannelTUN,
	private awg.Key,
	remotePeer peer.ID,
	remotePublic awg.Key,
	endpoint awg.SelectedEndpoint,
	allowed netip.Addr,
) *awg.Transport {
	t.Helper()
	transport, err := awg.NewTransport(awg.Config{
		Mux: mux, NetworkGeneration: networkGeneration, TUN: tunDevice.TUN(), PrivateKey: private,
		Obfuscation: m6Obfuscation(),
		Peers: []awg.PeerConfig{{
			RemotePeer: remotePeer, PublicKey: remotePublic, Endpoint: endpoint,
			AllowedIPs: []netip.Prefix{netip.PrefixFrom(allowed, allowed.BitLen())},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	return transport
}

func establishM6Handshake(t *testing.T, a, b *awg.Transport, publicB, publicA awg.Key) {
	t.Helper()
	if err := a.TriggerHandshake(publicB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := a.WaitHandshake(ctx, publicB); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WaitHandshake(ctx, publicA); err != nil {
		t.Fatal(err)
	}
}

func m6KeyPair(t *testing.T, seed byte) (awg.Key, awg.Key) {
	t.Helper()
	var private awg.Key
	for index := range private {
		private[index] = seed + byte(index)
	}
	encoded, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	var public awg.Key
	copy(public[:], encoded)
	return private, public
}

func m6Obfuscation() awg.ObfuscationConfig {
	return awg.ObfuscationConfig{
		JunkPacketCount: 1, JunkMin: 24, JunkMax: 24,
		InitPadding: 16, ResponsePadding: 16, CookiePadding: 8,
		InitHeader: 101, ResponseHeader: 202, CookieHeader: 303, TransportHeader: 404,
		SignaturePackets: [5]string{"<b 0x4859445241>"},
	}
}

func m6SendInner(t *testing.T, source, destination *tuntest.ChannelTUN, packet []byte) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case source.Outbound <- append([]byte(nil), packet...):
	case <-timer.C:
		t.Fatal("inject M6 inner packet timed out")
	}
	timer.Reset(3 * time.Second)
	select {
	case received := <-destination.Inbound:
		if string(received) != string(packet) {
			t.Fatalf("decrypted M6 inner packet mismatch: got=%x want=%x", received, packet)
		}
	case <-timer.C:
		t.Fatal("decrypted M6 inner packet timed out")
	}
}

func m6InnerPacket(source, destination netip.Addr, sequence byte) []byte {
	if source.Is4() {
		packet := make([]byte, 28)
		packet[0] = 0x45
		binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
		packet[8], packet[9] = 64, 17
		copy(packet[12:16], source.AsSlice())
		copy(packet[16:20], destination.AsSlice())
		packet[27] = sequence
		return packet
	}
	packet := make([]byte, 48)
	packet[0] = 0x60
	binary.BigEndian.PutUint16(packet[4:6], 8)
	packet[6], packet[7] = 17, 64
	copy(packet[8:24], source.AsSlice())
	copy(packet[24:40], destination.AsSlice())
	packet[47] = sequence
	return packet
}
