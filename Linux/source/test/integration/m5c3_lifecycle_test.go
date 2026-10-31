// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package integration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/iceengine"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestM5C3_FullIrvioFreshGenerationRestart(t *testing.T) {
	for _, test := range []struct {
		name     string
		networks []icesession.NetworkType
	}{
		{name: "IPv4", networks: []icesession.NetworkType{icesession.NetworkUDP4}},
		{name: "IPv6", networks: []icesession.NetworkType{icesession.NetworkUDP6}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pair := newLifecyclePair(t, test.networks...)
			supervisor := pair.newSupervisor(t)
			firstEvent := lifecycleEvent(t, supervisor.Events(), connsupervisor.EventConnected)
			firstRecord := pair.record(firstEvent.SessionGeneration)
			if err := supervisor.Restart(connsupervisor.ReasonExplicitRestart); err != nil {
				t.Fatal(err)
			}
			secondEvent := lifecycleEvent(t, supervisor.Events(), connsupervisor.EventRestartSucceeded)
			secondRecord := pair.record(secondEvent.SessionGeneration)
			if firstRecord.sessionID == secondRecord.sessionID || firstRecord.ufrag == secondRecord.ufrag || firstRecord.password == secondRecord.password || firstRecord.agent == secondRecord.agent {
				t.Fatal("fresh session generation reused SessionID, credentials, or Pion Agent")
			}
			if firstRecord.mux != secondRecord.mux || firstRecord.adapter != secondRecord.adapter || firstRecord.muxPort != secondRecord.muxPort {
				t.Fatal("same-network restart replaced shared transport ownership")
			}
			if !firstRecord.session.closed.Load() || secondRecord.session.closed.Load() {
				t.Fatal("make-before-break promotion closed the wrong generation")
			}
			if got := pair.initiator.engine.Stats(); got.ActiveSessions != 1 || got.PeakSessions < 2 {
				t.Fatalf("shared initiator engine did not overlap generations: %+v", got)
			}
			if current := supervisor.Current(); current.NetworkGeneration != 1 || current.Active.SessionID != secondRecord.sessionID {
				t.Fatalf("new generation not active: %+v", current)
			}
			if err := supervisor.Close(); err != nil {
				t.Fatal(err)
			}
			if !secondRecord.session.closed.Load() {
				t.Fatal("supervisor shutdown retained active ICE generation")
			}
		})
	}
}

func TestM5C3_ExplicitReconnectSoak(t *testing.T) {
	iterationsText := os.Getenv("HYDRA_M5C3_SOAK_ITERATIONS")
	if iterationsText == "" {
		t.Skip("set HYDRA_M5C3_SOAK_ITERATIONS for the bounded reconnect soak")
	}
	iterations, err := strconv.Atoi(iterationsText)
	if err != nil || iterations < 1 || iterations > 1000 {
		t.Fatalf("invalid HYDRA_M5C3_SOAK_ITERATIONS=%q", iterationsText)
	}
	pair := newLifecyclePair(t, icesession.NetworkUDP4)
	supervisor := pair.newSupervisor(t)
	lifecycleEvent(t, supervisor.Events(), connsupervisor.EventConnected)
	for range iterations {
		if err := supervisor.Restart(connsupervisor.ReasonExplicitRestart); err != nil {
			t.Fatal(err)
		}
		lifecycleEvent(t, supervisor.Events(), connsupervisor.EventRestartSucceeded)
	}
	if got := pair.initiator.engine.Stats(); got.ActiveSessions != 1 || got.SessionsCreated != uint64(iterations+1) || got.SessionsClosed != uint64(iterations) {
		t.Fatalf("reconnect soak leaked initiator sessions: %+v", got)
	}
	if got := pair.responder.engine.Stats(); got.ActiveSessions != 1 || got.SessionsCreated != uint64(iterations+1) || got.SessionsClosed != uint64(iterations) {
		t.Fatalf("reconnect soak leaked responder sessions: %+v", got)
	}
}

type lifecycleEngine struct {
	mux    *socketmux.Mux
	engine *iceengine.Engine
}

type lifecyclePair struct {
	initiator  *lifecycleEngine
	responder  *lifecycleEngine
	client     *icesignal.Client
	service    *icesignal.Service
	localPeer  peer.ID
	remotePeer peer.ID
	validation icesession.ValidationConfig

	connectMu sync.Mutex
	recordMu  sync.Mutex
	records   map[connsupervisor.SessionGeneration]lifecycleRecord
}

