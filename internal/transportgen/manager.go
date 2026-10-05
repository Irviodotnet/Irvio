// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package transportgen

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"hydra.local/hydra/internal/networkstate"
)

// Transport is the lifecycle boundary required by Manager.
type Transport interface {
	ID() networkstate.Generation
	MarkStale() bool
	Close() error
}

// Factory builds one transport from an authoritative state.
type Factory interface {
	BuildTransport(context.Context, networkstate.State) (Transport, error)
}

// FactoryFunc adapts a function to Factory.
type FactoryFunc func(context.Context, networkstate.State) (Transport, error)

// BuildTransport invokes f.
func (f FactoryFunc) BuildTransport(ctx context.Context, state networkstate.State) (Transport, error) {
	return f(ctx, state)
}

// StateSource supplies semantic generations and terminal monitor errors.
type StateSource interface {
	Current() networkstate.State
	Changes() <-chan networkstate.State
	Errors() <-chan error
	Close() error
}

// Migrator re-establishes logical sessions before transport promotion.
type Migrator interface {
	Migrate(context.Context, Transport) error
}

// MigratorFunc adapts a function to Migrator.
type MigratorFunc func(context.Context, Transport) error

// Migrate invokes f.
func (f MigratorFunc) Migrate(ctx context.Context, transport Transport) error {
	return f(ctx, transport)
}

// ManagerEventType identifies bounded transport lifecycle events.
type ManagerEventType uint8

const (
	ManagerPreparing ManagerEventType = iota + 1
	ManagerActivated
	ManagerBuildFailed
	ManagerMigrationFailed
	ManagerStaleDiscarded
	ManagerMonitorFailed
	ManagerClosed
)

// ManagerEvent contains no credentials or candidate descriptions.
type ManagerEvent struct {
	Type       ManagerEventType
	Generation networkstate.Generation
	At         time.Time
	Err        error
}

type managerResult struct {
	generation networkstate.Generation
	transport  Transport
	err        error
	migration  bool
}

// ManagerConfig bounds transport build and activation work.
type ManagerConfig struct {
	Source              StateSource
	Factory             Factory
	Migrator            Migrator
	EventCapacity       int
	BuildTimeout        time.Duration
	MaxConcurrentBuilds int
}

// Manager fences stale builds and performs make-before-break activation.
type Manager struct {
	config ManagerConfig
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	events chan ManagerEvent
	finish chan managerResult

	mu      sync.RWMutex
	current Transport
	desired networkstate.Generation
	started bool
	closed  bool

	buildCancels map[networkstate.Generation]context.CancelFunc
	pending      *networkstate.State
	wg           sync.WaitGroup
	closeOnce    sync.Once
	closeErr     error
}

// NewManager validates a transport lifecycle owner.
func NewManager(config ManagerConfig) (*Manager, error) {
	if config.MaxConcurrentBuilds == 0 {
		config.MaxConcurrentBuilds = 2
	}
	if config.Source == nil || config.Factory == nil || config.Migrator == nil || config.BuildTimeout <= 0 ||
		config.EventCapacity <= 0 || config.EventCapacity > 65536 || config.MaxConcurrentBuilds < 1 || config.MaxConcurrentBuilds > 16 {
		return nil, fmt.Errorf("create transport manager: invalid config")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		config: config, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		events: make(chan ManagerEvent, config.EventCapacity), finish: make(chan managerResult, config.MaxConcurrentBuilds),
		buildCancels: make(map[networkstate.Generation]context.CancelFunc, config.MaxConcurrentBuilds),
	}, nil
}

// Start builds and activates the source's initial authoritative generation.
func (m *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("start transport manager: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrClosed
	}
	if m.started {
		m.mu.Unlock()
		return fmt.Errorf("transport manager already started")
	}
	m.started = true
	m.mu.Unlock()
	stop := context.AfterFunc(ctx, m.cancel)
	go func() {
		defer stop()
		m.run()
	}()
	return nil
}

