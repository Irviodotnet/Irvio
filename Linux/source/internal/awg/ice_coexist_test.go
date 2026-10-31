// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/iceengine"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestM6_RealPionSelectedPairDrivesAmneziaWGWhileICERemainsActive(t *testing.T) {
	tests := []struct {
		name      string
		addresses []netip.Addr
	}{
		{name: "ipv4", addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		{name: "ipv6", addresses: []netip.Addr{netip.MustParseAddr("::1")}},
		{name: "dual-stack", addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stackA := newICEAWGStack(t, test.addresses)
			stackB := newICEAWGStack(t, test.addresses)
			peerA, peerB := testPeerID(t), testPeerID(t)
			connection := connectICEAWGPair(t, stackA, stackB, peerA, peerB)
			defer connection.close()

			resultA := connection.a.Result()
			resultB := connection.b.Result()
			if resultA.ApplicationBytesSent != 0 || resultA.ApplicationBytesRecv != 0 ||
				resultB.ApplicationBytesSent != 0 || resultB.ApplicationBytesRecv != 0 {
				t.Fatal("ICE carried application bytes before AWG")
			}
			pathB, err := SelectedEndpointFromICE(resultA, 1, 1, peerB)
			if err != nil {
				t.Fatal(err)
			}
			pathA, err := SelectedEndpointFromICE(resultB, 1, 1, peerA)
			if err != nil {
				t.Fatal(err)
			}
			privateA, publicA := testAWGKeyPair(t, 31)
			privateB, publicB := testAWGKeyPair(t, 61)
			tunA, tunB := tuntest.NewChannelTUN(), tuntest.NewChannelTUN()
			innerA, innerB := netip.MustParseAddr("fd00:96::1"), netip.MustParseAddr("fd00:96::2")
			transportA := newSelectedAWGTransport(t, stackA.mux, tunA, privateA, peerB, publicB, pathB, innerB)
			transportB := newSelectedAWGTransport(t, stackB.mux, tunB, privateB, peerA, publicA, pathA, innerA)
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
			sendInner(t, tunA, tunB, innerPacket(innerA, innerB, 1))
			sendInner(t, tunB, tunA, innerPacket(innerB, innerA, 2))

			if got := transportA.Bind().Stats().LastSend; got != resultA.RemoteEndpoint {
				t.Fatalf("A AWG destination=%s selected ICE=%s", got, resultA.RemoteEndpoint)
			}
			if got := transportB.Bind().Stats().LastSend; got != resultB.RemoteEndpoint {
				t.Fatalf("B AWG destination=%s selected ICE=%s", got, resultB.RemoteEndpoint)
			}
			muxStatsA := stackA.mux.Stats()
			muxStatsB := stackB.mux.Stats()
			if muxStatsA.ControlEnqueued == 0 || muxStatsB.ControlEnqueued == 0 || muxStatsA.DataEnqueued == 0 || muxStatsB.DataEnqueued == 0 {
				t.Fatalf("ICE/AWG classifier coexistence missing: A=%+v B=%+v", muxStatsA, muxStatsB)
			}
			currentA, err := connection.a.CurrentResult()
			if err != nil {
				t.Fatal(err)
			}
			currentB, err := connection.b.CurrentResult()
			if err != nil {
				t.Fatal(err)
			}
			if currentA.ApplicationBytesSent != 0 || currentA.ApplicationBytesRecv != 0 ||
				currentB.ApplicationBytesSent != 0 || currentB.ApplicationBytesRecv != 0 {
				t.Fatal("AWG application traffic leaked through ice.Conn")
			}
			if stackA.engine.Stats().Adapter.ActiveUfrags != 1 || stackB.engine.Stats().Adapter.ActiveUfrags != 1 {
				t.Fatal("AWG disturbed active ICE sessions")
			}
		})
	}
}

