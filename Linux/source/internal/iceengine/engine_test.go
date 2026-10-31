// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceengine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"
	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

func TestEngineSharesOneAdapterAcrossSessions(t *testing.T) {
	stack := newEngineStack(t, true, nil, 4)
	sessions := make([]*icesession.PreparedSession, 3)
	ufrags := make(map[string]struct{}, 3)
	for i := range sessions {
		var err error
		sessions[i], err = stack.engine.Prepare(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if sessions[i].Adapter() != stack.engine.Adapter() {
			t.Fatal("session did not use the shared adapter")
		}
		ufrags[sessions[i].LocalDescription().Credentials.Ufrag] = struct{}{}
	}
	if len(ufrags) != len(sessions) {
		t.Fatal("Pion Agents reused a local ufrag")
	}
	stats := stack.engine.Stats()
	if stats.ActiveSessions != 3 || stats.PeakSessions != 3 || stats.Adapter.ActiveUfrags != 3 {
		t.Fatalf("unexpected shared engine stats: %+v", stats)
	}
	if err := sessions[0].Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sessions[1].Agent().GetLocalUserCredentials(); err != nil {
		t.Fatalf("closing one session affected another Agent: %v", err)
	}
	if stack.phases.Phase() != icesession.ControlPhaseICE {
		t.Fatal("session close released the transport ICE lease")
	}
	if got := stack.engine.Stats().ActiveSessions; got != 2 {
		t.Fatalf("active sessions = %d", got)
	}
	if err := stack.engine.Close(); err != nil {
		t.Fatal(err)
	}
	if stack.phases.Phase() != icesession.ControlPhaseIdle {
		t.Fatal("engine close did not release the ICE phase")
	}
	if _, err := stack.engine.Prepare(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("prepare after close error = %v", err)
	}
	assertDataUsable(t, stack.mux)
}

func TestEngineConnectsThreeSessionsWithoutCrossDelivery(t *testing.T) {
	exit := newEngineStack(t, false, nil, 8)
	exitPeer := engineTestPeer(t)
	type connectedPair struct {
		exit   *iceconnect.ConnectedSession
		client *iceconnect.ConnectedSession
	}
	pairs := make([]connectedPair, 3)
	clients := make([]*engineStack, 3)
	exitSessions := make([]*icesession.PreparedSession, 3)
	exitUfrags := make(map[string]struct{}, 3)
	for i := range clients {
		clients[i] = newEngineStack(t, false, nil, 2)
		clientPeer := engineTestPeer(t)
		exitSession, err := exit.engine.Prepare(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		clientSession, err := clients[i].engine.Prepare(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		exitSessions[i] = exitSession
		exitUfrags[exitSession.LocalDescription().Credentials.Ufrag] = struct{}{}
		sessionID, err := icesession.NewSessionID()
		if err != nil {
			t.Fatal(err)
		}
		clientBinding := iceconnect.Binding{
			SessionID: sessionID, Initiator: clientPeer, Responder: exitPeer,
			LocalPeer: clientPeer, RemotePeer: exitPeer, Purpose: icesession.PurposeExit,
			LocalRole: icesession.RoleControlling, RemoteRole: icesession.RoleControlled,
		}
		exitBinding := iceconnect.Binding{
			SessionID: sessionID, Initiator: clientPeer, Responder: exitPeer,
			LocalPeer: exitPeer, RemotePeer: clientPeer, Purpose: icesession.PurposeExit,
			LocalRole: icesession.RoleControlled, RemoteRole: icesession.RoleControlling,
		}
		if err := clientSession.BindConnectivity(bindingFromConnect(clientBinding)); err != nil {
			t.Fatal(err)
		}
		if err := exitSession.BindConnectivity(bindingFromConnect(exitBinding)); err != nil {
			t.Fatal(err)
		}
		validation := icesession.DefaultValidationConfig()
		validation.AllowLoopback = true
		connectConfig := iceconnect.DefaultConfig()
		connectConfig.Validation = validation
		clientAttempt, err := iceconnect.New(clientSession, clientBinding, iceconnect.RemoteState{
			SessionID: sessionID, PeerID: exitPeer, Role: icesession.RoleControlled, Description: exitSession.LocalDescription(),
		}, connectConfig)
		if err != nil {
			t.Fatal(err)
		}
		exitAttempt, err := iceconnect.New(exitSession, exitBinding, iceconnect.RemoteState{
			SessionID: sessionID, PeerID: clientPeer, Role: icesession.RoleControlling, Description: clientSession.LocalDescription(),
		}, connectConfig)
		if err != nil {
			t.Fatal(err)
		}
		if err := clientAttempt.LoadRemoteCandidates(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := exitAttempt.LoadRemoteCandidates(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exitDone := make(chan struct {
			connected *iceconnect.ConnectedSession
			err       error
		}, 1)
		go func() {
			connected, err := exitAttempt.Connect(ctx)
			exitDone <- struct {
				connected *iceconnect.ConnectedSession
				err       error
			}{connected, err}
		}()
		clientConnected, err := clientAttempt.Connect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		exitResult := <-exitDone
		if exitResult.err != nil {
			t.Fatal(exitResult.err)
		}
		pairs[i] = connectedPair{exit: exitResult.connected, client: clientConnected}
	}
	if len(exitUfrags) != 3 || exit.engine.Stats().ActiveSessions != 3 || exit.engine.Stats().Adapter.ActiveUfrags != 3 {
		t.Fatalf("sessions did not share one dispatcher correctly: ufrags=%d stats=%+v", len(exitUfrags), exit.engine.Stats())
	}
	if err := pairs[0].exit.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pairs[0].client.Close(); err != nil {
		t.Fatal(err)
	}
	for index := 1; index < len(pairs); index++ {
		pair, err := exitSessions[index].Agent().GetSelectedCandidatePair()
		if err != nil || pair == nil {
			t.Fatalf("closing session A affected session %d: pair=%v err=%v", index, pair, err)
		}
	}
	if exit.engine.Stats().ActiveSessions != 2 {
		t.Fatalf("closing one peer affected shared engine stats: %+v", exit.engine.Stats())
	}
	for index := 1; index < len(pairs); index++ {
		_ = pairs[index].exit.Close()
		_ = pairs[index].client.Close()
	}
}

func TestEngineReconnectsOneOfThreeClientsWithoutDisturbingOthers(t *testing.T) {
	exit := newEngineStack(t, false, nil, 8)
	exitPeer := engineTestPeer(t)
	clients := []*engineStack{
		newEngineStack(t, false, nil, 2),
		newEngineStack(t, false, nil, 2),
		newEngineStack(t, false, nil, 2),
	}
	clientPeers := []peer.ID{engineTestPeer(t), engineTestPeer(t), engineTestPeer(t)}
	connections := make([]enginePairConnection, len(clients))
	for index := range clients {
		connections[index] = connectEngineTestPair(t, exit, clients[index], exitPeer, clientPeers[index])
	}
	sharedAdapter := exit.engine.Adapter()
	unaffectedAgentB := connections[1].exitPrepared.Agent()
	unaffectedAgentC := connections[2].exitPrepared.Agent()
	oldUfrag := connections[0].exitPrepared.LocalDescription().Credentials.Ufrag
	if err := connections[0].Close(); err != nil {
		t.Fatal(err)
	}
	replacement := connectEngineTestPair(t, exit, clients[0], exitPeer, clientPeers[0])
	if replacement.exitPrepared.LocalDescription().Credentials.Ufrag == oldUfrag {
		t.Fatal("fresh client generation reused its local ufrag")
	}
	if exit.engine.Adapter() != sharedAdapter || exit.engine.Stats().ActiveSessions != 3 {
		t.Fatalf("one-client reconnect replaced or disturbed the shared engine: %+v", exit.engine.Stats())
	}
	for index, agent := range []*ice.Agent{unaffectedAgentB, unaffectedAgentC} {
		if connections[index+1].exitPrepared.Agent() != agent {
			t.Fatalf("unrelated client %d Agent was recreated", index+1)
		}
		pair, err := agent.GetSelectedCandidatePair()
		if err != nil || pair == nil {
			t.Fatalf("unrelated client %d lost its selected pair: pair=%v err=%v", index+1, pair, err)
		}
	}
	_ = replacement.Close()
	_ = connections[1].Close()
	_ = connections[2].Close()
}

type enginePairConnection struct {
	exitPrepared    *icesession.PreparedSession
	clientPrepared  *icesession.PreparedSession
	exitConnected   *iceconnect.ConnectedSession
	clientConnected *iceconnect.ConnectedSession
}

func (c *enginePairConnection) Close() error {
	return errors.Join(c.exitConnected.Close(), c.clientConnected.Close())
}

func connectEngineTestPair(t *testing.T, exit, client *engineStack, exitPeer, clientPeer peer.ID) enginePairConnection {
	t.Helper()
	exitPrepared, err := exit.engine.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	clientPrepared, err := client.engine.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := icesession.NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	clientBinding := iceconnect.Binding{
		SessionID: sessionID, Initiator: clientPeer, Responder: exitPeer,
		LocalPeer: clientPeer, RemotePeer: exitPeer, Purpose: icesession.PurposeExit,
		LocalRole: icesession.RoleControlling, RemoteRole: icesession.RoleControlled,
	}
	exitBinding := iceconnect.Binding{
		SessionID: sessionID, Initiator: clientPeer, Responder: exitPeer,
		LocalPeer: exitPeer, RemotePeer: clientPeer, Purpose: icesession.PurposeExit,
		LocalRole: icesession.RoleControlled, RemoteRole: icesession.RoleControlling,
	}
	if err := clientPrepared.BindConnectivity(bindingFromConnect(clientBinding)); err != nil {
		t.Fatal(err)
	}
	if err := exitPrepared.BindConnectivity(bindingFromConnect(exitBinding)); err != nil {
		t.Fatal(err)
	}
	connectConfig := iceconnect.DefaultConfig()
	connectConfig.Validation = exit.validation
	clientAttempt, err := iceconnect.New(clientPrepared, clientBinding, iceconnect.RemoteState{
		SessionID: sessionID, PeerID: exitPeer, Role: icesession.RoleControlled, Description: exitPrepared.LocalDescription(),
	}, connectConfig)
	if err != nil {
		t.Fatal(err)
	}
	exitAttempt, err := iceconnect.New(exitPrepared, exitBinding, iceconnect.RemoteState{
		SessionID: sessionID, PeerID: clientPeer, Role: icesession.RoleControlling, Description: clientPrepared.LocalDescription(),
	}, connectConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientAttempt.LoadRemoteCandidates(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := exitAttempt.LoadRemoteCandidates(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exitDone := make(chan struct {
		connected *iceconnect.ConnectedSession
		err       error
	}, 1)
	go func() {
		connected, err := exitAttempt.Connect(ctx)
		exitDone <- struct {
			connected *iceconnect.ConnectedSession
			err       error
		}{connected, err}
	}()
	clientConnected, err := clientAttempt.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exitResult := <-exitDone
	if exitResult.err != nil {
		t.Fatal(exitResult.err)
	}
	return enginePairConnection{
		exitPrepared: exitPrepared, clientPrepared: clientPrepared,
		exitConnected: exitResult.connected, clientConnected: clientConnected,
	}
}

func TestEngineBoundsSessionsWithoutEviction(t *testing.T) {
	stack := newEngineStack(t, false, nil, 2)
	first, err := stack.engine.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := stack.engine.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stack.engine.Prepare(context.Background()); !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("limit error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := stack.engine.Prepare(context.Background())
	if err != nil {
		t.Fatalf("closed slot was not reusable: %v", err)
	}
	_ = second.Close()
	_ = third.Close()
}

func TestEnginePreparesHostAndIrvioSrflxCandidates(t *testing.T) {
	stack := newEngineStack(t, true, []icesession.SrflxObservation{
		{Family: socketmux.IPv4, LocalEndpoint: netip.MustParseAddrPort("127.0.0.1:41000"), MappedEndpoint: netip.MustParseAddrPort("198.51.100.10:51001")},
		{Family: socketmux.IPv6, LocalEndpoint: netip.MustParseAddrPort("[::1]:41000"), MappedEndpoint: netip.MustParseAddrPort("[2001:db8::10]:51002")},
	}, 4)
	session, err := stack.engine.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	description := session.LocalDescription()
	if len(description.Candidates) != 4 {
		t.Fatalf("candidate count = %d", len(description.Candidates))
	}
	if stack.engine.Adapter().Stats().OutboundTransactionsRegistered != 0 {
		t.Fatal("host gathering started ICE connectivity transactions")
	}
	for _, candidate := range description.Candidates {
		if candidate.Type == icesession.CandidateTypeHost && candidate.Port != stack.mux.Port() {
			t.Fatalf("host port = %d, mux port = %d", candidate.Port, stack.mux.Port())
		}
		if candidate.Type == icesession.CandidateTypeServerReflexive {
			if !candidate.RelatedAddress.IsValid() || candidate.RelatedPort != stack.mux.Port() {
				t.Fatalf("invalid srflx related endpoint: %+v", candidate)
			}
			pionCandidate, err := icesession.CandidateToPion(candidate, stack.validation)
			if err != nil || pionCandidate.Foundation() != candidate.Foundation || pionCandidate.Priority() != candidate.Priority {
				t.Fatalf("detached srflx reconstruction failed: %v", err)
			}
		}
	}
}

func TestEngineSuppressesNoNATDuplicate(t *testing.T) {
	stack := newEngineStack(t, false, []icesession.SrflxObservation{{
		Family: socketmux.IPv4, LocalEndpoint: netip.MustParseAddrPort("127.0.0.1:41000"), MappedEndpoint: netip.MustParseAddrPort("127.0.0.1:41000"),
	}}, 2)
	session, err := stack.engine.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count := len(session.LocalDescription().Candidates); count != 1 {
		t.Fatalf("no-NAT candidate count = %d", count)
	}
}

func TestEngineConcurrentSessionClose(t *testing.T) {
	stack := newEngineStack(t, false, nil, 2)
	session, err := stack.engine.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	const callers = 16
	results := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- session.Close()
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := stack.engine.Stats().ActiveSessions; got != 0 {
		t.Fatalf("active sessions = %d", got)
	}
}

func TestRealSTUNToSharedICEEnginePhaseTransition(t *testing.T) {
	stunRegistry, err := stunprobe.NewRegistry(stunprobe.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	iceRegistry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = 0
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	muxConfig.Classifier = socketmux.NewClassifierChain(stunprobe.NewClassifier(stunRegistry), iceadapter.NewClassifier(iceRegistry))
	muxConfig.Logger = discardLogger()
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()

	server, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	serverDone := make(chan error, 1)
	go serveOneBinding(server, serverDone)

	phases := icesession.NewPhaseManager()
	stunLease, err := phases.Acquire(context.Background(), icesession.ControlPhaseSTUN)
	if err != nil {
		t.Fatal(err)
	}
	clientConfig := stunprobe.DefaultConfig()
	clientConfig.InitialRTO = 50 * time.Millisecond
	clientConfig.MaxRTO = 100 * time.Millisecond
	clientConfig.MaxAttempts = 2
	client, err := stunprobe.NewClient(mux.Control(), stunRegistry, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Probe(context.Background(), server.LocalAddr().(*net.UDPAddr).AddrPort())
	if err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	stunLease.Release()

	iceLease, err := phases.Acquire(context.Background(), icesession.ControlPhaseICE)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig(netip.MustParseAddr("127.0.0.1"))
	config.Session.Validation.AllowLoopback = true
	config.Session.Logger = muxConfig.Logger
	config.Session.SrflxObservations = []icesession.SrflxObservation{{Family: socketmux.IPv4, LocalEndpoint: result.Local, MappedEndpoint: result.Mapped}}
	engine, err := New(mux, iceRegistry, iceLease, config)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := engine.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.LocalDescription().Candidates) != 1 {
		t.Fatal("no-NAT STUN observation was not suppressed")
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if phases.Phase() != icesession.ControlPhaseICE {
		t.Fatal("session close released shared ICE phase")
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if phases.Phase() != icesession.ControlPhaseIdle {
		t.Fatal("engine close did not release shared ICE phase")
	}
	assertDataUsable(t, mux)
}

type engineStack struct {
	mux        *socketmux.Mux
	phases     *icesession.PhaseManager
	engine     *Engine
	validation icesession.ValidationConfig
}

func newEngineStack(t *testing.T, dual bool, observations []icesession.SrflxObservation, maxSessions int) *engineStack {
	t.Helper()
	registry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
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
	muxConfig.Logger = discardLogger()
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		if dual {
			t.Skipf("IPv6 loopback unavailable: %v", err)
		}
		t.Fatal(err)
	}
	validation := icesession.DefaultValidationConfig()
	validation.AllowLoopback = true
	normalized := append([]icesession.SrflxObservation(nil), observations...)
	for index := range normalized {
		originalLocal := normalized[index].LocalEndpoint
		if originalLocal.Port() == 41000 {
			normalized[index].LocalEndpoint = netip.AddrPortFrom(originalLocal.Addr(), mux.Port())
			if normalized[index].MappedEndpoint == originalLocal {
				normalized[index].MappedEndpoint = normalized[index].LocalEndpoint
			}
		}
	}
	config := DefaultConfig(addresses...)
	config.MaxSessions = maxSessions
	config.Session.SrflxObservations = normalized
	config.Session.Validation = validation
	config.Session.Logger = muxConfig.Logger
	config.Logger = muxConfig.Logger
	phases := icesession.NewPhaseManager()
	lease, err := phases.Acquire(context.Background(), icesession.ControlPhaseICE)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(mux, registry, lease, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = engine.Close()
		_ = mux.Close()
	})
	return &engineStack{mux: mux, phases: phases, engine: engine, validation: validation}
}

func serveOneBinding(server *net.UDPConn, done chan<- error) {
	buffer := make([]byte, 2048)
	count, source, err := server.ReadFromUDPAddrPort(buffer)
	if err != nil {
		done <- err
		return
	}
	request := stun.NewWithOptions(stun.WithStrict(true))
	if err := request.UnmarshalBinary(buffer[:count]); err != nil {
		done <- err
		return
	}
	response := stun.MustBuild(stun.BindingSuccess)
	response.TransactionID = request.TransactionID
	response.WriteTransactionID()
	mapped := stun.XORMappedAddress{IP: net.IP(source.Addr().AsSlice()), Port: int(source.Port())}
	if err := mapped.AddTo(response); err != nil {
		done <- err
		return
	}
	_, err = server.WriteToUDPAddrPort(response.Raw, source)
	done <- err
}

func assertDataUsable(t *testing.T, mux *socketmux.Mux) {
	t.Helper()
	sender, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	local, _ := mux.LocalAddr(socketmux.IPv4)
	if _, err := sender.WriteToUDPAddrPort([]byte("data-after-engine"), local); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	datagram, err := mux.Data().Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(datagram.Payload) != "data-after-engine" {
		t.Fatalf("data payload = %q", datagram.Payload)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func engineTestPeer(t *testing.T) peer.ID {
	t.Helper()
	privateKey, _, err := libcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func bindingFromConnect(binding iceconnect.Binding) icesession.ConnectivityBinding {
	return icesession.ConnectivityBinding{
		SessionID: binding.SessionID, Initiator: binding.Initiator, Responder: binding.Responder,
		LocalPeer: binding.LocalPeer, RemotePeer: binding.RemotePeer, Purpose: binding.Purpose,
		LocalRole: binding.LocalRole, RemoteRole: binding.RemoteRole,
	}
}
