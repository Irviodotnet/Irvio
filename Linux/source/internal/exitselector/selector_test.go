// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

type manualTimer struct {
	duration time.Duration
	channel  chan time.Time
	stopped  atomic.Bool
}

func (t *manualTimer) C() <-chan time.Time { return t.channel }
func (t *manualTimer) Stop() bool          { return t.stopped.CompareAndSwap(false, true) }
func (t *manualTimer) fire(at time.Time) bool {
	if t.stopped.Load() {
		return false
	}
	t.channel <- at
	return true
}

type manualClock struct {
	mu      sync.Mutex
	now     time.Time
	created chan *manualTimer
}

func newManualClock(now time.Time) *manualClock {
	return &manualClock{now: now, created: make(chan *manualTimer, 128)}
}
func (c *manualClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *manualClock) NewTimer(duration time.Duration) Timer {
	timer := &manualTimer{duration: duration, channel: make(chan time.Time, 1)}
	c.created <- timer
	return timer
}
func (c *manualClock) advance(duration time.Duration) time.Time {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	result := c.now
	c.mu.Unlock()
	return result
}
func (c *manualClock) nextLiveTimer(t *testing.T, expected time.Duration) *manualTimer {
	t.Helper()
	for {
		select {
		case timer := <-c.created:
			if timer.stopped.Load() {
				continue
			}
			if expected > 0 && timer.duration != expected {
				t.Fatalf("timer duration = %s, want %s", timer.duration, expected)
			}
			return timer
		case <-time.After(time.Second):
			t.Fatal("timer was not created")
			return nil
		}
	}
}

type providerResponse struct {
	batch CandidateBatch
	err   error
}
type providerCall struct {
	ctx      context.Context
	request  CandidateRequest
	response chan providerResponse
}
type controlledProvider struct {
	calls        chan providerCall
	ignoreCancel bool
}

func (p *controlledProvider) GetCandidates(ctx context.Context, request CandidateRequest) (CandidateBatch, error) {
	call := providerCall{ctx: ctx, request: request, response: make(chan providerResponse, 1)}
	select {
	case p.calls <- call:
	case <-ctx.Done():
		return CandidateBatch{}, ctx.Err()
	}
	if p.ignoreCancel {
		response := <-call.response
		return response.batch, response.err
	}
	select {
	case response := <-call.response:
		return response.batch, response.err
	case <-ctx.Done():
		return CandidateBatch{}, ctx.Err()
	}
}

type connectorCall struct {
	ctx       context.Context
	candidate Candidate
	response  chan connectorResponse
}
type controlledExitConnector struct {
	calls        chan connectorCall
	ignoreCancel bool
}

func (c *controlledExitConnector) Connect(ctx context.Context, candidate Candidate) (ConnectResult, error) {
	call := connectorCall{ctx: ctx, candidate: candidate, response: make(chan connectorResponse, 1)}
	select {
	case c.calls <- call:
	case <-ctx.Done():
		return ConnectResult{}, ctx.Err()
	}
	if c.ignoreCancel {
		response := <-call.response
		return response.result, response.err
	}
	select {
	case response := <-call.response:
		return response.result, response.err
	case <-ctx.Done():
		return ConnectResult{}, ctx.Err()
	}
}

type fakeExitConnection struct {
	events chan ConnectionEvent
	closed atomic.Bool
}

func newFakeExitConnection() *fakeExitConnection {
	return &fakeExitConnection{events: make(chan ConnectionEvent, 8)}
}
func (c *fakeExitConnection) Events() <-chan ConnectionEvent { return c.events }
func (c *fakeExitConnection) Close() error                   { c.closed.Store(true); return nil }

type promotableExitConnection struct {
	*fakeExitConnection
	promotions atomic.Uint64
	err        error
	deadlines  chan time.Time
}

func newPromotableExitConnection(err error) *promotableExitConnection {
	return &promotableExitConnection{fakeExitConnection: newFakeExitConnection(), err: err}
}

func (c *promotableExitConnection) Promote(ctx context.Context) error {
	c.promotions.Add(1)
	if c.deadlines != nil {
		deadline, _ := ctx.Deadline()
		c.deadlines <- deadline
	}
	return c.err
}

type timedPromotableExitConnection struct {
	*promotableExitConnection
	timeout time.Duration
}

func (c *timedPromotableExitConnection) PromotionTimeout() time.Duration { return c.timeout }

type latencyProbe struct {
	mu        sync.RWMutex
	latencies map[uint64]time.Duration
	now       func() time.Time
}

func (p *latencyProbe) Probe(_ context.Context, candidate Candidate) (ProbeResult, error) {
	p.mu.RLock()
	latency := p.latencies[candidate.NetworkEpoch]
	p.mu.RUnlock()
	return ProbeResult{Reachable: true, Latency: latency, ObservedAt: p.now(), NetworkEpoch: candidate.NetworkEpoch}, nil
}
func (p *latencyProbe) set(epoch uint64, latency time.Duration) {
	p.mu.Lock()
	p.latencies[epoch] = latency
	p.mu.Unlock()
}

type selectorHarness struct {
	selector  *Selector
	provider  *controlledProvider
	connector *controlledExitConnector
	clock     *manualClock
	probe     *latencyProbe
	now       time.Time
}

func newSelectorHarness(t *testing.T, ignoreConnectCancel bool) *selectorHarness {
	t.Helper()
	now := time.Unix(1000, 0).UTC()
	clock := newManualClock(now)
	provider := &controlledProvider{calls: make(chan providerCall, 32)}
	connector := &controlledExitConnector{calls: make(chan connectorCall, 32), ignoreCancel: ignoreConnectCancel}
	probe := &latencyProbe{latencies: make(map[uint64]time.Duration), now: clock.Now}
	localID := selectorPeer(t, 250)
	config := DefaultConfig(localID, "DE", provider, probe, connector, func() bool { return true }, UnderlayState{NetworkGeneration: 1, IPv4Usable: true})
	config.Clock = clock
	config.Random = &sequenceRandom{}
	config.LatencyTolerance = time.Millisecond
	selector, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = selector.Close() })
	if err := selector.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return &selectorHarness{selector: selector, provider: provider, connector: connector, clock: clock, probe: probe, now: now}
}

