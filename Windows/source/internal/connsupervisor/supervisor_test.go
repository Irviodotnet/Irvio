// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package connsupervisor

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

type fakeTimer struct {
	channel chan time.Time
	stopped atomic.Bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.channel }
func (t *fakeTimer) Stop() bool          { return t.stopped.CompareAndSwap(false, true) }
func (t *fakeTimer) fire() bool {
	if t.stopped.Load() {
		return false
	}
	t.channel <- time.Now()
	return true
}

type fakeClock struct{ created chan *fakeTimer }

func newFakeClock() *fakeClock { return &fakeClock{created: make(chan *fakeTimer, 32)} }
func (c *fakeClock) NewTimer(time.Duration) Timer {
	timer := &fakeTimer{channel: make(chan time.Time, 1)}
	c.created <- timer
	return timer
}

type connectResponse struct {
	session Session
	err     error
}

type connectCall struct {
	request  Request
	response chan connectResponse
}

type controlledConnector struct {
	calls        chan connectCall
	ignoreCancel bool
}

func (c *controlledConnector) Connect(ctx context.Context, request Request) (Session, error) {
	call := connectCall{request: request, response: make(chan connectResponse, 1)}
	select {
	case c.calls <- call:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if c.ignoreCancel {
		response := <-call.response
		return response.session, response.err
	}
	select {
	case response := <-call.response:
		return response.session, response.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type fakeSession struct {
	metadata SessionMetadata
	states   chan ice.ConnectionState
	closed   atomic.Bool
}

func (s *fakeSession) Metadata() SessionMetadata                { return s.metadata }
func (s *fakeSession) StateChanges() <-chan ice.ConnectionState { return s.states }
func (s *fakeSession) Close() error                             { s.closed.Store(true); return nil }

func TestSupervisorTransientDisconnectRecoversWithoutRestart(t *testing.T) {
	supervisor, connector, clock := newSupervisorHarness(t, false)
	call := waitCall(t, connector.calls)
	first := sessionFor(t, call.request)
	call.response <- connectResponse{session: first}
	waitEvent(t, supervisor.Events(), EventConnected)

	first.states <- ice.ConnectionStateDisconnected
	waitEvent(t, supervisor.Events(), EventDisconnected)
	grace := waitTimer(t, clock.created)
	first.states <- ice.ConnectionStateConnected
	waitEvent(t, supervisor.Events(), EventRecovered)
	if grace.fire() {
		t.Fatal("recovery did not cancel disconnected grace")
	}
	if got := supervisor.Current(); got.DesiredSessionGeneration != 1 || got.Active.SessionID != first.metadata.SessionID {
		t.Fatalf("transient loss replaced session: %+v", got)
	}
	select {
	case unexpected := <-connector.calls:
		t.Fatalf("transient recovery started generation %d", unexpected.request.SessionGeneration)
	default:
	}
}

func TestSupervisorFailedSessionCreatesFreshGeneration(t *testing.T) {
	supervisor, connector, _ := newSupervisorHarness(t, false)
	firstCall := waitCall(t, connector.calls)
	first := sessionFor(t, firstCall.request)
	firstCall.response <- connectResponse{session: first}
	waitEvent(t, supervisor.Events(), EventConnected)

	first.states <- ice.ConnectionStateFailed
	secondCall := waitCall(t, connector.calls)
	if secondCall.request.NetworkGeneration != firstCall.request.NetworkGeneration || secondCall.request.SessionGeneration != 2 || secondCall.request.Reason != ReasonICEFailed {
		t.Fatalf("wrong restart request: %+v", secondCall.request)
	}
	second := sessionFor(t, secondCall.request)
	if first.metadata.SessionID == second.metadata.SessionID || first.metadata.LocalUfrag == second.metadata.LocalUfrag {
		t.Fatal("fresh ICE generation reused identity or ufrag")
	}
	if first.closed.Load() {
		t.Fatal("old session was closed before replacement connected")
	}
	secondCall.response <- connectResponse{session: second}
	waitEvent(t, supervisor.Events(), EventRestartSucceeded)
	if !first.closed.Load() || supervisor.Current().Active.SessionID != second.metadata.SessionID {
		t.Fatal("atomic active-session replacement failed")
	}
}

func TestSupervisorExplicitRestartIsMakeBeforeBreak(t *testing.T) {
	supervisor, connector, _ := newSupervisorHarness(t, false)
	firstCall := waitCall(t, connector.calls)
	first := sessionFor(t, firstCall.request)
	firstCall.response <- connectResponse{session: first}
	waitEvent(t, supervisor.Events(), EventConnected)
	if err := supervisor.Restart(ReasonExplicitRestart); err != nil {
		t.Fatal(err)
	}
	secondCall := waitCall(t, connector.calls)
	if first.closed.Load() {
		t.Fatal("old connection broke before replacement")
	}
	second := sessionFor(t, secondCall.request)
	secondCall.response <- connectResponse{session: second}
	waitEvent(t, supervisor.Events(), EventRestartSucceeded)
	if !first.closed.Load() || second.closed.Load() {
		t.Fatal("make-before-break close ordering failed")
	}
}

func TestSupervisorOldFailureDoesNotCancelReplacement(t *testing.T) {
	supervisor, connector, _ := newSupervisorHarness(t, false)
	firstCall := waitCall(t, connector.calls)
	first := sessionFor(t, firstCall.request)
	firstCall.response <- connectResponse{session: first}
	waitEvent(t, supervisor.Events(), EventConnected)
	if err := supervisor.Restart(ReasonExplicitRestart); err != nil {
		t.Fatal(err)
	}
	secondCall := waitCall(t, connector.calls)
	first.states <- ice.ConnectionStateFailed
	second := sessionFor(t, secondCall.request)
	secondCall.response <- connectResponse{session: second}
	waitEvent(t, supervisor.Events(), EventRestartSucceeded)
	select {
	case unexpected := <-connector.calls:
		t.Fatalf("old failure spawned generation %d during replacement", unexpected.request.SessionGeneration)
	default:
	}
	if second.closed.Load() || supervisor.Current().Active.SessionID != second.metadata.SessionID {
		t.Fatal("old failure prevented replacement promotion")
	}
}

func TestSupervisorCoalescesRepeatedRestartTriggers(t *testing.T) {
	supervisor, connector, _ := newSupervisorHarness(t, false)
	firstCall := waitCall(t, connector.calls)
	firstCall.response <- connectResponse{session: sessionFor(t, firstCall.request)}
	waitEvent(t, supervisor.Events(), EventConnected)
	if err := supervisor.Restart(ReasonExplicitRestart); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Restart(ReasonExplicitRestart); err != nil {
		t.Fatal(err)
	}
	secondCall := waitCall(t, connector.calls)
	secondCall.response <- connectResponse{session: sessionFor(t, secondCall.request)}
	waitEvent(t, supervisor.Events(), EventRestartSucceeded)
	select {
	case unexpected := <-connector.calls:
		t.Fatalf("coalesced triggers started generation %d", unexpected.request.SessionGeneration)
	default:
	}
}

func TestSupervisorDiscardsStaleCompletion(t *testing.T) {
	supervisor, connector, _ := newSupervisorHarness(t, true)
	firstCall := waitCall(t, connector.calls)
	if err := supervisor.Restart(ReasonExplicitRestart); err != nil {
		t.Fatal(err)
	}
	secondCall := waitCall(t, connector.calls)
	second := sessionFor(t, secondCall.request)
	secondCall.response <- connectResponse{session: second}
	waitEvent(t, supervisor.Events(), EventRestartSucceeded)
	first := sessionFor(t, firstCall.request)
	firstCall.response <- connectResponse{session: first}
	waitEventReason(t, supervisor.Events(), EventRestartFailed, ReasonGenerationStale)
	if !first.closed.Load() || second.closed.Load() || supervisor.Current().Active.SessionID != second.metadata.SessionID {
		t.Fatal("stale completion overwrote the current generation")
	}
}

func TestSupervisorBackoffAndNetworkChangePreemption(t *testing.T) {
	supervisor, connector, clock := newSupervisorHarness(t, false)
	firstCall := waitCall(t, connector.calls)
	firstCall.response <- connectResponse{err: errors.New("connect failed")}
	waitEvent(t, supervisor.Events(), EventRestartFailed)
	waitEvent(t, supervisor.Events(), EventRestartScheduled)
	retry := waitTimer(t, clock.created)
	select {
	case call := <-connector.calls:
		t.Fatalf("retry bypassed backoff: %+v", call.request)
	default:
	}
	if err := supervisor.NetworkChanged(2); err != nil {
		t.Fatal(err)
	}
	secondCall := waitCall(t, connector.calls)
	if secondCall.request.NetworkGeneration != 2 || secondCall.request.Reason != ReasonNetworkChanged {
		t.Fatalf("network change did not preempt backoff: %+v", secondCall.request)
	}
	if retry.fire() {
		t.Fatal("network change did not cancel stale retry timer")
	}
	second := sessionFor(t, secondCall.request)
	secondCall.response <- connectResponse{session: second}
	waitEvent(t, supervisor.Events(), EventRestartSucceeded)
	if got := supervisor.Current(); got.NetworkGeneration != 2 || got.Active.NetworkGeneration != 2 {
		t.Fatalf("new network generation was not promoted: %+v", got)
	}
}

func TestSupervisorRetryTimerStartsFreshGeneration(t *testing.T) {
	supervisor, connector, clock := newSupervisorHarness(t, false)
	first := waitCall(t, connector.calls)
	first.response <- connectResponse{err: errors.New("connect failed")}
	waitEvent(t, supervisor.Events(), EventRestartScheduled)
	timer := waitTimer(t, clock.created)
	if !timer.fire() {
		t.Fatal("retry timer was unexpectedly stopped")
	}
	second := waitCall(t, connector.calls)
	if second.request.SessionGeneration != 2 {
		t.Fatalf("retry generation = %d", second.request.SessionGeneration)
	}
	connected := sessionFor(t, second.request)
	second.response <- connectResponse{session: connected}
	waitEvent(t, supervisor.Events(), EventRestartSucceeded)
}

func TestSupervisorRequestsTransportRefreshAfterBoundedFailures(t *testing.T) {
	local, remote := testPeer(t), testPeer(t)
	connector := &controlledConnector{calls: make(chan connectCall, 8)}
	clock := newFakeClock()
	config := DefaultConfig(Intent{
		LocalPeer: local, RemotePeer: remote, Purpose: icesession.PurposeExit, Role: icesession.RoleControlling,
	}, 1, connector)
	config.Clock = clock
	config.MaxSameGenerationFailures = 2
	supervisor, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = supervisor.Close() })

	first := waitCall(t, connector.calls)
	first.response <- connectResponse{err: WithFailureReason(ReasonSignalingFailed, errors.New("signal unavailable"))}
	waitEventReason(t, supervisor.Events(), EventRestartFailed, ReasonSignalingFailed)
	waitEvent(t, supervisor.Events(), EventRestartScheduled)
	timer := waitTimer(t, clock.created)
	if !timer.fire() {
		t.Fatal("bounded retry timer stopped unexpectedly")
	}
	second := waitCall(t, connector.calls)
	second.response <- connectResponse{err: errors.New("ICE unavailable")}
	waitEvent(t, supervisor.Events(), EventRestartFailed)
	refresh := waitEvent(t, supervisor.Events(), EventTransportRefreshRequested)
	if refresh.NetworkGeneration != 1 || refresh.SessionGeneration != 2 {
		t.Fatalf("wrong refresh boundary event: %+v", refresh)
	}
	select {
	case unexpected := <-clock.created:
		t.Fatalf("retry continued after refresh boundary: %+v", unexpected)
	default:
	}
}

func TestSupervisorCloseCancelsAttemptAndPreventsResurrection(t *testing.T) {
	supervisor, connector, _ := newSupervisorHarness(t, false)
	_ = waitCall(t, connector.calls)
	if err := supervisor.Close(); err != nil {
		t.Fatal(err)
	}
	if got := supervisor.Current(); got.State != StateClosed || got.HasActive {
		t.Fatalf("supervisor did not close: %+v", got)
	}
	if err := supervisor.Restart(ReasonExplicitRestart); !errors.Is(err, ErrClosed) {
		t.Fatalf("restart after close error = %v", err)
	}
	if err := supervisor.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorCloseDiscardsLateSuccessfulCompletion(t *testing.T) {
	supervisor, connector, _ := newSupervisorHarness(t, true)
	call := waitCall(t, connector.calls)
	late := sessionFor(t, call.request)
	closed := make(chan error, 1)
	go func() { closed <- supervisor.Close() }()
	call.response <- connectResponse{session: late}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor shutdown waited indefinitely for late completion")
	}
	if !late.closed.Load() || supervisor.Current().State != StateClosed || supervisor.Current().HasActive {
		t.Fatalf("late completion survived shutdown: closed=%t current=%+v", late.closed.Load(), supervisor.Current())
	}
}

func newSupervisorHarness(t *testing.T, ignoreCancel bool) (*Supervisor, *controlledConnector, *fakeClock) {
	t.Helper()
	local, remote := testPeer(t), testPeer(t)
	connector := &controlledConnector{calls: make(chan connectCall, 32), ignoreCancel: ignoreCancel}
	clock := newFakeClock()
	config := DefaultConfig(Intent{
		LocalPeer: local, RemotePeer: remote, Purpose: icesession.PurposeExit, Role: icesession.RoleControlling,
	}, 1, connector)
	config.Clock = clock
	config.DisconnectedGrace = time.Minute
	config.RetryInitial = time.Second
	config.RetryMaximum = 4 * time.Second
	supervisor, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ignoreCancel {
			for {
				select {
				case call := <-connector.calls:
					call.response <- connectResponse{err: context.Canceled}
				default:
					_ = supervisor.Close()
					return
				}
			}
		}
		_ = supervisor.Close()
	})
	return supervisor, connector, clock
}

