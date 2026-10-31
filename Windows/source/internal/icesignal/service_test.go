// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesignal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/iceengine"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestAuthenticatedOfferAnswerDualStack(t *testing.T) {
	for _, listen := range []string{"/ip4/127.0.0.1/tcp/0", "/ip6/::1/tcp/0"} {
		t.Run(listen, func(t *testing.T) {
			initiatorHost := newSignalHost(t, listen)
			responderHost := newSignalHost(t, listen)
			connectSignalHosts(t, initiatorHost, responderHost)

			initiatorStack := newPreparedTestStack(t, true, true)
			var responderStack *preparedTestStack
			var stackMu sync.Mutex
			var logs synchronizedBuffer
			config := testConfig()
			config.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			service, err := NewService(
				responderHost, responderHost.ID(),
				SessionFactoryFunc(func(context.Context, peer.ID, icesession.Description) (*icesession.PreparedSession, error) {
					stack, err := buildPreparedTestStack(true, true)
					if err != nil {
						return nil, err
					}
					stackMu.Lock()
					responderStack = stack
					stackMu.Unlock()
					return stack.session, nil
				}),
				AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
				config,
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			client, err := NewClient(initiatorHost, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })

			before := initiatorStack.session.Adapter().Stats().OutboundTransactionsRegistered
			result, err := client.Exchange(context.Background(), responderHost.ID(), initiatorStack.session)
			if err != nil {
				if strings.Contains(listen, "/ip6/") {
					t.Skipf("IPv6 libp2p signaling unavailable: %v", err)
				}
				t.Fatal(err)
			}
			acceptedContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			accepted, err := service.NextAccepted(acceptedContext)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = accepted.Local.Close() })
			if result.SessionID != accepted.SessionID || result.RemotePeer != responderHost.ID() ||
				accepted.RemotePeer != initiatorHost.ID() {
				t.Fatal("session or authenticated peer binding changed")
			}
			if !equalDescription(result.RemoteDescription, accepted.Local.LocalDescription()) ||
				!equalDescription(accepted.RemoteDescription, initiatorStack.session.LocalDescription()) {
				t.Fatal("complete descriptions were not delivered intact")
			}
			if result.LocalRole != icesession.RoleControlling || result.RemoteRole != icesession.RoleControlled {
				t.Fatal("future ICE roles were not preserved")
			}
			if initiatorStack.session.Adapter().Stats().OutboundTransactionsRegistered != before ||
				accepted.Local.Adapter().Stats().OutboundTransactionsRegistered != 0 {
				t.Fatal("signaling started ICE connectivity checks")
			}
			assertCandidateFamiliesAndSrflx(t, result.RemoteDescription)
			passwords := []string{
				initiatorStack.session.LocalDescription().Credentials.Pwd,
				accepted.Local.LocalDescription().Credentials.Pwd,
			}
			for _, password := range passwords {
				if strings.Contains(logs.String(), password) {
					t.Fatal("ICE password appeared in structured logs")
				}
			}
			stackMu.Lock()
			stack := responderStack
			stackMu.Unlock()
			if stack == nil {
				t.Fatal("responder session factory was not invoked")
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			if stack.phases.Phase() != icesession.ControlPhaseICE {
				t.Fatal("service closed a handed-off PreparedSession")
			}
			if err := accepted.Local.Close(); err != nil {
				t.Fatal(err)
			}
			if stack.phases.Phase() != icesession.ControlPhaseICE {
				t.Fatal("session close released shared engine lease")
			}
			assertMuxDataPath(t, stack.mux)
			stack.close()
			if stack.phases.Phase() != icesession.ControlPhaseIdle {
				t.Fatal("engine close did not release responder lease")
			}
		})
	}
}