func newSelectedAWGTransport(
	t *testing.T,
	mux *socketmux.Mux,
	tunDevice *tuntest.ChannelTUN,
	private Key,
	remotePeer peer.ID,
	public Key,
	endpoint SelectedEndpoint,
	allowed netip.Addr,
) *Transport {
	t.Helper()
	transport, err := NewTransport(Config{
		Mux: mux, NetworkGeneration: 1, TUN: tunDevice.TUN(), PrivateKey: private,
		Obfuscation: testObfuscation(),
		Peers: []PeerConfig{{
			RemotePeer: remotePeer, PublicKey: public, Endpoint: endpoint,
			AllowedIPs: []netip.Prefix{netip.PrefixFrom(allowed, allowed.BitLen())},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	return transport
}

type iceAWGStack struct {
	mux        *socketmux.Mux
	engine     *iceengine.Engine
	validation icesession.ValidationConfig
}

func newICEAWGStack(t *testing.T, addresses []netip.Addr) *iceAWGStack {
	t.Helper()
	registry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	config := socketmux.DefaultConfig()
	config.Port = 0
	config.IPv4Policy = socketmux.FamilyDisabled
	config.IPv6Policy = socketmux.FamilyDisabled
	for _, address := range addresses {
		if address.Is4() {
			config.IPv4Policy = socketmux.FamilyRequired
			config.IPv4Bind = address
		} else {
			config.IPv6Policy = socketmux.FamilyRequired
			config.IPv6Bind = address
		}
	}
	config.Classifier = iceadapter.NewClassifier(registry)
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	validation := icesession.DefaultValidationConfig()
	validation.AllowLoopback = true
	engineConfig := iceengine.DefaultConfig(addresses...)
	engineConfig.TransportGeneration = 1
	engineConfig.Session.Validation = validation
	engineConfig.Session.Logger = config.Logger
	engineConfig.Logger = config.Logger
	phases := icesession.NewPhaseManager()
	lease, err := phases.Acquire(context.Background(), icesession.ControlPhaseICE)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := iceengine.New(mux, registry, lease, engineConfig)
	if err != nil {
		_ = mux.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = engine.Close()
		_ = mux.Close()
	})
	return &iceAWGStack{mux: mux, engine: engine, validation: validation}
}

type iceAWGConnection struct {
	a, b                 *iceconnect.ConnectedSession
	preparedA, preparedB *icesession.PreparedSession
}

func (c *iceAWGConnection) close() {
	_ = c.a.Close()
	_ = c.b.Close()
}

func connectICEAWGPair(t *testing.T, a, b *iceAWGStack, peerA, peerB peer.ID) *iceAWGConnection {
	t.Helper()
	preparedA, err := a.engine.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	preparedB, err := b.engine.Prepare(context.Background())
	if err != nil {
		_ = preparedA.Close()
		t.Fatal(err)
	}
	sessionID, err := icesession.NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	bindingA := iceconnect.Binding{
		SessionID: sessionID, Initiator: peerA, Responder: peerB, LocalPeer: peerA, RemotePeer: peerB,
		Purpose: icesession.PurposeExit, LocalRole: icesession.RoleControlling, RemoteRole: icesession.RoleControlled,
	}
	bindingB := iceconnect.Binding{
		SessionID: sessionID, Initiator: peerA, Responder: peerB, LocalPeer: peerB, RemotePeer: peerA,
		Purpose: icesession.PurposeExit, LocalRole: icesession.RoleControlled, RemoteRole: icesession.RoleControlling,
	}
	if err := preparedA.BindConnectivity(connectivityBinding(bindingA)); err != nil {
		t.Fatal(err)
	}
	if err := preparedB.BindConnectivity(connectivityBinding(bindingB)); err != nil {
		t.Fatal(err)
	}
	configA := iceconnect.DefaultConfig()
	configA.Validation = a.validation
	configB := iceconnect.DefaultConfig()
	configB.Validation = b.validation
	attemptA, err := iceconnect.New(preparedA, bindingA, iceconnect.RemoteState{
		SessionID: sessionID, PeerID: peerB, Role: icesession.RoleControlled, Description: preparedB.LocalDescription(),
	}, configA)
	if err != nil {
		t.Fatal(err)
	}
	attemptB, err := iceconnect.New(preparedB, bindingB, iceconnect.RemoteState{
		SessionID: sessionID, PeerID: peerA, Role: icesession.RoleControlling, Description: preparedA.LocalDescription(),
	}, configB)
	if err != nil {
		t.Fatal(err)
	}
	if err := attemptA.LoadRemoteCandidates(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := attemptB.LoadRemoteCandidates(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type connectResult struct {
		connected *iceconnect.ConnectedSession
		err       error
	}
	bDone := make(chan connectResult, 1)
	go func() {
		connected, connectErr := attemptB.Connect(ctx)
		bDone <- connectResult{connected: connected, err: connectErr}
	}()
	connectedA, err := attemptA.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resultB := <-bDone
	if resultB.err != nil {
		t.Fatal(resultB.err)
	}
	return &iceAWGConnection{a: connectedA, b: resultB.connected, preparedA: preparedA, preparedB: preparedB}
}

func connectivityBinding(binding iceconnect.Binding) icesession.ConnectivityBinding {
	return icesession.ConnectivityBinding{
		SessionID: binding.SessionID, Initiator: binding.Initiator, Responder: binding.Responder,
		LocalPeer: binding.LocalPeer, RemotePeer: binding.RemotePeer, Purpose: binding.Purpose,
		LocalRole: binding.LocalRole, RemoteRole: binding.RemoteRole,
	}
}