type lifecycleRecord struct {
	sessionID icesession.SessionID
	ufrag     string
	password  string
	agent     any
	mux       *socketmux.Mux
	adapter   *iceadapter.Adapter
	muxPort   uint16
	session   *lifecycleConnected
}

type lifecycleConnected struct {
	metadata  connsupervisor.SessionMetadata
	states    <-chan ice.ConnectionState
	initiator *iceconnect.ConnectedSession
	responder *iceconnect.ConnectedSession
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

func (s *lifecycleConnected) Metadata() connsupervisor.SessionMetadata { return s.metadata }
func (s *lifecycleConnected) StateChanges() <-chan ice.ConnectionState { return s.states }
func (s *lifecycleConnected) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.closeErr = errors.Join(s.initiator.Close(), s.responder.Close())
	})
	return s.closeErr
}

func newLifecyclePair(t *testing.T, networks ...icesession.NetworkType) *lifecyclePair {
	t.Helper()
	initiatorHost := newM5BHost(t)
	responderHost := newM5BHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := initiatorHost.Connect(ctx, peer.AddrInfo{ID: responderHost.ID(), Addrs: responderHost.Addrs()}); err != nil {
		t.Fatal(err)
	}
	initiator := newLifecycleEngine(t, networks...)
	responder := newLifecycleEngine(t, networks...)
	validation := icesession.DefaultValidationConfig()
	validation.AllowLoopback = true
	signalConfig := icesignal.DefaultConfig()
	signalConfig.Validation = validation
	signalConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := icesignal.NewService(
		responderHost, responderHost.ID(),
		icesignal.SessionFactoryFunc(func(ctx context.Context, _ peer.ID, _ icesession.Description) (*icesession.PreparedSession, error) {
			return responder.engine.Prepare(ctx)
		}),
		icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		signalConfig,
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := icesignal.NewClient(initiatorHost, signalConfig)
	if err != nil {
		t.Fatal(err)
	}
	pair := &lifecyclePair{
		initiator: initiator, responder: responder, client: client, service: service,
		localPeer: initiatorHost.ID(), remotePeer: responderHost.ID(), validation: validation,
		records: make(map[connsupervisor.SessionGeneration]lifecycleRecord),
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = service.Close()
		_ = initiator.engine.Close()
		_ = responder.engine.Close()
		_ = initiator.mux.Close()
		_ = responder.mux.Close()
	})
	return pair
}

func newLifecycleEngine(t *testing.T, networks ...icesession.NetworkType) *lifecycleEngine {
	t.Helper()
	registry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
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
		if len(networks) == 1 && networks[0] == icesession.NetworkUDP6 {
			t.Skipf("IPv6 loopback unavailable: %v", err)
		}
		t.Fatal(err)
	}
	phases := icesession.NewPhaseManager()
	lease, err := phases.Acquire(context.Background(), icesession.ControlPhaseICE)
	if err != nil {
		t.Fatal(err)
	}
	config := iceengine.DefaultConfig(addresses...)
	config.MaxSessions = 8
	config.Session.Validation.AllowLoopback = true
	config.Session.Logger = muxConfig.Logger
	config.Logger = muxConfig.Logger
	engine, err := iceengine.New(mux, registry, lease, config)
	if err != nil {
		lease.Release()
		_ = mux.Close()
		t.Fatal(err)
	}
	return &lifecycleEngine{mux: mux, engine: engine}
}

func (p *lifecyclePair) newSupervisor(t *testing.T) *connsupervisor.Supervisor {
	t.Helper()
	config := connsupervisor.DefaultConfig(connsupervisor.Intent{
		LocalPeer: p.localPeer, RemotePeer: p.remotePeer, Purpose: icesession.PurposeExit, Role: icesession.RoleControlling,
	}, networkstate.Generation(1), connsupervisor.ConnectorFunc(p.connect))
	config.DisconnectedGrace = 2 * time.Second
	supervisor, err := connsupervisor.New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = supervisor.Close() })
	return supervisor
}