func (h *selectorHarness) candidates(t *testing.T, seeds ...byte) []Candidate {
	t.Helper()
	result := make([]Candidate, len(seeds))
	for index, seed := range seeds {
		result[index] = selectorCandidate(t, seed, h.now)
		h.probe.set(uint64(seed), time.Duration(seed)*time.Millisecond)
	}
	return result
}
func (h *selectorHarness) respondBatch(t *testing.T, candidates ...Candidate) providerCall {
	t.Helper()
	call := waitProviderCall(t, h.provider.calls)
	call.response <- providerResponse{batch: CandidateBatch{Country: call.request.Country, AvailablePeerCount: len(candidates), GeneratedAt: h.clock.Now(), Candidates: candidates}}
	return call
}

func waitProviderCall(t *testing.T, calls <-chan providerCall) providerCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("CandidateProvider was not called")
		return providerCall{}
	}
}
func waitConnectorCall(t *testing.T, calls <-chan connectorCall) connectorCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("Exit Connector was not called")
		return connectorCall{}
	}
}
func waitSelectorEvent(t *testing.T, events <-chan Event, kind EventType) Event {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == kind {
				return event
			}
		case <-deadline.C:
			t.Fatalf("selector event %d not observed", kind)
		}
	}
}
func connectSuccess(call connectorCall, connection Connection) {
	call.response <- connectorResponse{result: ConnectResult{Outcome: OutcomeConnected, Connection: connection}}
}
func connectFailure(call connectorCall, outcome ConnectOutcome) {
	call.response <- connectorResponse{result: ConnectResult{Outcome: outcome}, err: &OutcomeError{Outcome: outcome, Err: errors.New("fixture failure")}}
}

func TestSelectorInitialSelectionAndHealthyActiveIsSticky(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	a, b, c := candidates[0], candidates[1], candidates[2]
	h.respondBatch(t, a, b, c)
	initial := waitConnectorCall(t, h.connector.calls)
	if initial.candidate.PeerID != a.PeerID {
		t.Fatal("best candidate was not attempted first")
	}
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)

	d := h.candidates(t, 4)[0]
	h.probe.set(d.NetworkEpoch, time.Millisecond/2)
	if err := h.selector.RefreshNow(); err != nil {
		t.Fatal(err)
	}
	h.respondBatch(t, d, b, c)
	waitSelectorEvent(t, h.selector.Events(), EventBackgroundRefreshCompleted)
	snapshot := h.selector.Current()
	if snapshot.Active == nil || snapshot.Active.Candidate.PeerID != a.PeerID || snapshot.Reserve == nil || snapshot.Reserve.Candidate.PeerID != d.PeerID {
		t.Fatalf("healthy ACTIVE was not sticky: %+v", snapshot)
	}
	select {
	case call := <-h.connector.calls:
		t.Fatalf("background refresh connected %s", call.candidate.PeerID)
	default:
	}
}

func TestSelectorActiveFailureReserveBusyThenCold(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	connectionA.events <- ConnectionEvent{Type: ConnectionFailed, At: h.clock.Now()}
	recovery := waitConnectorCall(t, h.connector.calls)
	if recovery.candidate.PeerID != candidates[0].PeerID {
		t.Fatal("ACTIVE did not receive same-peer recovery first")
	}
	connectFailure(recovery, OutcomeUnavailable)
	reserve := waitConnectorCall(t, h.connector.calls)
	if reserve.candidate.PeerID != candidates[1].PeerID {
		t.Fatal("RESERVE was not attempted after ACTIVE")
	}
	connectFailure(reserve, OutcomeBusy)
	cold := waitConnectorCall(t, h.connector.calls)
	if cold.candidate.PeerID != candidates[2].PeerID {
		t.Fatal("COLD was not attempted immediately after BUSY")
	}
	connectionC := newFakeExitConnection()
	connectSuccess(cold, connectionC)
	waitSelectorEvent(t, h.selector.Events(), EventExitFailoverCompleted)
	if active := h.selector.Current().Active; active == nil || active.Candidate.PeerID != candidates[2].PeerID {
		t.Fatalf("COLD not promoted: %+v", active)
	}
	stats := h.selector.Stats()
	if stats.Busy != 1 || stats.Failovers != 1 {
		t.Fatalf("BUSY/failover stats = %+v", stats)
	}
	refresh := waitProviderCall(t, h.provider.calls)
	for _, excluded := range refresh.request.ExcludePeerIDs {
		if excluded == candidates[1].PeerID {
			t.Fatal("BUSY candidate was hard-suppressed")
		}
	}
}

func TestSelectorTransientActiveRecoveryDoesNotConsumeReserve(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	connectionA.events <- ConnectionEvent{Type: ConnectionDisconnected}
	recovery := waitConnectorCall(t, h.connector.calls)
	if recovery.candidate.PeerID != candidates[0].PeerID {
		t.Fatal("transient recovery did not retain ACTIVE")
	}
	connectionA.events <- ConnectionEvent{Type: ConnectionRecovered}
	waitSelectorEvent(t, h.selector.Events(), EventSamePeerRecovered)
	select {
	case next := <-h.connector.calls:
		t.Fatalf("transient recovery consumed fallback %s", next.candidate.PeerID)
	default:
	}
	if snapshot := h.selector.Current(); snapshot.Active == nil || snapshot.Active.Candidate.PeerID != candidates[0].PeerID || snapshot.RecoveryRemaining != 2*time.Second {
		t.Fatalf("transient recovery snapshot = %+v", snapshot)
	}
}

