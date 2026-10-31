// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"

	"github.com/Irviodotnet/Irvio/internal/icesession"
)

func TestM6_OneExitTransportServesThreeEncryptedPeersAndIsolatesRestart(t *testing.T) {
	exitStack := newICEAWGStack(t, []netip.Addr{netip.MustParseAddr("127.0.0.1")})
	exitPrivate, exitPublic := testAWGKeyPair(t, 101)
	exitPeer := testPeerID(t)
	exitInner := netip.MustParseAddr("fd00:97::1")
	exitTUN := tuntest.NewChannelTUN()
	clients := make([]multiPeerClient, 0, 3)
	peerConfigs := make([]PeerConfig, 0, 3)
	for index := range 3 {
		client := newMultiPeerClient(t, byte(111+index*10), index, exitStack, exitPeer, exitPublic, exitInner)
		clients = append(clients, client)
		peerConfigs = append(peerConfigs, PeerConfig{
			RemotePeer: client.peer, PublicKey: client.public, Endpoint: client.path,
			AllowedIPs: []netip.Prefix{netip.PrefixFrom(client.inner, client.inner.BitLen())},
		})
	}
	exitTransport, err := NewTransport(Config{
		Mux: exitStack.mux, NetworkGeneration: 1, TUN: exitTUN.TUN(), PrivateKey: exitPrivate,
		Obfuscation: testObfuscation(), MaxPeers: 3, Peers: peerConfigs,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exitTransport.Close() })
	if exitTransport.PeerCount() != 3 {
		t.Fatalf("exit peer count = %d", exitTransport.PeerCount())
	}
	if exitStack.engine.Stats().ActiveSessions != 3 || exitStack.engine.Stats().Adapter.ActiveUfrags != 3 {
		t.Fatalf("exit ICE sessions do not share one engine: %+v", exitStack.engine.Stats())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for index := range clients {
		client := &clients[index]
		if err := client.transport.TriggerHandshake(exitPublic); err != nil {
			t.Fatal(err)
		}
		if _, err := client.transport.WaitHandshake(ctx, exitPublic); err != nil {
			clientStats, _ := client.transport.PeerStats(exitPublic)
			exitStats, _ := exitTransport.PeerStats(client.public)
			t.Fatalf("client %d handshake: %v client-peer=%+v client-bind=%+v exit-peer=%+v exit-bind=%+v", index, err, clientStats, client.transport.Bind().Stats(), exitStats, exitTransport.Bind().Stats())
		}
		if _, err := exitTransport.WaitHandshake(ctx, client.public); err != nil {
			t.Fatalf("exit peer %d handshake: %v", index, err)
		}
		sendInner(t, client.tun, exitTUN, innerPacket(client.inner, exitInner, byte(index+1)))
		sendInner(t, exitTUN, client.tun, innerPacket(exitInner, client.inner, byte(index+11)))
	}

	exitBind := exitTransport.Bind()
	exitPort := exitStack.mux.Port()
	restarted := &clients[0]
	unaffectedAgents := []*icesession.PreparedSession{
		clients[1].connection.preparedB,
		clients[2].connection.preparedB,
	}
	oldAgent := restarted.connection.preparedA.Agent()
	oldUfrag := restarted.connection.preparedA.LocalDescription().Credentials.Ufrag
	restarted.connection.close()
	replacement := connectICEAWGPair(t, restarted.stack, exitStack, restarted.peer, exitPeer)
	t.Cleanup(replacement.close)
	if replacement.preparedA.Agent() == oldAgent || replacement.preparedA.LocalDescription().Credentials.Ufrag == oldUfrag {
		t.Fatal("one-client reconnect did not create a fresh Pion Agent and local ufrag")
	}
	restarted.path, err = SelectedEndpointFromICE(replacement.b.Result(), 1, 2, restarted.peer)
	if err != nil {
		t.Fatal(err)
	}
	if err := exitTransport.UpdatePeerEndpoint(restarted.public, restarted.path); err != nil {
		t.Fatal(err)
	}
	restarted.exitPath, err = SelectedEndpointFromICE(replacement.a.Result(), 1, 2, exitPeer)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.transport.UpdatePeerEndpoint(exitPublic, restarted.exitPath); err != nil {
		t.Fatal(err)
	}
	restarted.connection = replacement
	if err := restarted.transport.TriggerHandshake(exitPublic); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.transport.WaitHandshake(ctx, exitPublic); err != nil {
		t.Fatal(err)
	}
	if exitTransport.Bind() != exitBind || exitStack.mux.Port() != exitPort || exitTransport.PeerCount() != 3 ||
		exitStack.engine.Stats().ActiveSessions != 3 || exitStack.engine.Stats().Adapter.ActiveUfrags != 3 {
		t.Fatal("one-peer restart recreated or disturbed the shared exit transport")
	}
	for index, prepared := range unaffectedAgents {
		if clients[index+1].connection.preparedB.Agent() != prepared.Agent() {
			t.Fatalf("unrelated client %d Pion Agent was recreated", index+1)
		}
		if _, err := clients[index+1].connection.b.CurrentResult(); err != nil {
			t.Fatalf("unrelated client %d ICE connection was disturbed: %v", index+1, err)
		}
	}
	for index := range clients {
		client := &clients[index]
		sendInner(t, client.tun, exitTUN, innerPacket(client.inner, exitInner, byte(index+21)))
		sendInner(t, exitTUN, client.tun, innerPacket(exitInner, client.inner, byte(index+31)))
	}

	if err := exitTransport.RemovePeer(restarted.public); err != nil {
		t.Fatal(err)
	}
	if exitTransport.PeerCount() != 2 {
		t.Fatalf("exit peer count after removal = %d", exitTransport.PeerCount())
	}
	for index := 1; index < len(clients); index++ {
		client := &clients[index]
		sendInner(t, client.tun, exitTUN, innerPacket(client.inner, exitInner, byte(index+41)))
		sendInner(t, exitTUN, client.tun, innerPacket(exitInner, client.inner, byte(index+51)))
	}
}

type multiPeerClient struct {
	peer       stringPeer
	public     Key
	inner      netip.Addr
	path       SelectedEndpoint
	exitPath   SelectedEndpoint
	tun        *tuntest.ChannelTUN
	transport  *Transport
	stack      *iceAWGStack
	connection *iceAWGConnection
}

func newMultiPeerClient(
	t *testing.T,
	keySeed byte,
	index int,
	exitStack *iceAWGStack,
	exitPeer stringPeer,
	exitPublic Key,
	exitInner netip.Addr,
) multiPeerClient {
	t.Helper()
	clientStack := newICEAWGStack(t, []netip.Addr{netip.MustParseAddr("127.0.0.1")})
	private, public := testAWGKeyPair(t, keySeed)
	clientPeer := testPeerID(t)
	clientInner := netip.MustParseAddr("fd00:97::" + string(rune('2'+index)))
	connection := connectICEAWGPair(t, clientStack, exitStack, clientPeer, exitPeer)
	t.Cleanup(connection.close)
	clientPath, err := SelectedEndpointFromICE(connection.b.Result(), 1, 1, clientPeer)
	if err != nil {
		t.Fatal(err)
	}
	exitPath, err := SelectedEndpointFromICE(connection.a.Result(), 1, 1, exitPeer)
	if err != nil {
		t.Fatal(err)
	}
	clientTUN := tuntest.NewChannelTUN()
	transport, err := NewTransport(Config{
		Mux: clientStack.mux, NetworkGeneration: 1, TUN: clientTUN.TUN(), PrivateKey: private,
		Obfuscation: testObfuscation(),
		Peers: []PeerConfig{{
			RemotePeer: exitPeer, PublicKey: exitPublic, Endpoint: exitPath,
			AllowedIPs: []netip.Prefix{netip.PrefixFrom(exitInner, exitInner.BitLen())},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	return multiPeerClient{
		peer: clientPeer, public: public, inner: clientInner, path: clientPath,
		exitPath: exitPath, tun: clientTUN, transport: transport, stack: clientStack, connection: connection,
	}
}
