// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	MaxExitRegistrations        = 3
	MaxRegistrationDialAttempts = 6
)

type RegistrationManagerOptions struct {
	Host             host.Host
	Servers          RendezvousServerProvider
	SignedDescriptor func() []byte
	Handler          IncomingSignalHandler
	Config           Config
	OperationTimeout time.Duration
	Logger           *slog.Logger
}

type RegistrationManagerSnapshot struct {
	Active  int
	Servers []peer.ID
	Closed  bool
}

type RegistrationManager struct {
	options RegistrationManagerOptions
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	events  chan RegistrationManagerSnapshot
	wake    chan struct{}

	mu         sync.Mutex
	active     map[peer.ID]*RegistrationClient
	descriptor [sha256.Size]byte
	closed     bool
	failures   int
	closeOnce  sync.Once
	closeErr   error
}

func NewRegistrationManager(options RegistrationManagerOptions) (*RegistrationManager, error) {
	if options.Host == nil || options.Servers == nil || options.SignedDescriptor == nil || options.Handler == nil {
		return nil, fmt.Errorf("%w: registration manager", ErrInvalid)
	}
	if err := options.Config.validate(); err != nil {
		return nil, err
	}
	if options.OperationTimeout == 0 {
		options.OperationTimeout = options.Config.PendingTimeout
	}
	if options.OperationTimeout <= 0 || options.OperationTimeout > time.Minute {
		return nil, ErrInvalid
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &RegistrationManager{
		options: options, active: make(map[peer.ID]*RegistrationClient),
		done: make(chan struct{}), events: make(chan RegistrationManagerSnapshot, 16), wake: make(chan struct{}, 1),
	}, nil
}

func (m *RegistrationManager) Start(parent context.Context) error {
	if parent == nil {
		return ErrInvalid
	}
	m.mu.Lock()
	if m.ctx != nil || m.closed {
		m.mu.Unlock()
		return ErrConflict
	}
	m.ctx, m.cancel = context.WithCancel(parent)
	m.mu.Unlock()
	go m.run()
	return nil
}

func (m *RegistrationManager) run() {
	defer close(m.done)
	defer close(m.events)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-timer.C:
			active := m.reconcile()
			delay := m.nextReconcileDelay(active)
			timer.Reset(delay)
		case <-m.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			active := m.reconcile()
			timer.Reset(m.nextReconcileDelay(active))
		}
	}
}

func (m *RegistrationManager) nextReconcileDelay(active int) time.Duration {
	delay := m.repairDelay(active)
	if refresh := m.options.Config.descriptorRefreshCheckInterval(); refresh < delay {
		return refresh
	}
	return delay
}

