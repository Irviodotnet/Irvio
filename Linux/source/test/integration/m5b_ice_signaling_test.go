// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package integration

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/iceengine"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestM5B_AuthenticatedDualStackDescriptionExchange(t *testing.T) {
	initiatorHost := newM5BHost(t)
	responderHost := newM5BHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := initiatorHost.Connect(ctx, peer.AddrInfo{ID: responderHost.ID(), Addrs: responderHost.Addrs()}); err != nil {
		t.Fatal(err)
	}

	initiator := newM5BPrepared(t, true)
	var responder *m5bStack
	var responderMu sync.Mutex
	config := icesignal.DefaultConfig()
	config.Validation.AllowLoopback = true
	service, err := icesignal.NewService(
		responderHost,
		responderHost.ID(),
		icesignal.SessionFactoryFunc(func(context.Context, peer.ID, icesession.Description) (*icesession.PreparedSession, error) {
			stack, err := buildM5BPrepared(true)
			if err != nil {
				return nil, err
			}
			responderMu.Lock()
			responder = stack
			responderMu.Unlock()
			return stack.session, nil
		}),
		icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	client, err := icesignal.NewClient(initiatorHost, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	before := initiator.session.Adapter().Stats().OutboundTransactionsRegistered
	result, err := client.Exchange(ctx, responderHost.ID(), initiator.session)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := service.NextAccepted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Local.Close()
	if result.SessionID != accepted.SessionID || result.RemotePeer != responderHost.ID() ||
		accepted.RemotePeer != initiatorHost.ID() {
		t.Fatal("authenticated session binding mismatch")
	}
	assertM5BDescription(t, result.RemoteDescription, true)
	assertM5BDescription(t, accepted.RemoteDescription, true)
	if initiator.session.Adapter().Stats().OutboundTransactionsRegistered != before ||
		accepted.Local.Adapter().Stats().OutboundTransactionsRegistered != 0 {
		t.Fatal("Offer/Answer initiated ICE connectivity traffic")
	}
	responderMu.Lock()
	stack := responder
	responderMu.Unlock()
	if stack == nil {
		t.Fatal("responder did not prepare a live Pion session")
	}
	if result.RemoteDescription.Credentials.Pwd == "" ||
		accepted.RemoteDescription.Credentials.Pwd != initiator.session.LocalDescription().Credentials.Pwd {
		t.Fatal("Pion credentials were not delivered intact")
	}
}

type m5bStack struct {
	mux     *socketmux.Mux
	engine  *iceengine.Engine
	session *icesession.PreparedSession
}

func newM5BPrepared(t *testing.T, mapped bool) *m5bStack {
	t.Helper()
	stack, err := buildM5BPrepared(mapped)
	if err != nil {
		t.Skipf("dual-stack loopback unavailable: %v", err)
	}
	t.Cleanup(stack.close)
	return stack
}

func buildM5BPrepared(mapped bool) (*m5bStack, error) {
	registry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		return nil, err
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = 0
	muxConfig.IPv4Policy = socketmux.FamilyRequired
	muxConfig.IPv6Policy = socketmux.FamilyRequired
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Bind = netip.MustParseAddr("::1")
	muxConfig.Classifier = iceadapter.NewClassifier(registry)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		return nil, err
	}
	config := iceengine.DefaultConfig(
		netip.MustParseAddr("127.0.0.1"),
		netip.MustParseAddr("::1"),
	)
	config.Session.Validation.AllowLoopback = true
	config.Session.Logger = muxConfig.Logger
	config.Logger = muxConfig.Logger
	if mapped {
		local4, _ := mux.LocalAddr(socketmux.IPv4)
		local6, _ := mux.LocalAddr(socketmux.IPv6)
		config.Session.SrflxObservations = []icesession.SrflxObservation{
			{
				Family: socketmux.IPv4, LocalEndpoint: local4,
				MappedEndpoint: netip.AddrPortFrom(netip.MustParseAddr("198.51.100.20"), 52001),
			},
			{
				Family: socketmux.IPv6, LocalEndpoint: local6,
				MappedEndpoint: netip.AddrPortFrom(netip.MustParseAddr("2001:db8::20"), 52002),
			},
		}
	}
	phases := icesession.NewPhaseManager()
	lease, err := phases.Acquire(context.Background(), icesession.ControlPhaseICE)
	if err != nil {
		_ = mux.Close()
		return nil, err
	}
	engine, err := iceengine.New(mux, registry, lease, config)
	if err != nil {
		lease.Release()
		_ = mux.Close()
		return nil, err
	}
	session, err := engine.Prepare(context.Background())
	if err != nil {
		_ = engine.Close()
		_ = mux.Close()
		return nil, err
	}
	return &m5bStack{mux: mux, engine: engine, session: session}, nil
}

func (s *m5bStack) close() {
	if s == nil {
		return
	}
	_ = s.session.Close()
	_ = s.engine.Close()
	_ = s.mux.Close()
}

func newM5BHost(t *testing.T) host.Host {
	t.Helper()
	result, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0", "/ip6/::1/tcp/0"),
		libp2p.NoTransports,
		libp2p.Transport(tcptransport.NewTCPTransport),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Close() })
	return result
}

func assertM5BDescription(t *testing.T, description icesession.Description, wantSrflx bool) {
	t.Helper()
	if description.Credentials.Ufrag == "" || description.Credentials.Pwd == "" {
		t.Fatal("missing Pion credentials")
	}
	seen := make(map[[2]uint8]bool)
	for _, candidate := range description.Candidates {
		seen[[2]uint8{uint8(candidate.Network), uint8(candidate.Type)}] = true
		if candidate.Type == icesession.CandidateTypeServerReflexive {
			if !candidate.RelatedAddress.IsValid() || candidate.RelatedPort == 0 {
				t.Fatal("srflx candidate lost related endpoint")
			}
		}
	}
	for _, networkType := range []icesession.NetworkType{icesession.NetworkUDP4, icesession.NetworkUDP6} {
		if !seen[[2]uint8{uint8(networkType), uint8(icesession.CandidateTypeHost)}] {
			t.Fatalf("missing host candidate for network %d", networkType)
		}
		if wantSrflx && !seen[[2]uint8{uint8(networkType), uint8(icesession.CandidateTypeServerReflexive)}] {
			t.Fatalf("missing srflx candidate for network %d", networkType)
		}
	}
}
