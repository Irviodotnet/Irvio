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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/iceengine"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestM5C1_FullIrvioDirectIPv4(t *testing.T) {
	runFullIrvioDirect(t, []icesession.NetworkType{icesession.NetworkUDP4}, "ip4", icesession.NetworkUDP4, nil)
}

func TestM5C1_FullIrvioDirectIPv6(t *testing.T) {
	runFullIrvioDirect(t, []icesession.NetworkType{icesession.NetworkUDP6}, "ip6", icesession.NetworkUDP6, nil)
}

func TestM5C1_FullIrvioDirectDualStack(t *testing.T) {
	runFullIrvioDirect(t, []icesession.NetworkType{icesession.NetworkUDP4, icesession.NetworkUDP6}, "ip4", 0, nil)
}

type fullIrvioDirectResult struct {
	InitiatorPeer, ResponderPeer     peer.ID
	InitiatorStack, ResponderStack   *m5bStack
	InitiatorICE, ResponderICE       *iceconnect.ConnectedSession
	InitiatorResult, ResponderResult iceconnect.Result
}

func runFullIrvioDirect(
	t *testing.T,
	networks []icesession.NetworkType,
	signalingFamily string,
	expected icesession.NetworkType,
	after func(*fullIrvioDirectResult),
) {
	t.Helper()
	initiatorHost := newM5BHost(t)
	responderHost := newM5BHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	address := hostAddressForFamily(t, responderHost.Addrs(), signalingFamily)
	if err := initiatorHost.Connect(ctx, peer.AddrInfo{ID: responderHost.ID(), Addrs: []multiaddr.Multiaddr{address}}); err != nil {
		t.Fatal(err)
	}

	initiatorStack := newM5C1Stack(t, networks...)
	var responderStack *m5bStack
	var responderMu sync.Mutex
	signalConfig := icesignal.DefaultConfig()
	signalConfig.Validation.AllowLoopback = true
	service, err := icesignal.NewService(
		responderHost,
		responderHost.ID(),
		icesignal.SessionFactoryFunc(func(context.Context, peer.ID, icesession.Description) (*icesession.PreparedSession, error) {
			stack, err := buildM5C1Prepared(networks...)
			if err != nil {
				return nil, err
			}
			responderMu.Lock()
			responderStack = stack
			responderMu.Unlock()
			return stack.session, nil
		}),
		icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		signalConfig,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	client, err := icesignal.NewClient(initiatorHost, signalConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	exchange, err := client.Exchange(ctx, responderHost.ID(), initiatorStack.session)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := service.NextAccepted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	responderMu.Lock()
	responder := responderStack
	responderMu.Unlock()
	if responder == nil || accepted.Local != responder.session {
		t.Fatal("responder PreparedSession was not handed off intact")
	}
	t.Cleanup(responder.close)

	connectConfig := iceconnect.DefaultConfig()
	connectConfig.Validation.AllowLoopback = true
	initiatorAttempt, err := iceconnect.New(
		initiatorStack.session,
		iceconnect.Binding{
			SessionID: exchange.SessionID, Initiator: exchange.Initiator, Responder: exchange.Responder,
			LocalPeer: initiatorHost.ID(), RemotePeer: exchange.RemotePeer, Purpose: exchange.Purpose,
			LocalRole: exchange.LocalRole, RemoteRole: exchange.RemoteRole,
		},
		iceconnect.RemoteState{
			SessionID: exchange.SessionID, PeerID: exchange.RemotePeer,
			Role: exchange.RemoteRole, Description: exchange.RemoteDescription,
		},
		connectConfig,
	)
	if err != nil {
		t.Fatal(err)
	}
	responderAttempt, err := iceconnect.New(
		accepted.Local,
		iceconnect.Binding{
			SessionID: accepted.SessionID, Initiator: accepted.Initiator, Responder: accepted.Responder,
			LocalPeer: responderHost.ID(), RemotePeer: accepted.RemotePeer, Purpose: accepted.Purpose,
			LocalRole: accepted.LocalRole, RemoteRole: accepted.RemoteRole,
		},
		iceconnect.RemoteState{
			SessionID: accepted.SessionID, PeerID: accepted.RemotePeer,
			Role: accepted.RemoteRole, Description: accepted.RemoteDescription,
		},
		connectConfig,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := initiatorAttempt.LoadRemoteCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if err := responderAttempt.LoadRemoteCandidates(ctx); err != nil {
		t.Fatal(err)
	}

	acceptedResult := make(chan m5c1Outcome, 1)
	go func() {
		connected, err := responderAttempt.Connect(ctx)
		acceptedResult <- m5c1Outcome{session: connected, err: err}
	}()
	waitM5C1State(t, ctx, responderAttempt, iceconnect.StateConnecting)
	initiatorConnected, err := initiatorAttempt.Connect(ctx)
	if err != nil {
		t.Fatalf("initiator Dial: %v", err)
	}
	responderOutcome := <-acceptedResult
	if responderOutcome.err != nil {
		t.Fatalf("responder Accept: %v", responderOutcome.err)
	}
	defer initiatorConnected.Close()
	defer responderOutcome.session.Close()
	a, b := initiatorConnected.Result(), responderOutcome.session.Result()
	if a.LocalEndpoint != b.RemoteEndpoint || a.RemoteEndpoint != b.LocalEndpoint || a.Network != b.Network {
		t.Fatalf("selected pairs are not mirrors: A=%+v B=%+v", a, b)
	}
	if expected != 0 && (a.Network != expected || b.Network != expected) {
		t.Fatalf("selected family A=%d B=%d want=%d", a.Network, b.Network, expected)
	}
	for _, result := range []iceconnect.Result{a, b} {
		if result.SelectedLocalCandidate.Type != ice.CandidateTypeHost ||
			result.SelectedRemoteCandidate.Type != ice.CandidateTypeHost ||
			!result.Nominated || result.PairState != ice.CandidatePairStateSucceeded ||
			result.RequestsSent == 0 || result.ResponsesReceived == 0 ||
			result.AdapterRequestsAfter <= result.AdapterRequestsBefore ||
			result.ApplicationPacketsSent != 0 || result.ApplicationPacketsRecv != 0 ||
			result.ApplicationBytesSent != 0 || result.ApplicationBytesRecv != 0 {
			t.Fatalf("incomplete direct connectivity evidence: %+v", result)
		}
	}
	if after != nil {
		after(&fullIrvioDirectResult{
			InitiatorPeer: initiatorHost.ID(), ResponderPeer: responderHost.ID(),
			InitiatorStack: initiatorStack, ResponderStack: responder,
			InitiatorICE: initiatorConnected, ResponderICE: responderOutcome.session,
			InitiatorResult: a, ResponderResult: b,
		})
	}
}

type m5c1Outcome struct {
	session *iceconnect.ConnectedSession
	err     error
}

func waitM5C1State(t *testing.T, ctx context.Context, attempt *iceconnect.Attempt, wanted iceconnect.State) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if attempt.State() == wanted {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for M5c1 state %d: %v", wanted, ctx.Err())
		case <-ticker.C:
		}
	}
}

func hostAddressForFamily(t *testing.T, addresses []multiaddr.Multiaddr, family string) multiaddr.Multiaddr {
	t.Helper()
	for _, address := range addresses {
		if strings.HasPrefix(address.String(), "/"+family+"/") {
			return address
		}
	}
	t.Fatalf("no %s libp2p address in %v", family, addresses)
	return nil
}

func newM5C1Stack(t *testing.T, networks ...icesession.NetworkType) *m5bStack {
	t.Helper()
	stack, err := buildM5C1Prepared(networks...)
	if err != nil {
		if len(networks) == 1 && networks[0] == icesession.NetworkUDP6 {
			t.Skipf("IPv6 loopback unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(stack.close)
	return stack
}

func buildM5C1Prepared(networks ...icesession.NetworkType) (*m5bStack, error) {
	registry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		return nil, err
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = 0
	muxConfig.IPv4Policy = socketmux.FamilyDisabled
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	addresses := make([]netip.Addr, 0, len(networks))
	for _, network := range networks {
		if network == icesession.NetworkUDP4 {
			muxConfig.IPv4Policy = socketmux.FamilyRequired
			muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
			addresses = append(addresses, muxConfig.IPv4Bind)
		} else {
			muxConfig.IPv6Policy = socketmux.FamilyRequired
			muxConfig.IPv6Bind = netip.MustParseAddr("::1")
			addresses = append(addresses, muxConfig.IPv6Bind)
		}
	}
	muxConfig.Classifier = iceadapter.NewClassifier(registry)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		return nil, err
	}
	engineConfig := iceengine.DefaultConfig(addresses...)
	engineConfig.Session.Validation.AllowLoopback = true
	engineConfig.Session.Logger = muxConfig.Logger
	engineConfig.Logger = muxConfig.Logger
	phases := icesession.NewPhaseManager()
	lease, err := phases.Acquire(context.Background(), icesession.ControlPhaseICE)
	if err != nil {
		_ = mux.Close()
		return nil, err
	}
	engine, err := iceengine.New(mux, registry, lease, engineConfig)
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
