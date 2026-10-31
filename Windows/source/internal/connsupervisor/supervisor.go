// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package connsupervisor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

type completion struct {
	request Request
	session Session
	err     error
}

type attempt struct{ cancel context.CancelFunc }

// Supervisor owns fresh session generations for one logical peer intent.
type Supervisor struct {
	config Config

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wake   chan struct{}
	finish chan completion
	events chan Event

	stateMu  sync.RWMutex
	snapshot Snapshot
	started  bool
	closed   bool

	inboxMu        sync.Mutex
	pendingRestart bool
	pendingReason  Reason
	pendingNetwork networkstate.Generation

	closeOnce sync.Once
	wg        sync.WaitGroup
}

// New validates but does not start a logical connection supervisor.
func New(config Config) (*Supervisor, error) {
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("create connection supervisor: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		config: config, ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1),
		finish: make(chan completion, config.MaxInFlight), events: make(chan Event, config.EventCapacity),
		snapshot: Snapshot{State: StateIdle, NetworkGeneration: config.InitialNetworkGeneration},
	}, nil
}

// Start begins the initial generation. Cancellation of ctx closes the supervisor.
func (s *Supervisor) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("start connection supervisor: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return ErrClosed
	}
	if s.started {
		s.stateMu.Unlock()
		return ErrAlreadyStarted
	}
	s.started = true
	s.stateMu.Unlock()
	stop := context.AfterFunc(ctx, s.cancel)
	go func() {
		defer stop()
		s.run()
	}()
	return nil
}

// Restart requests a fresh same-network ICE generation.
func (s *Supervisor) Restart(reason Reason) error {
	if reason == 0 {
		reason = ReasonExplicitRestart
	}
	return s.enqueue(0, reason)
}

// NetworkChanged fences old work and requests the newest semantic generation.
func (s *Supervisor) NetworkChanged(generation networkstate.Generation) error {
	if generation == 0 {
		return fmt.Errorf("network generation must be nonzero")
	}
	return s.enqueue(generation, ReasonNetworkChanged)
}