func TestSelectorSuppressedPeerWithNewNetworkEpochCanReconnect(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidate := h.candidates(t, 1)[0]
	h.respondBatch(t, candidate)
	initial := waitConnectorCall(t, h.connector.calls)
	connection := newFakeExitConnection()
	connectSuccess(initial, connection)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)

	refresh := waitProviderCall(t, h.provider.calls)
	refresh.response <- providerResponse{batch: CandidateBatch{
		Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(),
	}}
	waitSelectorEvent(t, h.selector.Events(), EventBackgroundRefreshCompleted)

	connection.events <- ConnectionEvent{Type: ConnectionFailed, At: h.clock.Now()}
	recovery := waitConnectorCall(t, h.connector.calls)
	connectFailure(recovery, OutcomeUnavailable)

	providerCall := waitProviderCall(t, h.provider.calls)
	for _, excluded := range providerCall.request.ExcludePeerIDs {
		if excluded == candidate.PeerID {
			t.Fatal("path-suppressed peer was hidden from a potentially new NetworkEpoch")
		}
	}
	rotated := candidate
	rotated.NetworkEpoch++
	h.probe.set(rotated.NetworkEpoch, time.Millisecond)
	providerCall.response <- providerResponse{batch: CandidateBatch{
		Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{rotated},
	}}

	reconnect := waitConnectorCall(t, h.connector.calls)
	if reconnect.candidate.PeerID != candidate.PeerID || reconnect.candidate.NetworkEpoch != rotated.NetworkEpoch {
		t.Fatalf("new path epoch was not retried: %+v", reconnect.candidate)
	}
	connectSuccess(reconnect, newFakeExitConnection())
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	if active := h.selector.Current().Active; active == nil || active.Candidate.NetworkEpoch != rotated.NetworkEpoch {
		t.Fatalf("new path epoch was not promoted: %+v", active)
	}
}

func TestSelectorLocalPromotionFailureBacksOffWithoutPoisoningPeer(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidate := h.candidates(t, 1)[0]
	h.respondBatch(t, candidate)
	initial := waitConnectorCall(t, h.connector.calls)
	_ = h.clock.nextLiveTimer(t, defaultFastConnectTimeout)
	failure := errors.New("injected local DNS activation failure")
	connection := newPromotableExitConnection(&OutcomeError{Outcome: OutcomeLocalPromotionFailed, Err: failure})
	connectSuccess(initial, connection)
	event := waitSelectorEvent(t, h.selector.Events(), EventLocalPromotionFailed)
	if !errors.Is(event.Err, failure) || event.Outcome != OutcomeLocalPromotionFailed {
		t.Fatalf("local failure event = %+v", event)
	}
	if snapshot := h.selector.Current(); snapshot.State != StateLocalPromotionFailed || snapshot.Active != nil {
		t.Fatalf("local failure snapshot = %+v", snapshot)
	}
	if !connection.closed.Load() || h.selector.Stats().LocalPromotionFailures != 1 {
		t.Fatalf("staged close=%t stats=%+v", connection.closed.Load(), h.selector.Stats())
	}

	retry := h.clock.nextLiveTimer(t, defaultLocalRetry)
	if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 1, IPv4Usable: true}); err != nil {
		t.Fatal(err)
	}
	if err := h.selector.RefreshNow(); err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-h.provider.calls:
		t.Fatalf("unchanged local/path state bypassed backoff: %+v", call.request)
	case call := <-h.connector.calls:
		t.Fatalf("unchanged local/path state retried peer %s", call.candidate.PeerID)
	default:
	}

	retry.fire(h.clock.advance(defaultLocalRetry))
	refresh := waitProviderCall(t, h.provider.calls)
	refreshed := candidate
	refreshed.DescriptorSequence++
	refreshed.DescriptorID[0]++
	refresh.response <- providerResponse{batch: CandidateBatch{
		Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{refreshed},
	}}
	reconnect := waitConnectorCall(t, h.connector.calls)
	if reconnect.candidate.PeerID != candidate.PeerID || reconnect.candidate.NetworkEpoch != candidate.NetworkEpoch {
		t.Fatalf("same authenticated path was poisoned by local failure: %+v", reconnect.candidate)
	}
	connectSuccess(reconnect, newFakeExitConnection())
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	if snapshot := h.selector.Current(); snapshot.State != StateConnected || snapshot.Active == nil || snapshot.Active.Candidate.PeerID != candidate.PeerID {
		t.Fatalf("recovery after local condition cleared = %+v", snapshot)
	}
}

func TestSelectorNetworkGenerationChangeClearsPathSuppression(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidate := h.candidates(t, 1)[0]
	h.respondBatch(t, candidate)
	connectFailure(waitConnectorCall(t, h.connector.calls), OutcomeUnavailable)

	_ = waitProviderCall(t, h.provider.calls)
	if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 2, IPv4Usable: true}); err != nil {
		t.Fatal(err)
	}
	newPathSelection := waitProviderCall(t, h.provider.calls)
	newPathSelection.response <- providerResponse{batch: CandidateBatch{
		Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{candidate},
	}}
	reconnect := waitConnectorCall(t, h.connector.calls)
	if reconnect.candidate.PeerID != candidate.PeerID {
		t.Fatalf("new client network generation retained old path suppression: %s", reconnect.candidate.PeerID)
	}
}

func TestSelectorDescriptorRefreshDoesNotClearSamePathSuppression(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidate := h.candidates(t, 1)[0]
	h.respondBatch(t, candidate)
	connectFailure(waitConnectorCall(t, h.connector.calls), OutcomeUnavailable)

	refresh := waitProviderCall(t, h.provider.calls)
	refreshed := candidate
	refreshed.DescriptorSequence++
	refreshed.DescriptorID[0]++
	refresh.response <- providerResponse{batch: CandidateBatch{
		Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{refreshed},
	}}
	waitSelectorEvent(t, h.selector.Events(), EventNoExitAvailable)
	select {
	case reconnect := <-h.connector.calls:
		t.Fatalf("descriptor refresh bypassed same-path suppression for %s", reconnect.candidate.PeerID)
	default:
	}
}

