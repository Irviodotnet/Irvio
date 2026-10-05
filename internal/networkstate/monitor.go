// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package networkstate

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	defaultDebounce    = 75 * time.Millisecond
	defaultEventBuffer = 64
)

type scanner interface {
	Scan(context.Context) (Snapshot, error)
}

type subscription interface {
	Events() <-chan struct{}
	Errors() <-chan error
	Close() error
}

type subscriptionFactory func() (subscription, error)

// Config controls bounded event coalescing and delivery.
type Config struct {
	Debounce    time.Duration
	EventBuffer int
}

func (c Config) withDefaults() (Config, error) {
	if c.Debounce == 0 {
		c.Debounce = defaultDebounce
	}
	if c.Debounce < 0 {
		return Config{}, fmt.Errorf("debounce must not be negative")
	}
	if c.EventBuffer == 0 {
		c.EventBuffer = defaultEventBuffer
	}
	if c.EventBuffer < 1 || c.EventBuffer > 4096 {
		return Config{}, fmt.Errorf("event buffer must be between 1 and 4096")
	}
	return c, nil
}

// Monitor converts netlink activity into semantic network generations.
type Monitor struct {
	cfg Config

	ctx    context.Context
	cancel context.CancelFunc
	sub    subscription
	scan   scanner

	mu      sync.RWMutex
	current State

	changes chan State
	errors  chan error
	done    chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// NewMonitor creates a Linux netlink monitor and performs its initial scan.
func NewMonitor(cfg Config) (*Monitor, error) {
	normalized, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return newMonitor(normalized, newLinuxScanner(), func() (subscription, error) {
		return newLinuxSubscription(normalized.EventBuffer)
	})
}

func newMonitor(cfg Config, scan scanner, subscribe subscriptionFactory) (*Monitor, error) {
	if scan == nil || subscribe == nil {
		return nil, fmt.Errorf("network monitor dependencies must not be nil")
	}
	var err error
	cfg, err = cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	sub, err := subscribe()
	if err != nil {
		return nil, fmt.Errorf("subscribe to network state: %w", err)
	}
	initial, err := scan.Scan(context.Background())
	if err != nil {
		_ = sub.Close()
		return nil, fmt.Errorf("initial network scan: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Monitor{
		cfg:     cfg,
		ctx:     ctx,
		cancel:  cancel,
		sub:     sub,
		scan:    scan,
		current: State{Generation: 1, Snapshot: initial.Clone(), ObservedAt: time.Now()},
		changes: make(chan State, 1),
		errors:  make(chan error, 1),
		done:    make(chan struct{}),
	}
	go m.run()
	return m, nil
}

// Current returns a copy of the latest authoritative state.
func (m *Monitor) Current() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneState(m.current)
}

// Changes delivers semantic state changes, coalescing to the latest state.
func (m *Monitor) Changes() <-chan State { return m.changes }

// Errors reports a terminal subscription or rescan failure.
func (m *Monitor) Errors() <-chan error { return m.errors }

func (m *Monitor) run() {
	defer close(m.done)
	defer close(m.changes)
	defer close(m.errors)

	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	pending := false
	events := m.sub.Events()
	errorsChannel := m.sub.Errors()
	for {
		select {
		case <-m.ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				if m.ctx.Err() == nil {
					m.publishError(ErrSubscriptionClosed)
				}
				return
			}
			if !pending {
				pending = true
				timer.Reset(m.cfg.Debounce)
			}
		case err, ok := <-errorsChannel:
			if !ok {
				errorsChannel = nil
				continue
			}
			if err == nil {
				err = ErrSubscriptionClosed
			}
			m.publishError(fmt.Errorf("network subscription: %w", err))
			return
		case <-timer.C:
			pending = false
			if err := m.rescan(); err != nil {
				m.publishError(err)
				return
			}
		}
	}
}

func (m *Monitor) rescan() error {
	snapshot, err := m.scan.Scan(m.ctx)
	if err != nil {
		if m.ctx.Err() != nil {
			return m.ctx.Err()
		}
		return fmt.Errorf("authoritative network rescan: %w", err)
	}
	m.mu.Lock()
	if m.current.Snapshot.transportEquivalent(snapshot) {
		m.current.Snapshot = snapshot.Clone()
		m.current.ObservedAt = time.Now()
		m.mu.Unlock()
		return nil
	}
	m.current = State{
		Generation: m.current.Generation + 1,
		Snapshot:   snapshot.Clone(),
		ObservedAt: time.Now(),
	}
	state := cloneState(m.current)
	m.mu.Unlock()
	m.publishLatest(state)
	return nil
}

func (m *Monitor) publishLatest(state State) {
	select {
	case m.changes <- state:
		return
	default:
	}
	select {
	case <-m.changes:
	default:
	}
	select {
	case m.changes <- state:
	default:
	}
}

func (m *Monitor) publishError(err error) {
	select {
	case m.errors <- err:
	default:
	}
}

// Close terminates all subscriptions and waits for the monitor goroutine.
func (m *Monitor) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.cancel()
		m.closeErr = m.sub.Close()
		<-m.done
	})
	return m.closeErr
}

func cloneState(state State) State {
	state.Snapshot = state.Snapshot.Clone()
	return state
}