func (p *lifecyclePair) connect(ctx context.Context, request connsupervisor.Request) (connsupervisor.Session, error) {
	p.connectMu.Lock()
	defer p.connectMu.Unlock()
	connectContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx = connectContext
	local, err := p.initiator.engine.Prepare(ctx)
	if err != nil {
		return nil, err
	}
	keepLocal := false
	defer func() {
		if !keepLocal {
			_ = local.Close()
		}
	}()
	exchange, err := p.client.Exchange(ctx, p.remotePeer, local)
	if err != nil {
		return nil, err
	}
	accepted, err := p.service.NextAccepted(ctx)
	if err != nil {
		return nil, err
	}
	keepRemote := false
	defer func() {
		if !keepRemote {
			_ = accepted.Local.Close()
		}
	}()
	connectConfig := iceconnect.DefaultConfig()
	connectConfig.Validation = p.validation
	localBinding := iceconnect.Binding{
		SessionID: exchange.SessionID, Initiator: exchange.Initiator, Responder: exchange.Responder,
		LocalPeer: p.localPeer, RemotePeer: p.remotePeer, Purpose: exchange.Purpose,
		LocalRole: exchange.LocalRole, RemoteRole: exchange.RemoteRole,
	}
	remoteBinding := iceconnect.Binding{
		SessionID: accepted.SessionID, Initiator: accepted.Initiator, Responder: accepted.Responder,
		LocalPeer: p.remotePeer, RemotePeer: p.localPeer, Purpose: accepted.Purpose,
		LocalRole: accepted.LocalRole, RemoteRole: accepted.RemoteRole,
	}
	localAttempt, err := iceconnect.New(local, localBinding, iceconnect.RemoteState{
		SessionID: exchange.SessionID, PeerID: p.remotePeer, Role: exchange.RemoteRole, Description: exchange.RemoteDescription,
	}, connectConfig)
	if err != nil {
		return nil, err
	}
	remoteAttempt, err := iceconnect.New(accepted.Local, remoteBinding, iceconnect.RemoteState{
		SessionID: accepted.SessionID, PeerID: p.localPeer, Role: accepted.RemoteRole, Description: accepted.RemoteDescription,
	}, connectConfig)
	if err != nil {
		_ = localAttempt.Close()
		return nil, err
	}
	if err := localAttempt.LoadRemoteCandidates(ctx); err != nil {
		_ = remoteAttempt.Close()
		return nil, err
	}
	if err := remoteAttempt.LoadRemoteCandidates(ctx); err != nil {
		_ = localAttempt.Close()
		_ = remoteAttempt.Close()
		return nil, err
	}
	remoteDone := make(chan struct {
		connected *iceconnect.ConnectedSession
		err       error
	}, 1)
	go func() {
		connected, err := remoteAttempt.Connect(ctx)
		remoteDone <- struct {
			connected *iceconnect.ConnectedSession
			err       error
		}{connected, err}
	}()
	localConnected, localErr := localAttempt.Connect(ctx)
	remoteResult := <-remoteDone
	if localErr != nil || remoteResult.err != nil {
		_ = localAttempt.Close()
		_ = remoteAttempt.Close()
		return nil, errors.Join(localErr, remoteResult.err)
	}
	keepLocal, keepRemote = true, true
	connected := &lifecycleConnected{
		metadata: connsupervisor.SessionMetadata{
			NetworkGeneration: request.NetworkGeneration, SessionGeneration: request.SessionGeneration,
			SessionID: exchange.SessionID, RemotePeer: p.remotePeer, Purpose: icesession.PurposeExit,
			LocalUfrag: local.LocalDescription().Credentials.Ufrag,
		},
		states: localAttempt.ConnectionStateChanges(), initiator: localConnected, responder: remoteResult.connected,
	}
	p.recordMu.Lock()
	p.records[request.SessionGeneration] = lifecycleRecord{
		sessionID: exchange.SessionID, ufrag: local.LocalDescription().Credentials.Ufrag,
		password: local.LocalDescription().Credentials.Pwd, agent: local.Agent(), mux: p.initiator.mux,
		adapter: p.initiator.engine.Adapter(), muxPort: p.initiator.mux.Port(), session: connected,
	}
	p.recordMu.Unlock()
	return connected, nil
}

func (p *lifecyclePair) record(generation connsupervisor.SessionGeneration) lifecycleRecord {
	p.recordMu.Lock()
	defer p.recordMu.Unlock()
	return p.records[generation]
}

func lifecycleEvent(t *testing.T, events <-chan connsupervisor.Event, wanted connsupervisor.EventType) connsupervisor.Event {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("lifecycle event channel closed")
			}
			if event.Type == wanted {
				return event
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for lifecycle event %d", wanted)
		}
	}
}