func TestSelectorRepeatedLocalPromotionFailuresRemainBounded(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidate := h.candidates(t, 1)[0]
	candidate.ExpiresAt = h.now.Add(24 * time.Hour)
	failure := errors.New("controlled local activation failure")
	for attempt := range 100 {
		candidate.GeneratedAt = h.clock.Now()
		h.respondBatch(t, candidate)
		call := waitConnectorCall(t, h.connector.calls)
		_ = h.clock.nextLiveTimer(t, defaultFastConnectTimeout)
		connection := newPromotableExitConnection(&OutcomeError{Outcome: OutcomeLocalPromotionFailed, Err: failure})
		connectSuccess(call, connection)
		waitSelectorEvent(t, h.selector.Events(), EventLocalPromotionFailed)
		if !connection.closed.Load() {
			t.Fatalf("attempt %d left staged connection open", attempt+1)
		}
		if attempt != 99 {
			retry := h.clock.nextLiveTimer(t, defaultLocalRetry)
			retry.fire(h.clock.advance(defaultLocalRetry))
		}
	}
	stats := h.selector.Stats()
	if stats.ConnectAttempts != 100 || stats.LocalPromotionFailures != 100 || stats.Failed != 0 || stats.FreshBatchRequests != 0 {
		t.Fatalf("repeated local failure stats = %+v", stats)
	}
	select {
	case call := <-h.provider.calls:
		t.Fatalf("attempt 100 retried before backoff: %+v", call.request)
	case call := <-h.connector.calls:
		t.Fatalf("attempt 100 reconnected before backoff: %s", call.candidate.PeerID)
	default:
	}
}

func TestSelectorPersistentFailurePromotesReserve(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	connectionA.events <- ConnectionEvent{Type: ConnectionFailed}
	connectFailure(waitConnectorCall(t, h.connector.calls), OutcomeUnavailable)
	reserve := waitConnectorCall(t, h.connector.calls)
	connectionB := newFakeExitConnection()
	connectSuccess(reserve, connectionB)
	waitSelectorEvent(t, h.selector.Events(), EventExitFailoverCompleted)
	if snapshot := h.selector.Current(); snapshot.Active == nil || snapshot.Active.Candidate.PeerID != candidates[1].PeerID || !connectionA.closed.Load() {
		t.Fatalf("persistent failure did not promote RESERVE: %+v", snapshot)
	}
}

func TestSelectorStaleReportedEventCannotAffectNewSamePeerConnection(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidate := h.candidates(t, 1)[0]
	h.respondBatch(t, candidate)
	initial := waitConnectorCall(t, h.connector.calls)
	first := newFakeExitConnection()
	connectSuccess(initial, first)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	stale := h.selector.Current()

	first.events <- ConnectionEvent{Type: ConnectionFailed}
	recovery := waitConnectorCall(t, h.connector.calls)
	second := newFakeExitConnection()
	connectSuccess(recovery, second)
	waitSelectorEvent(t, h.selector.Events(), EventSamePeerRecovered)
	if current := h.selector.Current(); current.LifecycleGeneration == stale.LifecycleGeneration {
		t.Fatal("same-peer recovery did not advance lifecycle fencing")
	}

	if err := h.selector.send(command{
		kind: commandActiveEvent, event: ConnectionFailed,
		peer: candidate.PeerID, token: stale.LifecycleGeneration,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-h.connector.calls:
		t.Fatalf("stale same-PeerID event started recovery for %s", call.candidate.PeerID)
	default:
	}
	if current := h.selector.Current(); current.State != StateConnected || current.Active == nil || current.Active.Candidate.PeerID != candidate.PeerID || second.closed.Load() {
		t.Fatalf("stale event affected current connection: %+v", current)
	}
}

func TestSelectorDeadReservePromotesColdWithoutRecoveryWindow(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	connectionA.events <- ConnectionEvent{Type: ConnectionFailed}
	connectFailure(waitConnectorCall(t, h.connector.calls), OutcomeUnavailable)
	reserve := waitConnectorCall(t, h.connector.calls)
	connectFailure(reserve, OutcomeUnavailable)
	cold := waitConnectorCall(t, h.connector.calls)
	if cold.candidate.PeerID != candidates[2].PeerID {
		t.Fatal("dead RESERVE did not fall through to COLD")
	}
	fastTimer := h.clock.nextLiveTimer(t, 1500*time.Millisecond)
	if fastTimer.duration >= 2*time.Second {
		t.Fatal("COLD received ACTIVE recovery budget")
	}
	connectionC := newFakeExitConnection()
	connectSuccess(cold, connectionC)
	waitSelectorEvent(t, h.selector.Events(), EventExitFailoverCompleted)
}

func TestSelectorCorrelatedActiveReserveLossKeepsColdEligibleAfterRouteFailure(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	candidates[2].SignalingRouteEpoch = 1
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)

	connectionA.events <- ConnectionEvent{Type: ConnectionFailed}
	connectFailure(waitConnectorCall(t, h.connector.calls), OutcomeUnavailable)
	reserve := waitConnectorCall(t, h.connector.calls)
	if reserve.candidate.PeerID != candidates[1].PeerID {
		t.Fatalf("correlated RESERVE = %s, want %s", reserve.candidate.PeerID, candidates[1].PeerID)
	}
	connectFailure(reserve, OutcomeUnavailable)
	cold := waitConnectorCall(t, h.connector.calls)
	if cold.candidate.PeerID != candidates[2].PeerID {
		t.Fatalf("COLD = %s, want %s", cold.candidate.PeerID, candidates[2].PeerID)
	}
	connectFailure(cold, OutcomeRouteFailed)

	fresh := waitProviderCall(t, h.provider.calls)
	candidates[2].SignalingRouteEpoch = 2
	fresh.response <- providerResponse{batch: CandidateBatch{
		Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(),
		Candidates: []Candidate{candidates[2]},
	}}
	retried := waitConnectorCall(t, h.connector.calls)
	if retried.candidate.PeerID != candidates[2].PeerID {
		t.Fatalf("fresh route retried peer = %s, want %s", retried.candidate.PeerID, candidates[2].PeerID)
	}
	connectionC := newPromotableExitConnection(nil)
	connectSuccess(retried, connectionC)
	waitSelectorEvent(t, h.selector.Events(), EventExitFailoverCompleted)
	if active := h.selector.Current().Active; active == nil || active.Candidate.PeerID != candidates[2].PeerID {
		t.Fatalf("fresh route did not promote COLD: %+v", active)
	}
	if connectionC.promotions.Load() != 1 {
		t.Fatalf("COLD resource ownership promotions = %d, want 1", connectionC.promotions.Load())
	}
	for {
		select {
		case event := <-h.selector.Events():
			if event.Type == EventNoExitAvailable {
				t.Fatal("NO_EXIT emitted while a fresh route to COLD remained usable")
			}
		default:
			return
		}
	}
}