func (s *Supervisor) enqueue(generation networkstate.Generation, reason Reason) error {
	s.stateMu.RLock()
	started, closed := s.started, s.closed
	s.stateMu.RUnlock()
	if closed {
		return ErrClosed
	}
	if !started {
		return ErrNotStarted
	}
	s.inboxMu.Lock()
	if generation > s.pendingNetwork {
		s.pendingNetwork = generation
	}
	if reason != ReasonNetworkChanged || s.pendingReason == 0 {
		s.pendingReason = reason
	}
	s.pendingRestart = true
	s.inboxMu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// Current returns a copied supervisor state.
func (s *Supervisor) Current() Snapshot {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.snapshot
}

// Events returns a bounded stream that drops the oldest event on overflow.
func (s *Supervisor) Events() <-chan Event { return s.events }

func (s *Supervisor) run() {
	defer close(s.done)
	defer close(s.events)
	networkGeneration := s.config.InitialNetworkGeneration
	desired := SessionGeneration(0)
	attempts := make(map[SessionGeneration]attempt, s.config.MaxInFlight)
	var active Session
	var activeState <-chan ice.ConnectionState
	var disconnectTimer Timer
	var disconnectC <-chan time.Time
	var retryTimer Timer
	var retryC <-chan time.Time
	retryDelay := s.config.RetryInitial
	sameGenerationFailures := 0
	queuedReason := Reason(0)
	scheduleFailure := func(completed completion, err error) {
		reason := classifyFailure(err)
		s.publish(EventRestartFailed, reason, completed.request, SessionMetadata{})
		sameGenerationFailures++
		if sameGenerationFailures >= s.config.MaxSameGenerationFailures {
			s.publish(EventTransportRefreshRequested, reason, completed.request, SessionMetadata{})
			return
		}
		if retryTimer == nil {
			retryTimer = s.config.Clock.NewTimer(retryDelay)
			retryC = retryTimer.C()
			s.publish(EventRestartScheduled, reason, completed.request, SessionMetadata{})
			if retryDelay < s.config.RetryMaximum/2 {
				retryDelay *= 2
			} else {
				retryDelay = s.config.RetryMaximum
			}
		}
	}

	start := func(reason Reason) {
		for _, running := range attempts {
			running.cancel()
		}
		if len(attempts) >= s.config.MaxInFlight {
			queuedReason = reason
			return
		}
		desired++
		request := Request{Intent: s.config.Intent, NetworkGeneration: networkGeneration, SessionGeneration: desired, Reason: reason}
		attemptContext, cancel := context.WithCancel(s.ctx)
		attempts[desired] = attempt{cancel: cancel}
		s.updateSnapshot(StateConnecting, networkGeneration, desired, active)
		eventType := EventConnecting
		if desired > 1 {
			eventType = EventRestartStarted
		}
		s.publish(eventType, reason, request, SessionMetadata{})
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.connect(attemptContext, request)
		}()
	}

	start(ReasonInitial)
	for {
		select {
		case <-s.ctx.Done():
			for _, running := range attempts {
				running.cancel()
			}
			if disconnectTimer != nil {
				disconnectTimer.Stop()
			}
			if retryTimer != nil {
				retryTimer.Stop()
			}
			if active != nil {
				_ = active.Close()
			}
			s.wg.Wait()
			s.closeFinishedSessions()
			s.updateSnapshot(StateClosed, networkGeneration, desired, nil)
			s.publish(EventClosed, ReasonContextCanceled, Request{Intent: s.config.Intent, NetworkGeneration: networkGeneration, SessionGeneration: desired}, SessionMetadata{})
			s.stateMu.Lock()
			s.closed = true
			s.stateMu.Unlock()
			return

		case <-s.wake:
			s.inboxMu.Lock()
			requested := s.pendingRestart
			reason := s.pendingReason
			newNetwork := s.pendingNetwork
			s.pendingRestart = false
			s.pendingReason = 0
			s.pendingNetwork = 0
			s.inboxMu.Unlock()
			if !requested {
				continue
			}
			if newNetwork > networkGeneration {
				networkGeneration = newNetwork
				sameGenerationFailures = 0
				retryDelay = s.config.RetryInitial
				s.publish(EventNetworkGenerationChanged, ReasonNetworkChanged, Request{Intent: s.config.Intent, NetworkGeneration: networkGeneration, SessionGeneration: desired}, SessionMetadata{})
				reason = ReasonNetworkChanged
			} else if newNetwork != 0 {
				continue
			}
			if retryTimer != nil {
				retryTimer.Stop()
				retryTimer, retryC = nil, nil
			}
			start(reason)

		case completed := <-s.finish:
			if running, ok := attempts[completed.request.SessionGeneration]; ok {
				running.cancel()
				delete(attempts, completed.request.SessionGeneration)
			}
			if completed.err != nil {
				if completed.request.SessionGeneration != desired || completed.request.NetworkGeneration != networkGeneration || errors.Is(completed.err, context.Canceled) {
					if queuedReason != 0 && len(attempts) < s.config.MaxInFlight {
						reason := queuedReason
						queuedReason = 0
						start(reason)
					}
					continue
				}
				scheduleFailure(completed, completed.err)
				continue
			}
			if completed.session == nil {
				completed.err = ErrInvalidSession
				scheduleFailure(completed, completed.err)
				continue
			}
			metadata := completed.session.Metadata()
			if !validMetadata(metadata, completed.request) || completed.request.SessionGeneration != desired || completed.request.NetworkGeneration != networkGeneration {
				_ = completed.session.Close()
				s.publish(EventRestartFailed, ReasonGenerationStale, completed.request, metadata)
				if queuedReason != 0 && len(attempts) < s.config.MaxInFlight {
					reason := queuedReason
					queuedReason = 0
					start(reason)
				}
				continue
			}
			previous := active
			active = completed.session
			activeState = active.StateChanges()
			retryDelay = s.config.RetryInitial
			sameGenerationFailures = 0
			if retryTimer != nil {
				retryTimer.Stop()
				retryTimer, retryC = nil, nil
			}
			s.updateSnapshot(StateConnected, networkGeneration, desired, active)
			if previous != nil {
				_ = previous.Close()
			}
			if desired == 1 {
				s.publish(EventConnected, completed.request.Reason, completed.request, metadata)
			} else {
				s.publish(EventRestartSucceeded, completed.request.Reason, completed.request, metadata)
			}
			if queuedReason != 0 && len(attempts) < s.config.MaxInFlight {
				reason := queuedReason
				queuedReason = 0
				start(reason)
			}

		case state, ok := <-activeState:
			if !ok {
				activeState = nil
				continue
			}
			switch state {
			case ice.ConnectionStateDisconnected:
				s.updateSnapshot(StateDisconnected, networkGeneration, desired, active)
				s.publish(EventDisconnected, ReasonICEFailed, Request{Intent: s.config.Intent, NetworkGeneration: networkGeneration, SessionGeneration: desired}, active.Metadata())
				if s.config.DisconnectedGrace > 0 && disconnectTimer == nil {
					disconnectTimer = s.config.Clock.NewTimer(s.config.DisconnectedGrace)
					disconnectC = disconnectTimer.C()
				}
			case ice.ConnectionStateConnected:
				if disconnectTimer != nil {
					disconnectTimer.Stop()
					disconnectTimer, disconnectC = nil, nil
					s.updateSnapshot(StateConnected, networkGeneration, desired, active)
					s.publish(EventRecovered, ReasonICEFailed, Request{Intent: s.config.Intent, NetworkGeneration: networkGeneration, SessionGeneration: desired}, active.Metadata())
				}
			case ice.ConnectionStateFailed:
				if disconnectTimer != nil {
					disconnectTimer.Stop()
					disconnectTimer, disconnectC = nil, nil
				}
				if len(attempts) == 0 {
					start(ReasonICEFailed)
				}
			}

		case <-disconnectC:
			disconnectTimer, disconnectC = nil, nil
			if len(attempts) == 0 {
				start(ReasonICEFailed)
			}

		case <-retryC:
			retryTimer, retryC = nil, nil
			start(ReasonICEFailed)
		}
	}
}