// DescriptorChanged coalesces significant updates without publishing on ordinary heartbeats.
func (m *RegistrationManager) DescriptorChanged() {
	if m == nil {
		return
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *RegistrationManager) reconcile() int {
	m.mu.Lock()
	for server, registration := range m.active {
		select {
		case <-registration.Done():
			delete(m.active, server)
			reportServerFailure(m.options.Servers, server, ErrRouteFailed)
		default:
		}
	}
	active := len(m.active)
	m.mu.Unlock()

	descriptor := m.options.SignedDescriptor()
	if len(descriptor) == 0 || len(descriptor) > MaxDescriptorSize {
		return active
	}
	digest := sha256.Sum256(descriptor)
	m.mu.Lock()
	changed := digest != m.descriptor
	current := make([]*RegistrationClient, 0, len(m.active))
	for _, registration := range m.active {
		current = append(current, registration)
	}
	m.mu.Unlock()
	if changed {
		updated := true
		for _, registration := range current {
			updateContext, cancel := context.WithTimeout(m.ctx, m.options.OperationTimeout)
			err := registration.Update(updateContext, descriptor)
			cancel()
			if err != nil {
				updated = false
			}
		}
		if updated {
			m.mu.Lock()
			m.descriptor = digest
			m.mu.Unlock()
		}
	}
	if active >= MaxExitRegistrations {
		m.failures = 0
		m.publish()
		return active
	}
	ctx, cancel := context.WithTimeout(m.ctx, m.options.OperationTimeout)
	defer cancel()
	servers, err := m.options.Servers.Servers(ctx, "", MaxRendezvousCache)
	if err != nil && len(servers) == 0 {
		if active == 0 {
			m.failures++
		}
		m.publish()
		return active
	}
	servers = append([]peer.ID(nil), servers...)
	shufflePeers(servers)
	attempts := 0
	for _, server := range servers {
		if active >= MaxExitRegistrations || attempts >= MaxRegistrationDialAttempts || ctx.Err() != nil {
			break
		}
		if server == m.options.Host.ID() {
			continue
		}
		m.mu.Lock()
		_, exists := m.active[server]
		m.mu.Unlock()
		if exists {
			continue
		}
		attempts++
		registration, err := OpenRegistration(ctx, RegistrationClientOptions{
			Host: m.options.Host, Server: server, SignedDescriptor: descriptor,
			Config: m.options.Config, Handler: m.options.Handler,
		})
		if err != nil {
			reportServerFailure(m.options.Servers, server, err)
			m.options.Logger.Warn("RENDEZVOUS_REGISTRATION_REPAIR_FAILED",
				"server_peer_id", server.String(),
				"error_class", registrationErrorClass(err),
			)
			continue
		}
		m.mu.Lock()
		if m.closed || m.ctx.Err() != nil {
			m.mu.Unlock()
			closeContext, closeCancel := context.WithTimeout(context.Background(), m.options.OperationTimeout)
			_ = registration.Close(closeContext)
			closeCancel()
			break
		}
		if _, duplicate := m.active[server]; duplicate {
			m.mu.Unlock()
			closeContext, closeCancel := context.WithTimeout(context.Background(), m.options.OperationTimeout)
			_ = registration.Close(closeContext)
			closeCancel()
			continue
		}
		m.active[server] = registration
		active++
		m.mu.Unlock()
	}
	if active > 0 {
		m.failures = 0
	} else {
		m.failures++
	}
	m.publish()
	return active
}

func registrationErrorClass(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, ErrBusy):
		return "busy"
	case errors.Is(err, ErrWrongPeer):
		return "wrong_peer"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	default:
		return "transport"
	}
}

func (m *RegistrationManager) repairDelay(active int) time.Duration {
	switch active {
	case 0:
		cap := time.Second << min(m.failures, 6)
		if cap > time.Minute {
			cap = time.Minute
		}
		return randomBetween(0, cap)
	case 1:
		return randomBetween(time.Second, 15*time.Second)
	case 2:
		return randomBetween(5*time.Second, 2*time.Minute)
	default:
		return 5 * time.Minute
	}
}

func (m *RegistrationManager) Snapshot() RegistrationManagerSnapshot {
	if m == nil {
		return RegistrationManagerSnapshot{Closed: true}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	servers := make([]peer.ID, 0, len(m.active))
	for server := range m.active {
		servers = append(servers, server)
	}
	return RegistrationManagerSnapshot{Active: len(servers), Servers: servers, Closed: m.closed}
}

func (m *RegistrationManager) Events() <-chan RegistrationManagerSnapshot { return m.events }

func (m *RegistrationManager) publish() {
	snapshot := m.Snapshot()
	select {
	case m.events <- snapshot:
	default:
		select {
		case <-m.events:
		default:
		}
		select {
		case m.events <- snapshot:
		default:
		}
	}
}

func (m *RegistrationManager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		if m.cancel != nil {
			m.cancel()
		}
		started := m.ctx != nil
		m.mu.Unlock()
		if started {
			<-m.done
		}
		m.mu.Lock()
		active := m.active
		m.active = make(map[peer.ID]*RegistrationClient)
		m.mu.Unlock()
		for _, registration := range active {
			m.closeErr = errors.Join(m.closeErr, registration.Close(ctx))
		}
	})
	return m.closeErr
}