func TestSelectorPropagatesFastConnectBudgetAsContextDeadline(t *testing.T) {
	h := newSelectorHarness(t, false)
	h.respondBatch(t, h.candidates(t, 1)...)
	call := waitConnectorCall(t, h.connector.calls)
	deadline, ok := call.ctx.Deadline()
	if !ok {
		t.Fatal("fast-connect budget was not propagated to Connector context")
	}
	remaining := time.Until(deadline)
	if remaining < time.Second || remaining > 1600*time.Millisecond {
		t.Fatalf("connector deadline remaining = %s, want approximately 1.5s", remaining)
	}
}

func TestSelectorPromotionGetsIndependentBoundedPhaseBudget(t *testing.T) {
	h := newSelectorHarness(t, false)
	h.respondBatch(t, h.candidates(t, 1)...)
	call := waitConnectorCall(t, h.connector.calls)
	h.clock.advance(1400 * time.Millisecond)
	connection := newPromotableExitConnection(nil)
	connection.deadlines = make(chan time.Time, 1)
	connectSuccess(call, connection)
	deadline := <-connection.deadlines
	remaining := time.Until(deadline)
	if remaining < time.Second || remaining > 1600*time.Millisecond {
		t.Fatalf("promotion deadline remaining = %s, want independent approximately 1.5s phase", remaining)
	}
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	if connection.promotions.Load() != 1 {
		t.Fatalf("promotion count = %d, want 1", connection.promotions.Load())
	}
	select {
	case duplicate := <-connection.deadlines:
		t.Fatalf("duplicate promotion with deadline %s", duplicate)
	default:
	}
}

func TestSelectorPromotionUsesConnectionProtocolTimeout(t *testing.T) {
	h := newSelectorHarness(t, false)
	h.respondBatch(t, h.candidates(t, 1)...)
	call := waitConnectorCall(t, h.connector.calls)
	connection := &timedPromotableExitConnection{
		promotableExitConnection: newPromotableExitConnection(nil), timeout: 5 * time.Second,
	}
	connection.deadlines = make(chan time.Time, 1)
	connectSuccess(call, connection)
	deadline := <-connection.deadlines
	remaining := time.Until(deadline)
	if remaining < 4500*time.Millisecond || remaining > 5100*time.Millisecond {
		t.Fatalf("promotion deadline remaining = %s, want protocol timeout approximately 5s", remaining)
	}
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	if connection.promotions.Load() != 1 {
		t.Fatalf("promotion count = %d, want 1", connection.promotions.Load())
	}
}

func TestSelectorAllBrokerRoutesDeadTerminatesCurrentAttemptSet(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidate := h.candidates(t, 3)[0]
	candidate.SignalingRouteEpoch = 1
	h.respondBatch(t, candidate)
	initial := waitConnectorCall(t, h.connector.calls)
	connection := newFakeExitConnection()
	connectSuccess(initial, connection)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)

	// Drain the automatic metadata refresh so recovery has no stale fallback.
	h.respondBatch(t)
	waitSelectorEvent(t, h.selector.Events(), EventBackgroundRefreshCompleted)
	connection.events <- ConnectionEvent{Type: ConnectionFailed}
	recovery := waitConnectorCall(t, h.connector.calls)
	connectFailure(recovery, OutcomeRouteFailed)

	fresh := waitProviderCall(t, h.provider.calls)
	fresh.response <- providerResponse{batch: CandidateBatch{
		Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(),
		Candidates: []Candidate{candidate},
	}}
	waitSelectorEvent(t, h.selector.Events(), EventNoExitAvailable)
	select {
	case call := <-h.connector.calls:
		t.Fatalf("known-dead route set retried without a fresh route: %s", call.candidate.PeerID)
	default:
	}
	if snapshot := h.selector.Current(); snapshot.State != StateNoExit {
		t.Fatalf("all-routes-dead state = %d, want NO_EXIT", snapshot.State)
	}
}

func TestSelectorExhaustionImmediatelyRequestsFreshBatch(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	connectionA.events <- ConnectionEvent{Type: ConnectionFailed}
	connectFailure(waitConnectorCall(t, h.connector.calls), OutcomeUnavailable)
	connectFailure(waitConnectorCall(t, h.connector.calls), OutcomeUnavailable)
	connectFailure(waitConnectorCall(t, h.connector.calls), OutcomeUnavailable)
	freshCall := waitProviderCall(t, h.provider.calls)
	if freshCall.request.Country != "DE" || freshCall.request.Limit != 10 {
		t.Fatalf("fresh request = %+v", freshCall.request)
	}
	d := h.candidates(t, 4)[0]
	freshCall.response <- providerResponse{batch: CandidateBatch{Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{d}}}
	connectD := waitConnectorCall(t, h.connector.calls)
	connectionD := newFakeExitConnection()
	connectSuccess(connectD, connectionD)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	if h.selector.Current().Active.Candidate.PeerID != d.PeerID || h.selector.Stats().FreshBatchRequests != 1 {
		t.Fatal("fresh batch did not restore ACTIVE")
	}
}