func TestServiceRejectsWrongIdentityExpiryAndReplay(t *testing.T) {
	initiatorHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
	responderHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
	connectSignalHosts(t, initiatorHost, responderHost)
	config := testConfig()
	config.MaxSessionsPerPeer = 1
	config.AcceptedQueue = 1

	var factoryCalls atomic.Uint64
	var stacksMu sync.Mutex
	var stacks []*preparedTestStack
	factory := SessionFactoryFunc(func(context.Context, peer.ID, icesession.Description) (*icesession.PreparedSession, error) {
		factoryCalls.Add(1)
		stack, err := buildPreparedTestStack(false, false)
		if err != nil {
			return nil, err
		}
		stacksMu.Lock()
		stacks = append(stacks, stack)
		stacksMu.Unlock()
		return stack.session, nil
	})
	service, err := NewService(
		responderHost, responderHost.ID(), factory,
		AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = service.Close()
		stacksMu.Lock()
		defer stacksMu.Unlock()
		for _, stack := range stacks {
			stack.close()
		}
	})
	client, err := NewClient(initiatorHost, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	other := testPeerID(t)
	base := testOffer(t, initiatorHost.ID(), responderHost.ID())
	cases := []struct {
		name string
		edit func(*Offer)
		code ErrorCode
	}{
		{"forged initiator", func(offer *Offer) { offer.Initiator = other }, ErrorWrongPeer},
		{"wrong responder", func(offer *Offer) { offer.Responder = other }, ErrorWrongPeer},
		{"expired", func(offer *Offer) {
			offer.CreatedAt = time.Now().Add(-2 * time.Minute)
			offer.ExpiresAt = time.Now().Add(-time.Minute)
		}, ErrorInvalidRequest},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			offer := base
			offer.SessionID, _ = icesession.NewSessionID()
			item.edit(&offer)
			_, err := client.exchangeOffer(context.Background(), responderHost.ID(), offer)
			var remoteError *RemoteError
			if !errors.As(err, &remoteError) || remoteError.Code != item.code {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if factoryCalls.Load() != 0 {
		t.Fatal("invalid offers allocated PreparedSessions")
	}

	replayOffer := testOffer(t, initiatorHost.ID(), responderHost.ID())
	if _, err := client.exchangeOffer(context.Background(), responderHost.ID(), replayOffer); err != nil {
		t.Fatal(err)
	}
	busyOffer := testOffer(t, initiatorHost.ID(), responderHost.ID())
	if _, err := client.exchangeOffer(context.Background(), responderHost.ID(), busyOffer); err == nil {
		t.Fatal("per-peer capacity accepted a second prepared session")
	} else {
		var remoteError *RemoteError
		if !errors.As(err, &remoteError) || remoteError.Code != ErrorBusy {
			t.Fatalf("capacity error = %v", err)
		}
	}
	if _, err := client.exchangeOffer(context.Background(), responderHost.ID(), replayOffer); err == nil {
		t.Fatal("replayed offer accepted")
	} else {
		var remoteError *RemoteError
		if !errors.As(err, &remoteError) || remoteError.Code != ErrorSessionConflict {
			t.Fatalf("replay error while session is queued: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	accepted, err := service.NextAccepted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := accepted.Local.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.exchangeOffer(context.Background(), responderHost.ID(), replayOffer); err == nil {
		t.Fatal("replayed offer accepted after capacity release")
	} else {
		var remoteError *RemoteError
		if !errors.As(err, &remoteError) || remoteError.Code != ErrorSessionConflict {
			t.Fatalf("replay error = %v", err)
		}
	}
	if factoryCalls.Load() != 1 {
		t.Fatalf("factory calls = %d", factoryCalls.Load())
	}
}

func TestAuthorizationAndSelfSession(t *testing.T) {
	initiatorHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
	responderHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
	connectSignalHosts(t, initiatorHost, responderHost)
	config := testConfig()
	secret := testDescription().Credentials.Pwd
	service, err := NewService(
		responderHost, responderHost.ID(),
		SessionFactoryFunc(func(context.Context, peer.ID, icesession.Description) (*icesession.PreparedSession, error) {
			t.Fatal("unauthorized offer reached session factory")
			return nil, nil
		}),
		AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return errors.New(secret) }),
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	client, err := NewClient(initiatorHost, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.exchangeOffer(context.Background(), responderHost.ID(), testOffer(t, initiatorHost.ID(), responderHost.ID()))
	var remoteError *RemoteError
	if !errors.As(err, &remoteError) || remoteError.Code != ErrorUnauthorized {
		t.Fatalf("authorization error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("authorization failure exposed an ICE password")
	}
	local := newPreparedTestStack(t, false, false)
	if _, err := client.Exchange(context.Background(), initiatorHost.ID(), local.session); !errors.Is(err, ErrWrongPeer) {
		t.Fatalf("self session error = %v", err)
	}
}

func TestClientRejectsForgedAndExpiredAnswer(t *testing.T) {
	for _, testCase := range []string{"forged responder", "expired"} {
		t.Run(testCase, func(t *testing.T) {
			initiatorHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
			responderHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
			other := testPeerID(t)
			config := testConfig()
			responderHost.SetStreamHandler(ProtocolID, func(stream network.Stream) {
				defer stream.Close()
				wire := new(irviov1.IceOffer)
				if readFrame(stream, wire) != nil {
					_ = stream.Reset()
					return
				}
				offer, err := offerFromProto(wire, config.Validation)
				if err != nil {
					_ = stream.Reset()
					return
				}
				now := time.Now().Truncate(time.Second)
				answer := Answer{
					SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: responderHost.ID(),
					Purpose: offer.Purpose, Role: icesession.RoleControlled,
					Description: testDescription(), CreatedAt: now, ExpiresAt: now.Add(time.Minute),
				}
				if testCase == "forged responder" {
					answer.Responder = other
				} else {
					answer.CreatedAt = now.Add(-2 * time.Minute)
					answer.ExpiresAt = now.Add(-time.Minute)
				}
				_ = writeFrame(stream, &irviov1.IceSignalResponse{
					Result: &irviov1.IceSignalResponse_Answer{Answer: answerToProto(answer)},
				})
			})
			connectSignalHosts(t, initiatorHost, responderHost)
			client, err := NewClient(initiatorHost, config)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			offer := testOffer(t, initiatorHost.ID(), responderHost.ID())
			if _, err := client.exchangeOffer(context.Background(), responderHost.ID(), offer); err == nil {
				t.Fatal("invalid answer accepted")
			}
		})
	}
}

func TestCancellationAndServiceCloseReleaseSessions(t *testing.T) {
	initiatorHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
	responderHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
	config := testConfig()
	received := make(chan struct{})
	release := make(chan struct{})
	responderHost.SetStreamHandler(ProtocolID, func(stream network.Stream) {
		defer stream.Reset()
		_ = readFrame(stream, new(irviov1.IceOffer))
		close(received)
		<-release
	})
	connectSignalHosts(t, initiatorHost, responderHost)
	client, err := NewClient(initiatorHost, config)
	if err != nil {
		t.Fatal(err)
	}
	offer := testOffer(t, initiatorHost.ID(), responderHost.ID())
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.exchangeOffer(ctx, responderHost.ID(), offer)
		result <- err
	}()
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("responder did not receive offer")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled exchange remained blocked")
	}
	close(release)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	secondResponder := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
	connectSignalHosts(t, initiatorHost, secondResponder)
	var built *preparedTestStack
	service, err := NewService(
		secondResponder, secondResponder.ID(),
		SessionFactoryFunc(func(context.Context, peer.ID, icesession.Description) (*icesession.PreparedSession, error) {
			stack, err := buildPreparedTestStack(false, false)
			built = stack
			if err != nil {
				return nil, err
			}
			return stack.session, nil
		}),
		AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondClient, _ := NewClient(initiatorHost, config)
	defer secondClient.Close()
	if _, err := secondClient.exchangeOffer(context.Background(), secondResponder.ID(), testOffer(t, initiatorHost.ID(), secondResponder.ID())); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if built == nil || built.phases.Phase() != icesession.ControlPhaseICE {
		t.Fatal("service close did not close unhanded PreparedSession")
	}
	assertMuxDataPath(t, built.mux)
	built.close()
	if built.phases.Phase() != icesession.ControlPhaseIdle {
		t.Fatal("engine close did not release unhanded responder lease")
	}
}

type preparedTestStack struct {
	mux     *socketmux.Mux
	phases  *icesession.PhaseManager
	engine  *iceengine.Engine
	session *icesession.PreparedSession
}

func newPreparedTestStack(t *testing.T, dual, mapped bool) *preparedTestStack {
	t.Helper()
	stack, err := buildPreparedTestStack(dual, mapped)
	if err != nil {
		if dual && strings.Contains(err.Error(), "IPv6") {
			t.Skipf("IPv6 loopback unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(stack.close)
	return stack
}

func buildPreparedTestStack(dual, mapped bool) (*preparedTestStack, error) {
	registry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		return nil, err
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = 0
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	addresses := []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	if dual {
		muxConfig.IPv6Policy = socketmux.FamilyRequired
		muxConfig.IPv6Bind = netip.MustParseAddr("::1")
		addresses = append(addresses, netip.MustParseAddr("::1"))
	}
	muxConfig.Classifier = iceadapter.NewClassifier(registry)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		return nil, err
	}
	phases := icesession.NewPhaseManager()
	engineConfig := iceengine.DefaultConfig(addresses...)
	engineConfig.Session.Validation.AllowLoopback = true
	engineConfig.Session.Logger = muxConfig.Logger
	engineConfig.Logger = muxConfig.Logger
	if mapped {
		v4, _ := mux.LocalAddr(socketmux.IPv4)
		engineConfig.Session.SrflxObservations = append(engineConfig.Session.SrflxObservations, icesession.SrflxObservation{
			Family: socketmux.IPv4, LocalEndpoint: v4,
			MappedEndpoint: netip.AddrPortFrom(netip.MustParseAddr("198.51.100.10"), 51001),
		})
		if dual {
			v6, _ := mux.LocalAddr(socketmux.IPv6)
			engineConfig.Session.SrflxObservations = append(engineConfig.Session.SrflxObservations, icesession.SrflxObservation{
				Family: socketmux.IPv6, LocalEndpoint: v6,
				MappedEndpoint: netip.AddrPortFrom(netip.MustParseAddr("2001:db8::10"), 51002),
			})
		}
	}
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
	return &preparedTestStack{mux: mux, phases: phases, engine: engine, session: session}, nil
}

func (s *preparedTestStack) close() {
	if s == nil {
		return
	}
	_ = s.session.Close()
	_ = s.engine.Close()
	_ = s.mux.Close()
}

func newSignalHost(t *testing.T, listen string) host.Host {
	t.Helper()
	result, err := libp2p.New(
		libp2p.ListenAddrStrings(listen),
		libp2p.NoTransports,
		libp2p.Transport(tcptransport.NewTCPTransport),
	)
	if err != nil {
		if strings.Contains(listen, "/ip6/") {
			t.Skipf("IPv6 libp2p unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Close() })
	return result
}

func connectSignalHosts(t *testing.T, first, second host.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := first.Connect(ctx, peer.AddrInfo{ID: second.ID(), Addrs: second.Addrs()}); err != nil {
		t.Fatal(err)
	}
}

func assertCandidateFamiliesAndSrflx(t *testing.T, description icesession.Description) {
	t.Helper()
	types := make(map[[2]uint8]bool)
	for _, candidate := range description.Candidates {
		types[[2]uint8{uint8(candidate.Network), uint8(candidate.Type)}] = true
		pionCandidate, err := icesession.CandidateToPion(candidate, testValidation())
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := icesession.CandidateFromPion(pionCandidate, testValidation())
		if err != nil {
			t.Fatal(err)
		}
		if roundTrip != candidate {
			t.Fatalf("candidate metadata changed: %+v != %+v", roundTrip, candidate)
		}
	}
	for _, key := range [][2]uint8{
		{uint8(icesession.NetworkUDP4), uint8(icesession.CandidateTypeHost)},
		{uint8(icesession.NetworkUDP4), uint8(icesession.CandidateTypeServerReflexive)},
		{uint8(icesession.NetworkUDP6), uint8(icesession.CandidateTypeHost)},
		{uint8(icesession.NetworkUDP6), uint8(icesession.CandidateTypeServerReflexive)},
	} {
		if !types[key] {
			t.Fatalf("candidate type/network missing: %v", key)
		}
	}
}

func assertMuxDataPath(t *testing.T, mux *socketmux.Mux) {
	t.Helper()
	receiver, err := socketmuxTestUDP()
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	target, _ := mux.LocalAddr(socketmux.IPv4)
	if _, err := receiver.WriteToUDPAddrPort([]byte("data-after-signaling"), target); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	packet, err := mux.Data().Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(packet.Payload) != "data-after-signaling" {
		t.Fatalf("data packet = %q", packet.Payload)
	}
}

func socketmuxTestUDP() (*net.UDPConn, error) {
	return net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
}
func TestServiceCloseCancelsPreparation(t *testing.T) {
	initiatorHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
	responderHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
	connectSignalHosts(t, initiatorHost, responderHost)
	config := testConfig()
	entered := make(chan struct{})
	var enterOnce sync.Once
	service, err := NewService(
		responderHost, responderHost.ID(),
		SessionFactoryFunc(func(ctx context.Context, _ peer.ID, _ icesession.Description) (*icesession.PreparedSession, error) {
			enterOnce.Do(func() { close(entered) })
			<-ctx.Done()
			return nil, ctx.Err()
		}),
		AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(initiatorHost, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result := make(chan error, 1)
	go func() {
		_, err := client.exchangeOffer(context.Background(), responderHost.ID(), testOffer(t, initiatorHost.ID(), responderHost.ID()))
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("session preparation did not start")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service Close remained blocked")
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("exchange succeeded while service was closing")
		}
	case <-time.After(time.Second):
		t.Fatal("exchange did not unblock")
	}
	if service.Stats().ActiveHandlers != 0 {
		t.Fatal("service leaked an active handler")
	}
}

func TestConcurrentAuthenticatedPeerPairs(t *testing.T) {
	const count = 4
	responderHost := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
	config := testConfig()
	config.MaxConcurrentOffers = count
	config.MaxSessionsPerPeer = 2
	config.AcceptedQueue = count

	var stacksMu sync.Mutex
	var responderStacks []*preparedTestStack
	t.Cleanup(func() {
		stacksMu.Lock()
		defer stacksMu.Unlock()
		for _, stack := range responderStacks {
			stack.close()
		}
	})
	service, err := NewService(
		responderHost, responderHost.ID(),
		SessionFactoryFunc(func(context.Context, peer.ID, icesession.Description) (*icesession.PreparedSession, error) {
			stack, err := buildPreparedTestStack(false, false)
			if err != nil {
				return nil, err
			}
			stacksMu.Lock()
			responderStacks = append(responderStacks, stack)
			stacksMu.Unlock()
			return stack.session, nil
		}),
		AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	type peerPair struct {
		client *Client
		stack  *preparedTestStack
	}
	pairs := make([]peerPair, 0, count)
	for range count {
		initiator := newSignalHost(t, "/ip4/127.0.0.1/tcp/0")
		connectSignalHosts(t, initiator, responderHost)
		client, err := NewClient(initiator, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		pairs = append(pairs, peerPair{client: client, stack: newPreparedTestStack(t, false, false)})
	}
	results := make(chan error, count)
	for _, pair := range pairs {
		pair := pair
		go func() {
			_, err := pair.client.Exchange(context.Background(), responderHost.ID(), pair.stack.session)
			results <- err
		}()
	}
	for range count {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	seen := make(map[peer.ID]bool, count)
	for range count {
		accepted, err := service.NextAccepted(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if seen[accepted.RemotePeer] {
			t.Fatal("duplicate remote peer handoff")
		}
		seen[accepted.RemotePeer] = true
		if accepted.Local.Adapter().Stats().OutboundTransactionsRegistered != 0 {
			t.Fatal("concurrent signaling started connectivity checks")
		}
		if err := accepted.Local.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != count || service.Stats().OffersSucceeded != count {
		t.Fatalf("concurrent results peers=%d stats=%+v", len(seen), service.Stats())
	}
}