func (s *Supervisor) connect(ctx context.Context, request Request) {
	if err := s.config.Limiter.acquire(ctx); err != nil {
		s.deliver(completion{request: request, err: err})
		return
	}
	defer s.config.Limiter.release()
	session, err := s.config.Connector.Connect(ctx, request)
	s.deliver(completion{request: request, session: session, err: err})
}

func (s *Supervisor) deliver(result completion) {
	select {
	case s.finish <- result:
	case <-s.ctx.Done():
		if result.session != nil {
			_ = result.session.Close()
		}
	}
}

func (s *Supervisor) closeFinishedSessions() {
	for {
		select {
		case completed := <-s.finish:
			if completed.session != nil {
				_ = completed.session.Close()
			}
		default:
			return
		}
	}
}

func validMetadata(metadata SessionMetadata, request Request) bool {
	return metadata.NetworkGeneration == request.NetworkGeneration && metadata.SessionGeneration == request.SessionGeneration &&
		metadata.SessionID != (icesession.SessionID{}) && metadata.RemotePeer == request.Intent.RemotePeer &&
		metadata.Purpose == request.Intent.Purpose && metadata.LocalUfrag != ""
}

func classifyFailure(err error) Reason {
	var failure *FailureError
	if errors.As(err, &failure) && failure.Reason >= ReasonICEFailed && failure.Reason <= ReasonGenerationStale {
		return failure.Reason
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ReasonContextCanceled
	}
	return ReasonICEFailed
}

func (s *Supervisor) updateSnapshot(state State, network networkstate.Generation, desired SessionGeneration, active Session) {
	s.stateMu.Lock()
	s.snapshot.State = state
	s.snapshot.NetworkGeneration = network
	s.snapshot.DesiredSessionGeneration = desired
	s.snapshot.HasActive = active != nil
	if active != nil {
		s.snapshot.Active = active.Metadata()
	} else {
		s.snapshot.Active = SessionMetadata{}
	}
	s.stateMu.Unlock()
}

func (s *Supervisor) publish(eventType EventType, reason Reason, request Request, metadata SessionMetadata) {
	event := Event{
		Type: eventType, Reason: reason, RemotePeer: request.Intent.RemotePeer,
		NetworkGeneration: request.NetworkGeneration, SessionGeneration: request.SessionGeneration,
		SessionID: metadata.SessionID, At: time.Now(),
	}
	select {
	case s.events <- event:
		return
	default:
	}
	select {
	case <-s.events:
	default:
	}
	select {
	case s.events <- event:
	default:
	}
}

// Close cancels every attempt and active session and prevents resurrection.
func (s *Supervisor) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.stateMu.Lock()
		started := s.started
		if !started {
			s.closed = true
			s.snapshot.State = StateClosed
		}
		s.stateMu.Unlock()
		s.cancel()
		if started {
			<-s.done
		}
	})
	return nil
}