func TestSelectorInitialCandidateGetsOnlyFastAttempt(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	fastTimer := h.clock.nextLiveTimer(t, 1500*time.Millisecond)
	fastTimer.fire(h.clock.advance(1500 * time.Millisecond))
	next := waitConnectorCall(t, h.connector.calls)
	if initial.candidate.PeerID != candidates[0].PeerID || next.candidate.PeerID != candidates[1].PeerID {
		t.Fatal("initial candidate received recovery privilege or wrong fallback")
	}
	if snapshot := h.selector.Current(); snapshot.State != StateConnectingReserve {
		t.Fatalf("state after initial fast timeout = %d", snapshot.State)
	}
}

func TestSelectorNoExitKeepsCountryAndRecoversProvider(t *testing.T) {
	h := newSelectorHarness(t, false)
	h.respondBatch(t)
	waitSelectorEvent(t, h.selector.Events(), EventNoExitAvailable)
	if snapshot := h.selector.Current(); snapshot.State != StateNoExit || snapshot.Country != "DE" || !snapshot.ProviderAvailable {
		t.Fatalf("NO_EXIT snapshot = %+v", snapshot)
	}
	retry := h.clock.nextLiveTimer(t, 5*time.Second)
	retry.fire(h.clock.advance(5 * time.Second))
	call := waitProviderCall(t, h.provider.calls)
	d := h.candidates(t, 4)[0]
	call.response <- providerResponse{batch: CandidateBatch{Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{d}}}
	connectD := waitConnectorCall(t, h.connector.calls)
	connectionD := newFakeExitConnection()
	connectSuccess(connectD, connectionD)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	if snapshot := h.selector.Current(); snapshot.Country != "DE" || snapshot.Active == nil || snapshot.Active.Candidate.PeerID != d.PeerID {
		t.Fatalf("provider recovery changed intent or failed promotion: %+v", snapshot)
	}
}

func TestSelectorProviderOutageWithoutExitRetriesWithoutCountryFallback(t *testing.T) {
	h := newSelectorHarness(t, false)
	call := waitProviderCall(t, h.provider.calls)
	call.response <- providerResponse{err: errors.New("provider offline")}
	waitSelectorEvent(t, h.selector.Events(), EventProviderUnavailable)
	if snapshot := h.selector.Current(); snapshot.State != StateNoExit || snapshot.Country != "DE" || snapshot.ProviderAvailable {
		t.Fatalf("provider outage snapshot = %+v", snapshot)
	}
	retry := h.clock.nextLiveTimer(t, 5*time.Second)
	retry.fire(h.clock.advance(5 * time.Second))
	recovered := waitProviderCall(t, h.provider.calls)
	d := h.candidates(t, 4)[0]
	recovered.response <- providerResponse{batch: CandidateBatch{Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{d}}}
	connectD := waitConnectorCall(t, h.connector.calls)
	connectionD := newFakeExitConnection()
	connectSuccess(connectD, connectionD)
	waitSelectorEvent(t, h.selector.Events(), EventProviderRecovered)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
}

func TestSelectorInitialSelectionWaitsForUsableUnderlay(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	clock := newManualClock(now)
	provider := &controlledProvider{calls: make(chan providerCall, 4)}
	connector := &controlledExitConnector{calls: make(chan connectorCall, 4)}
	probe := &latencyProbe{latencies: map[uint64]time.Duration{1: time.Millisecond}, now: clock.Now}
	config := DefaultConfig(selectorPeer(t, 250), "DE", provider, probe, connector, func() bool { return true }, UnderlayState{NetworkGeneration: 1})
	config.Clock, config.Random, config.LatencyTolerance = clock, &sequenceRandom{}, time.Millisecond
	selector, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = selector.Close() })
	if err := selector.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if snapshot := selector.Current(); snapshot.State != StateWaitingForUnderlay {
		t.Fatalf("initial unavailable underlay state = %d", snapshot.State)
	}
	select {
	case <-provider.calls:
		t.Fatal("provider queried without initial underlay")
	default:
	}
	if err := selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 2, IPv4Usable: true}); err != nil {
		t.Fatal(err)
	}
	if call := waitProviderCall(t, provider.calls); call.request.Country != "DE" {
		t.Fatal("underlay restoration changed CountryIntent")
	}
}

func TestSelectorUnderlayOutagePausesUsableRecoveryBudget(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	connectionA.events <- ConnectionEvent{Type: ConnectionFailed}
	firstRecovery := waitConnectorCall(t, h.connector.calls)
	h.clock.advance(500 * time.Millisecond)
	if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	waitSelectorEvent(t, h.selector.Events(), EventUnderlayLost)
	h.clock.advance(5 * time.Second)
	if snapshot := h.selector.Current(); snapshot.State != StateWaitingForUnderlay || snapshot.RecoveryRemaining != 1500*time.Millisecond || snapshot.Reserve == nil || snapshot.Cold == nil {
		t.Fatalf("underlay pause consumed state/budget: %+v", snapshot)
	}
	select {
	case call := <-h.connector.calls:
		t.Fatalf("candidate consumed without underlay: %s", call.candidate.PeerID)
	case call := <-h.provider.calls:
		t.Fatalf("provider queried without underlay: %+v", call.request)
	default:
	}
	if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 2, IPv4Usable: true}); err != nil {
		t.Fatal(err)
	}
	secondRecovery := waitConnectorCall(t, h.connector.calls)
	if secondRecovery.candidate.PeerID != candidates[0].PeerID {
		t.Fatal("underlay return did not try the same ACTIVE first")
	}
	connectionRecovered := newFakeExitConnection()
	connectSuccess(secondRecovery, connectionRecovered)
	waitSelectorEvent(t, h.selector.Events(), EventSamePeerRecovered)
	if h.selector.Current().Active.Candidate.PeerID != candidates[0].PeerID || h.selector.Stats().Failovers != 0 {
		t.Fatal("local underlay outage replaced or suppressed ACTIVE")
	}
	select {
	case <-firstRecovery.response:
		t.Fatal("canceled connector response channel was unexpectedly consumed by test")
	default:
	}
}