func sessionFor(t *testing.T, request Request) *fakeSession {
	t.Helper()
	id, err := icesession.NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeSession{
		metadata: SessionMetadata{
			NetworkGeneration: request.NetworkGeneration, SessionGeneration: request.SessionGeneration,
			SessionID: id, RemotePeer: request.Intent.RemotePeer, Purpose: request.Intent.Purpose,
			LocalUfrag: fmt.Sprintf("ufrag-%d", request.SessionGeneration),
		},
		states: make(chan ice.ConnectionState, 8),
	}
}

func testPeer(t *testing.T) peer.ID {
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

func waitCall(t *testing.T, calls <-chan connectCall) connectCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connector call")
		return connectCall{}
	}
}

func waitTimer(t *testing.T, timers <-chan *fakeTimer) *fakeTimer {
	t.Helper()
	select {
	case timer := <-timers:
		return timer
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lifecycle timer")
		return nil
	}
}

func waitEvent(t *testing.T, events <-chan Event, wanted EventType) Event {
	t.Helper()
	return waitEventReason(t, events, wanted, 0)
}

func waitEventReason(t *testing.T, events <-chan Event, wanted EventType, reason Reason) Event {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("supervisor event channel closed")
			}
			if event.Type == wanted && (reason == 0 || event.Reason == reason) {
				return event
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for event %d reason %d", wanted, reason)
		}
	}
}