func (m *Manager) run() {
	defer close(m.done)
	defer close(m.events)
	changes := m.config.Source.Changes()
	errorsChannel := m.config.Source.Errors()
	m.scheduleBuild(m.config.Source.Current())
	for {
		select {
		case <-m.ctx.Done():
			for _, cancel := range m.buildCancels {
				cancel()
			}
			m.wg.Wait()
			m.closeFinishedTransports()
			m.mu.Lock()
			current := m.current
			m.current = nil
			m.closed = true
			m.mu.Unlock()
			if current != nil {
				_ = current.Close()
			}
			m.publish(ManagerEvent{Type: ManagerClosed, At: time.Now()})
			return
		case state, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			m.mu.RLock()
			desired := m.desired
			current := m.current
			m.mu.RUnlock()
			if state.Generation <= desired {
				continue
			}
			if current != nil {
				current.MarkStale()
			}
			m.scheduleBuild(state)
		case err, ok := <-errorsChannel:
			if !ok {
				errorsChannel = nil
				continue
			}
			if ok && err != nil {
				m.publish(ManagerEvent{Type: ManagerMonitorFailed, At: time.Now(), Err: err})
				m.cancel()
			}
		case result := <-m.finish:
			delete(m.buildCancels, result.generation)
			m.mu.RLock()
			desired := m.desired
			m.mu.RUnlock()
			if result.generation != desired {
				if result.transport != nil {
					result.transport.MarkStale()
					_ = result.transport.Close()
				}
				m.publish(ManagerEvent{Type: ManagerStaleDiscarded, Generation: result.generation, At: time.Now()})
				m.startPending()
				continue
			}
			if result.err != nil {
				eventType := ManagerBuildFailed
				if result.migration {
					eventType = ManagerMigrationFailed
				}
				m.publish(ManagerEvent{Type: eventType, Generation: result.generation, At: time.Now(), Err: result.err})
				m.startPending()
				continue
			}
			m.mu.Lock()
			previous := m.current
			m.current = result.transport
			m.mu.Unlock()
			if previous != nil && previous != result.transport {
				previous.MarkStale()
				_ = previous.Close()
			}
			m.publish(ManagerEvent{Type: ManagerActivated, Generation: result.generation, At: time.Now()})
			m.startPending()
		}
	}
}

func (m *Manager) scheduleBuild(state networkstate.State) {
	if state.Generation == 0 {
		m.publish(ManagerEvent{Type: ManagerBuildFailed, At: time.Now(), Err: fmt.Errorf("zero network generation")})
		return
	}
	m.mu.Lock()
	if state.Generation <= m.desired {
		m.mu.Unlock()
		return
	}
	m.desired = state.Generation
	m.mu.Unlock()
	state.Snapshot = state.Snapshot.Clone()
	for generation, cancel := range m.buildCancels {
		if generation < state.Generation {
			cancel()
		}
	}
	if len(m.buildCancels) >= m.config.MaxConcurrentBuilds {
		m.pending = &state
		return
	}
	m.startBuild(state)
}

func (m *Manager) startPending() {
	if m.pending == nil || m.ctx.Err() != nil || len(m.buildCancels) >= m.config.MaxConcurrentBuilds {
		return
	}
	state := *m.pending
	m.pending = nil
	m.startBuild(state)
}

func (m *Manager) startBuild(state networkstate.State) {
	ctx, cancel := context.WithTimeout(m.ctx, m.config.BuildTimeout)
	m.buildCancels[state.Generation] = cancel
	m.publish(ManagerEvent{Type: ManagerPreparing, Generation: state.Generation, At: time.Now()})
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		transport, err := m.config.Factory.BuildTransport(ctx, state)
		if err != nil {
			m.deliver(managerResult{generation: state.Generation, err: err})
			return
		}
		if transport == nil || transport.ID() != state.Generation {
			if transport != nil {
				_ = transport.Close()
			}
			m.deliver(managerResult{generation: state.Generation, err: fmt.Errorf("factory returned wrong transport generation")})
			return
		}
		if err := m.config.Migrator.Migrate(ctx, transport); err != nil {
			transport.MarkStale()
			_ = transport.Close()
			m.deliver(managerResult{generation: state.Generation, err: err, migration: true})
			return
		}
		m.deliver(managerResult{generation: state.Generation, transport: transport})
	}()
}

func (m *Manager) deliver(result managerResult) {
	if m.ctx.Err() != nil {
		if result.transport != nil {
			result.transport.MarkStale()
			_ = result.transport.Close()
		}
		return
	}
	select {
	case m.finish <- result:
	case <-m.ctx.Done():
		if result.transport != nil {
			result.transport.MarkStale()
			_ = result.transport.Close()
		}
	}
}

func (m *Manager) closeFinishedTransports() {
	for {
		select {
		case result := <-m.finish:
			if result.transport != nil {
				result.transport.MarkStale()
				_ = result.transport.Close()
			}
		default:
			return
		}
	}
}

// Current returns the active transport, which remains manager-owned.
func (m *Manager) Current() Transport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// Events returns bounded transport lifecycle notifications.
func (m *Manager) Events() <-chan ManagerEvent { return m.events }

func (m *Manager) publish(event ManagerEvent) {
	select {
	case m.events <- event:
		return
	default:
	}
	select {
	case <-m.events:
	default:
	}
	select {
	case m.events <- event:
	default:
	}
}

// Close stops monitor/build work and closes the active transport.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.mu.Lock()
		started := m.started
		if !started {
			m.closed = true
		}
		m.mu.Unlock()
		m.cancel()
		if started {
			<-m.done
		} else {
			close(m.done)
			close(m.events)
		}
		m.closeErr = errors.Join(m.config.Source.Close())
	})
	return m.closeErr
}
