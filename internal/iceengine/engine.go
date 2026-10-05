// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceengine

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

// Engine owns exactly one shared adapter and one ICE Control lease.
type Engine struct {
	mux      *socketmux.Mux
	registry *iceadapter.Registry
	adapter  *iceadapter.Adapter
	lease    *icesession.Lease
	config   Config

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	closed   bool
	nextID   uint64
	sessions map[uint64]*icesession.PreparedSession
	creating map[uint64]struct{}
	wg       sync.WaitGroup
	counters counters

	closeOnce sync.Once
	closeErr  error
}

// New starts the sole ICE Control dispatcher for a transport generation.
func New(mux *socketmux.Mux, registry *iceadapter.Registry, lease *icesession.Lease, config Config) (*Engine, error) {
	if mux == nil || registry == nil || lease == nil {
		return nil, fmt.Errorf("create ICE engine: nil dependency")
	}
	if err := lease.Require(icesession.ControlPhaseICE); err != nil {
		return nil, fmt.Errorf("create ICE engine: %w", err)
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("create ICE engine: %w", err)
	}
	config.Session.ListenAddresses = append([]netip.Addr(nil), config.Session.ListenAddresses...)
	config.Session.SrflxObservations = append([]icesession.SrflxObservation(nil), config.Session.SrflxObservations...)
	adapterConfig := iceadapter.DefaultConfig(config.Session.ListenAddresses...)
	adapterConfig.ConnectionQueueCapacity = config.ConnectionQueueCapacity
	adapterConfig.STUNResponseHandler = config.STUNResponseHandler
	adapterConfig.Logger = config.Logger
	adapter, err := iceadapter.New(mux.Control(), registry, adapterConfig)
	if err != nil {
		return nil, fmt.Errorf("create ICE engine adapter: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		mux: mux, registry: registry, adapter: adapter, lease: lease, config: config,
		ctx: ctx, cancel: cancel, sessions: make(map[uint64]*icesession.PreparedSession, config.MaxSessions),
		creating: make(map[uint64]struct{}, config.MaxSessions),
	}, nil
}

// Prepare creates an independent Pion Agent over the shared adapter.
func (e *Engine) Prepare(ctx context.Context) (*icesession.PreparedSession, error) {
	if e == nil || ctx == nil {
		return nil, fmt.Errorf("prepare ICE engine session: nil dependency")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, ErrClosed
	}
	if len(e.sessions)+len(e.creating) >= e.config.MaxSessions {
		e.mu.Unlock()
		return nil, ErrTooManySessions
	}
	e.nextID++
	id := e.nextID
	e.creating[id] = struct{}{}
	e.wg.Add(1)
	e.mu.Unlock()
	defer e.wg.Done()

	prepareContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.ctx, cancel)
	defer func() {
		stop()
		cancel()
	}()
	sessionConfig := e.config.Session
	sessionConfig.SrflxObservations = append([]icesession.SrflxObservation(nil), sessionConfig.SrflxObservations...)
	if e.config.RefreshSrflx != nil {
		observations, refreshErr := e.config.RefreshSrflx(prepareContext)
		if refreshErr != nil {
			e.release(id)
			if e.ctx.Err() != nil && errors.Is(refreshErr, context.Canceled) {
				return nil, ErrClosed
			}
			return nil, fmt.Errorf("refresh ICE server-reflexive candidates: %w", refreshErr)
		}
		sessionConfig.SrflxObservations = append([]icesession.SrflxObservation(nil), observations...)
		for _, observation := range observations {
			if e.config.Logger == nil {
				continue
			}
			e.config.Logger.Info("ICE_SRFLX_REFRESHED",
				"transport_generation", e.config.TransportGeneration,
				"ice_session", id,
				"family", observation.Family.String(),
				"local_endpoint", observation.LocalEndpoint,
				"mapped_endpoint", observation.MappedEndpoint,
			)
		}
	}
	session, err := icesession.PrepareWithSharedAdapter(
		prepareContext, e.adapter, e.mux.Port(), e.config.TransportGeneration, sessionConfig, func() { e.release(id) },
	)
	if err != nil {
		e.release(id)
		if e.ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return nil, ErrClosed
		}
		return nil, err
	}
	e.mu.Lock()
	delete(e.creating, id)
	if e.closed {
		e.mu.Unlock()
		_ = session.Close()
		return nil, ErrClosed
	}
	e.sessions[id] = session
	active := len(e.sessions)
	e.counters.created.Add(1)
	updatePeak(&e.counters.peak, uint64(active))
	e.mu.Unlock()
	return session, nil
}

func (e *Engine) release(id uint64) {
	e.mu.Lock()
	delete(e.creating, id)
	if _, ok := e.sessions[id]; ok {
		delete(e.sessions, id)
		e.counters.closed.Add(1)
	}
	e.mu.Unlock()
}

// Adapter returns the engine-owned adapter for bounded diagnostics.
func (e *Engine) Adapter() *iceadapter.Adapter {
	if e == nil {
		return nil
	}
	return e.adapter
}

// Stats returns a race-safe engine snapshot.
func (e *Engine) Stats() Stats {
	if e == nil {
		return Stats{}
	}
	e.mu.Lock()
	active := len(e.sessions)
	e.mu.Unlock()
	return Stats{
		SessionsCreated: e.counters.created.Load(), SessionsClosed: e.counters.closed.Load(),
		ActiveSessions: uint64(active), PeakSessions: e.counters.peak.Load(), Adapter: e.adapter.Stats(),
	}
}

// Close closes every Agent, then the sole adapter, and finally the ICE lease.
func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closed = true
		e.cancel()
		e.mu.Unlock()
		e.wg.Wait()
		e.mu.Lock()
		sessions := make([]*icesession.PreparedSession, 0, len(e.sessions))
		for _, session := range e.sessions {
			sessions = append(sessions, session)
		}
		e.mu.Unlock()
		var errs []error
		for _, session := range sessions {
			errs = append(errs, session.Close())
		}
		errs = append(errs, e.adapter.Close())
		e.lease.Release()
		e.closeErr = errors.Join(errs...)
	})
	return e.closeErr
}