func TestSelectorUnderlayReturnTriesDeadActiveBeforeReserve(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	connectionA.events <- ConnectionEvent{Type: ConnectionFailed}
	h.clock.advance(3 * time.Second)
	if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 2, IPv4Usable: true}); err != nil {
		t.Fatal(err)
	}
	recovery := waitConnectorCall(t, h.connector.calls)
	if recovery.candidate.PeerID != candidates[0].PeerID {
		t.Fatal("dead ACTIVE was not retried first after underlay return")
	}
	connectFailure(recovery, OutcomeUnavailable)
	reserve := waitConnectorCall(t, h.connector.calls)
	if reserve.candidate.PeerID != candidates[1].PeerID {
		t.Fatal("RESERVE did not follow genuine ACTIVE failure")
	}
}

func TestSelectorUnderlayLossCannotResetExhaustedRecoveryBudget(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	connectionA.events <- ConnectionEvent{Type: ConnectionFailed}
	recovery := waitConnectorCall(t, h.connector.calls)
	originalTimer := h.clock.nextLiveTimer(t, 2*time.Second)
	h.clock.advance(2 * time.Second)
	if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	for !originalTimer.stopped.Load() {
		select {
		case <-deadline.C:
			t.Fatal("canceled recovery timer remained active")
		case <-ticker.C:
		}
	}
	ticker.Stop()
	deadline.Stop()
	if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 2, IPv4Usable: true}); err != nil {
		t.Fatal(err)
	}
	restarted := waitConnectorCall(t, h.connector.calls)
	if restarted.candidate.PeerID != candidates[0].PeerID {
		t.Fatal("underlay return did not fence the exhausted ACTIVE attempt")
	}
	timer := h.clock.nextLiveTimer(t, 0)
	if timer.duration != 0 {
		t.Fatalf("exhausted usable-underlay budget was reset to %s", timer.duration)
	}
	if !timer.fire(h.clock.Now()) {
		t.Fatal("zero-budget recovery timer did not fire")
	}
	reserve := waitConnectorCall(t, h.connector.calls)
	if reserve.candidate.PeerID != candidates[1].PeerID {
		t.Fatalf("exhausted ACTIVE did not advance to RESERVE: %s", reserve.candidate.PeerID)
	}
	select {
	case recovery.response <- connectorResponse{result: ConnectResult{Outcome: OutcomeUnavailable}}:
	default:
	}
}

func TestSelectorUnderlayFlappingDoesNotConsumeFallbacks(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	var finalRecovery connectorCall
	for generation := networkstate.Generation(2); generation <= 6; generation++ {
		if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: generation - 1}); err != nil {
			t.Fatal(err)
		}
		if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: generation, IPv4Usable: true}); err != nil {
			t.Fatal(err)
		}
		recovery := waitConnectorCall(t, h.connector.calls)
		if recovery.candidate.PeerID != candidates[0].PeerID {
			t.Fatal("underlay flap consumed RESERVE or COLD")
		}
		finalRecovery = recovery
	}
	connectionRecovered := newFakeExitConnection()
	connectSuccess(finalRecovery, connectionRecovered)
	waitSelectorEvent(t, h.selector.Events(), EventSamePeerRecovered)
	if stats := h.selector.Stats(); stats.Failovers != 0 || stats.FreshBatchRequests != 0 {
		t.Fatalf("underlay flapping consumed peer state: %+v", stats)
	}
}

func TestSelectorLateRecoveryCannotReplaceNewActive(t *testing.T) {
	h := newSelectorHarness(t, true)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	_ = h.clock.nextLiveTimer(t, defaultFastConnectTimeout)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	connectionA.events <- ConnectionEvent{Type: ConnectionFailed}
	lateRecovery := waitConnectorCall(t, h.connector.calls)
	recoveryTimer := h.clock.nextLiveTimer(t, 2*time.Second)
	recoveryTimer.fire(h.clock.advance(2 * time.Second))
	reserve := waitConnectorCall(t, h.connector.calls)
	connectionB := newFakeExitConnection()
	connectSuccess(reserve, connectionB)
	waitSelectorEvent(t, h.selector.Events(), EventExitFailoverCompleted)
	lateConnection := newPromotableExitConnection(nil)
	connectSuccess(lateRecovery, lateConnection)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for !lateConnection.closed.Load() {
		select {
		case <-deadline.C:
			t.Fatal("late ACTIVE completion was not closed")
		default:
		}
	}
	if h.selector.Current().Active.Candidate.PeerID != candidates[1].PeerID {
		t.Fatal("late recovery replaced newer ACTIVE")
	}
	if lateConnection.promotions.Load() != 0 {
		t.Fatal("late recovery published staged Data Plane resources")
	}
}

func TestSelectorProviderFailureIsIndependentFromHealthyActive(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	if err := h.selector.RefreshNow(); err != nil {
		t.Fatal(err)
	}
	call := waitProviderCall(t, h.provider.calls)
	call.response <- providerResponse{err: errors.New("provider unavailable")}
	waitSelectorEvent(t, h.selector.Events(), EventProviderUnavailable)
	snapshot := h.selector.Current()
	if snapshot.State != StateConnected || snapshot.Active == nil || snapshot.Active.Candidate.PeerID != candidates[0].PeerID || snapshot.ProviderAvailable {
		t.Fatalf("provider outage disturbed ACTIVE: %+v", snapshot)
	}
}