func TestSupervisorBoundedRandomOperations(t *testing.T) {
	for seed := int64(1); seed <= 32; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			local, remote := testPeer(t), testPeer(t)
			sessions := make(chan *fakeSession, 256)
			connector := ConnectorFunc(func(ctx context.Context, request Request) (Session, error) {
				if request.SessionGeneration%11 == 0 {
					return nil, WithFailureReason(ReasonSignalingFailed, errors.New("injected signaling failure"))
				}
				var id icesession.SessionID
				id[0] = 1
				binary.BigEndian.PutUint64(id[8:], uint64(request.SessionGeneration))
				session := &fakeSession{
					metadata: SessionMetadata{
						NetworkGeneration: request.NetworkGeneration, SessionGeneration: request.SessionGeneration,
						SessionID: id, RemotePeer: request.Intent.RemotePeer, Purpose: request.Intent.Purpose,
						LocalUfrag: fmt.Sprintf("random-%d", request.SessionGeneration),
					},
					states: make(chan ice.ConnectionState, 8),
				}
				select {
				case sessions <- session:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return session, nil
			})
			config := DefaultConfig(Intent{
				LocalPeer: local, RemotePeer: remote, Purpose: icesession.PurposeExit, Role: icesession.RoleControlling,
			}, 1, connector)
			config.EventCapacity = 8
			config.RetryInitial = time.Millisecond
			config.RetryMaximum = 4 * time.Millisecond
			config.DisconnectedGrace = time.Millisecond
			supervisor, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := supervisor.Start(context.Background()); err != nil {
				t.Fatal(err)
			}

			random := rand.New(rand.NewSource(seed))
			lastNetwork := networkstate.Generation(1)
			lastDesired := SessionGeneration(0)
			for operation := 0; operation < 128; operation++ {
				switch random.Intn(6) {
				case 0:
					_ = supervisor.Restart(ReasonExplicitRestart)
				case 1:
					lastNetwork++
					_ = supervisor.NetworkChanged(lastNetwork)
				case 2:
					_ = supervisor.NetworkChanged(1)
				case 3:
					select {
					case session := <-sessions:
						session.states <- ice.ConnectionStateDisconnected
						session.states <- ice.ConnectionStateConnected
					default:
					}
				case 4:
					select {
					case session := <-sessions:
						session.states <- ice.ConnectionStateFailed
					default:
					}
				case 5:
					current := supervisor.Current()
					if current.DesiredSessionGeneration < lastDesired {
						t.Fatalf("session generation regressed from %d to %d", lastDesired, current.DesiredSessionGeneration)
					}
					lastDesired = current.DesiredSessionGeneration
				}
			}
			if err := supervisor.Close(); err != nil {
				t.Fatal(err)
			}
			current := supervisor.Current()
			if current.State != StateClosed || current.HasActive {
				t.Fatalf("random lifecycle did not close terminally: %+v", current)
			}
			if !errors.Is(supervisor.Restart(ReasonExplicitRestart), ErrClosed) ||
				!errors.Is(supervisor.NetworkChanged(lastNetwork+1), ErrClosed) {
				t.Fatal("closed supervisor accepted a lifecycle operation")
			}
		})
	}
}

func TestSupervisorRejectsStaleNetworkNotification(t *testing.T) {
	supervisor, connector, _ := newSupervisorHarness(t, false)
	call := waitCall(t, connector.calls)
	session := sessionFor(t, call.request)
	call.response <- connectResponse{session: session}
	waitEvent(t, supervisor.Events(), EventConnected)
	if err := supervisor.NetworkChanged(1); err != nil {
		t.Fatal(err)
	}
	select {
	case unexpected := <-connector.calls:
		t.Fatalf("stale network event created request: %+v", unexpected.request)
	default:
	}
	if supervisor.Current().NetworkGeneration != networkstate.Generation(1) {
		t.Fatal("stale event changed current generation")
	}
}