func TestSelectorProviderUnavailableEventHasCommittedSnapshot(t *testing.T) {
	for iteration := range 128 {
		h := newSelectorHarness(t, false)
		call := waitProviderCall(t, h.provider.calls)
		call.response <- providerResponse{err: errors.New("provider unavailable")}
		waitSelectorEvent(t, h.selector.Events(), EventProviderUnavailable)
		snapshot := h.selector.Current()
		if snapshot.State != StateNoExit || snapshot.ProviderAvailable {
			t.Fatalf("iteration %d: unavailable event preceded snapshot commit: %+v", iteration, snapshot)
		}
	}
}

func TestSelectorStaleBackgroundRefreshCannotOverwriteRecoveryState(t *testing.T) {
	h := newSelectorHarness(t, false)
	candidates := h.candidates(t, 1, 2, 3)
	h.respondBatch(t, candidates...)
	initial := waitConnectorCall(t, h.connector.calls)
	connectionA := newFakeExitConnection()
	connectSuccess(initial, connectionA)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	h.provider.ignoreCancel = true
	if err := h.selector.RefreshNow(); err != nil {
		t.Fatal(err)
	}
	staleRefresh := waitProviderCall(t, h.provider.calls)
	connectionA.events <- ConnectionEvent{Type: ConnectionDisconnected}
	recovery := waitConnectorCall(t, h.connector.calls)
	d := h.candidates(t, 4)[0]
	staleRefresh.response <- providerResponse{batch: CandidateBatch{Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{d}}}
	connectionRecovered := newFakeExitConnection()
	connectSuccess(recovery, connectionRecovered)
	waitSelectorEvent(t, h.selector.Events(), EventSamePeerRecovered)
	if snapshot := h.selector.Current(); snapshot.Active.Candidate.PeerID != candidates[0].PeerID || snapshot.Reserve.Candidate.PeerID != candidates[1].PeerID {
		t.Fatalf("stale refresh overwrote recovery state: %+v", snapshot)
	}
}

func TestSelectorCountryChangeFencesOldWorkAndShutdownPreventsResurrection(t *testing.T) {
	h := newSelectorHarness(t, true)
	oldProvider := waitProviderCall(t, h.provider.calls)
	if err := h.selector.SetCountry("NL"); err != nil {
		t.Fatal(err)
	}
	newProvider := waitProviderCall(t, h.provider.calls)
	if newProvider.request.Country != "NL" {
		t.Fatalf("new CountryIntent request = %s", newProvider.request.Country)
	}
	nl := selectorCandidate(t, 10, h.now)
	nl.Country = "NL"
	h.probe.set(nl.NetworkEpoch, time.Millisecond)
	newProvider.response <- providerResponse{batch: CandidateBatch{Country: "NL", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{nl}}}
	connectNL := waitConnectorCall(t, h.connector.calls)
	lateDE := h.candidates(t, 1)[0]
	oldProvider.response <- providerResponse{batch: CandidateBatch{Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{lateDE}}}
	connectionNL := newFakeExitConnection()
	connectSuccess(connectNL, connectionNL)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	if snapshot := h.selector.Current(); snapshot.Country != "NL" || snapshot.Active.Candidate.PeerID != nl.PeerID {
		t.Fatalf("old CountryIntent work promoted: %+v", snapshot)
	}
	if err := h.selector.Close(); err != nil {
		t.Fatal(err)
	}
	if !connectionNL.closed.Load() || h.selector.Current().State != StateClosed {
		t.Fatal("shutdown left or resurrected ACTIVE")
	}
}

func TestSelectorNetworkGenerationFencesSelectionResult(t *testing.T) {
	h := newSelectorHarness(t, false)
	oldProvider := waitProviderCall(t, h.provider.calls)
	if err := h.selector.UpdateUnderlay(UnderlayState{NetworkGeneration: 2, IPv4Usable: true}); err != nil {
		t.Fatal(err)
	}
	newProvider := waitProviderCall(t, h.provider.calls)
	old := h.candidates(t, 1)[0]
	oldProvider.response <- providerResponse{batch: CandidateBatch{Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{old}}}
	current := h.candidates(t, 2)[0]
	newProvider.response <- providerResponse{batch: CandidateBatch{Country: "DE", AvailablePeerCount: 1, GeneratedAt: h.clock.Now(), Candidates: []Candidate{current}}}
	connectCurrent := waitConnectorCall(t, h.connector.calls)
	if connectCurrent.candidate.PeerID != current.PeerID {
		t.Fatal("stale NetworkGeneration selection reached connector")
	}
	connection := newFakeExitConnection()
	connectSuccess(connectCurrent, connection)
	waitSelectorEvent(t, h.selector.Events(), EventActivePromoted)
	if snapshot := h.selector.Current(); snapshot.Underlay.NetworkGeneration != 2 || snapshot.Active.Candidate.PeerID != current.PeerID {
		t.Fatalf("stale generation promoted: %+v", snapshot)
	}
}

func TestSelectorRequiresProtectedModeBeforeAnyProviderWork(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	provider := &controlledProvider{calls: make(chan providerCall, 1)}
	connector := &controlledExitConnector{calls: make(chan connectorCall, 1)}
	config := DefaultConfig(selectorPeer(t, 250), "DE", provider, probeFunc(func(context.Context, Candidate) (ProbeResult, error) { return ProbeResult{}, nil }), connector, func() bool { return false }, UnderlayState{NetworkGeneration: 1, IPv4Usable: true})
	config.Clock = newManualClock(now)
	config.Random = &sequenceRandom{}
	selector, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer selector.Close()
	if err := selector.Start(context.Background()); !errors.Is(err, ErrProtectedModeOff) {
		t.Fatalf("Start error = %v", err)
	}
	select {
	case <-provider.calls:
		t.Fatal("provider called before ProtectedMode")
	default:
	}
}
