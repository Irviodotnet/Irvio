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
	"io"
	"net/netip"
	"sync"

	"hydra.local/hydra/internal/iceadapter"
	"hydra.local/hydra/internal/iceengine"
	"hydra.local/hydra/internal/icesession"
	"hydra.local/hydra/internal/networkstate"
	"hydra.local/hydra/internal/socketmux"
	"hydra.local/hydra/internal/stunprobe"
)

// Builder creates independent immutable transport generations.
type Builder struct{ config Config }

// NewBuilder validates a transport-generation factory.
func NewBuilder(config Config) (*Builder, error) {
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("create transport generation builder: %w", err)
	}
	config.STUNServers = cloneServers(config.STUNServers)
	config.STUNServerPools = cloneServerPools(config.STUNServerPools)
	return &Builder{config: config}, nil
}

// BuildTransport adapts Builder to Manager's Factory contract.
func (b *Builder) BuildTransport(ctx context.Context, state networkstate.State) (Transport, error) {
	return b.Build(ctx, state)
}

// Build uses the configured preferred port.
func (b *Builder) Build(ctx context.Context, state networkstate.State) (_ *Generation, resultErr error) {
	return b.build(ctx, state, b.config.Port)
}

// BuildOnAnyPort uses a common kernel-selected port when the predecessor still owns the preferred port.
func (b *Builder) BuildOnAnyPort(ctx context.Context, state networkstate.State) (_ *Generation, resultErr error) {
	return b.build(ctx, state, 0)
}

func (b *Builder) build(ctx context.Context, state networkstate.State, port int) (_ *Generation, resultErr error) {
	if ctx == nil || state.Generation == 0 {
		return nil, fmt.Errorf("build transport generation: invalid context or generation")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := b.config.Planner.Plan(state.Snapshot.Clone())
	if err != nil {
		return nil, fmt.Errorf("build transport generation: plan: %w", err)
	}
	if err := validatePlan(plan, state.Snapshot, b.config.Validation.AllowLoopback); err != nil {
		return nil, err
	}
	stunRegistry, err := stunprobe.NewRegistry(b.config.STUNRegistry)
	if err != nil {
		return nil, err
	}
	iceRegistry, err := iceadapter.NewRegistry(b.config.ICERegistry)
	if err != nil {
		return nil, err
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = port
	muxConfig.IPv4Policy = socketmux.FamilyDisabled
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	addresses := make([]netip.Addr, 0, 2)
	if plan.IPv4.IsValid() {
		muxConfig.IPv4Policy = socketmux.FamilyRequired
		muxConfig.IPv4Bind = plan.IPv4
		addresses = append(addresses, plan.IPv4)
	}
	if plan.IPv6.IsValid() {
		muxConfig.IPv6Policy = socketmux.FamilyRequired
		muxConfig.IPv6Bind = plan.IPv6
		addresses = append(addresses, plan.IPv6)
	}
	muxConfig.ControlQueueCapacity = b.config.ControlQueueCapacity
	muxConfig.DataQueueCapacity = b.config.DataQueueCapacity
	muxConfig.Classifier = socketmux.NewClassifierChain(stunprobe.NewClassifier(stunRegistry), iceadapter.NewClassifier(iceRegistry))
	muxConfig.Logger = b.config.Logger
	mux, err := socketmux.New(ctx, muxConfig)
	if err != nil {
		return nil, fmt.Errorf("build transport generation mux: %w", err)
	}
	if b.config.SocketMark != 0 {
		if err := mux.SetMark(b.config.SocketMark); err != nil {
			_ = mux.Close()
			return nil, fmt.Errorf("mark transport generation sockets: %w", err)
		}
	}
	phases := icesession.NewPhaseManager()
	var engine *iceengine.Engine
	var refreshClient *stunprobe.Client
	defer func() {
		if resultErr == nil {
			return
		}
		if engine != nil {
			_ = engine.Close()
		}
		if refreshClient != nil {
			_ = refreshClient.Close()
		}
		_ = phases.Close()
		_ = mux.Close()
	}()

	observations, err := b.probe(ctx, state.Generation, mux, phases, stunRegistry)
	if err != nil {
		return nil, err
	}
	if b.hasSTUNServers() {
		refreshClient, err = stunprobe.NewClientWithExternalDispatcher(mux.Control(), stunRegistry, b.config.STUN)
		if err != nil {
			return nil, err
		}
	}
	lease, err := phases.Acquire(ctx, icesession.ControlPhaseICE)
	if err != nil {
		return nil, fmt.Errorf("acquire ICE phase: %w", err)
	}
	engineConfig := iceengine.DefaultConfig(addresses...)
	engineConfig.TransportGeneration = uint64(state.Generation)
	engineConfig.MaxSessions = b.config.MaxSessions
	engineConfig.ConnectionQueueCapacity = b.config.ConnectionQueueCapacity
	engineConfig.Logger = b.config.Logger
	engineConfig.Session.GatherTimeout = b.config.GatherTimeout
	engineConfig.Session.Timing = b.config.Timing
	engineConfig.Session.Validation = b.config.Validation
	engineConfig.Session.Logger = b.config.Logger
	engineConfig.Session.SrflxObservations = make([]icesession.SrflxObservation, 0, len(observations))
	for _, observation := range observations {
		engineConfig.Session.SrflxObservations = append(engineConfig.Session.SrflxObservations, icesession.SrflxObservation{
			Family: observation.Family, LocalEndpoint: observation.LocalEndpoint, MappedEndpoint: observation.MappedEndpoint,
		})
	}
	if refreshClient != nil {
		engineConfig.STUNResponseHandler = refreshClient.HandleResponse
		engineConfig.RefreshSrflx = func(refreshContext context.Context) ([]icesession.SrflxObservation, error) {
			fresh, refreshErr := b.probeWithClient(refreshContext, state.Generation, mux, refreshClient)
			if refreshErr != nil {
				return nil, refreshErr
			}
			result := make([]icesession.SrflxObservation, 0, len(fresh))
			for _, observation := range fresh {
				result = append(result, icesession.SrflxObservation{
					Family: observation.Family, LocalEndpoint: observation.LocalEndpoint, MappedEndpoint: observation.MappedEndpoint,
				})
			}
			return result, nil
		}
	}
	engine, err = iceengine.New(mux, iceRegistry, lease, engineConfig)
	if err != nil {
		lease.Release()
		return nil, err
	}
	generationContext, cancel := context.WithCancel(context.Background())
	return &Generation{
		id: state.Generation, snapshot: state.Snapshot.Clone(), plan: plan, mux: mux, phases: phases,
		observations: append([]Observation(nil), observations...), engine: engine, stunClient: refreshClient,
		context: generationContext, cancel: cancel, lifecycle: LifecycleReady,
	}, nil
}

func (b *Builder) probe(ctx context.Context, generation networkstate.Generation, mux *socketmux.Mux, phases *icesession.PhaseManager, registry *stunprobe.Registry) ([]Observation, error) {
	if !b.hasSTUNServers() {
		return nil, nil
	}
	lease, err := phases.Acquire(ctx, icesession.ControlPhaseSTUN)
	if err != nil {
		return nil, fmt.Errorf("acquire STUN phase: %w", err)
	}
	client, err := stunprobe.NewClient(mux.Control(), registry, b.config.STUN)
	if err != nil {
		lease.Release()
		return nil, err
	}
	observations, probeErr := b.probeWithClient(ctx, generation, mux, client)
	closeErr := client.Close()
	lease.Release()
	return observations, errors.Join(probeErr, closeErr)
}

func (b *Builder) probeWithClient(ctx context.Context, generation networkstate.Generation, mux *socketmux.Mux, client *stunprobe.Client) ([]Observation, error) {
	var observations []Observation
	for _, family := range []socketmux.AddressFamily{socketmux.IPv4, socketmux.IPv6} {
		servers := b.serversForFamily(family)
		if len(servers) == 0 {
			continue
		}
		for _, server := range servers {
			if !familyMatches(server.Addr(), family) {
				return nil, fmt.Errorf("STUN server %s does not match %s", server, family)
			}
		}
		result, err := probeSTUNPool(ctx, client, servers)
		if err != nil {
			return nil, err
		}
		observations = append(observations, Observation{
			NetworkGeneration: generation, MuxPort: mux.Port(), Family: result.Family,
			LocalEndpoint: result.Local, MappedEndpoint: result.Mapped, ObservedAt: result.CompletedAt,
		})
	}
	return observations, nil
}

// probeSTUNPool probes seeds concurrently so a dead first seed cannot exhaust recovery.
func probeSTUNPool(ctx context.Context, client *stunprobe.Client, servers []netip.AddrPort) (stunprobe.Result, error) {
	if len(servers) == 1 {
		return client.Probe(ctx, servers[0])
	}
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		server netip.AddrPort
		result stunprobe.Result
		err    error
	}
	results := make(chan outcome, len(servers))
	var workers sync.WaitGroup
	for _, server := range servers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := client.Probe(probeCtx, server)
			results <- outcome{server: server, result: result, err: err}
		}()
	}
	var failures error
	for range servers {
		select {
		case item := <-results:
			if item.err == nil {
				cancel()
				workers.Wait()
				return item.result, nil
			}
			failures = errors.Join(failures, fmt.Errorf("STUN server %s: %w", item.server, item.err))
		case <-ctx.Done():
			cancel()
			workers.Wait()
			return stunprobe.Result{}, ctx.Err()
		}
	}
	return stunprobe.Result{}, failures
}

func (b *Builder) hasSTUNServers() bool {
	return len(b.config.STUNServers) != 0 || len(b.config.STUNServerPools) != 0
}

func (b *Builder) serversForFamily(family socketmux.AddressFamily) []netip.AddrPort {
	if pool := b.config.STUNServerPools[family]; len(pool) != 0 {
		return pool
	}
	if server, ok := b.config.STUNServers[family]; ok {
		return []netip.AddrPort{server}
	}
	return nil
}

func cloneServerPools(pools map[socketmux.AddressFamily][]netip.AddrPort) map[socketmux.AddressFamily][]netip.AddrPort {
	if pools == nil {
		return nil
	}
	clone := make(map[socketmux.AddressFamily][]netip.AddrPort, len(pools))
	for family, servers := range pools {
		clone[family] = append([]netip.AddrPort(nil), servers...)
	}
	return clone
}

func cloneServers(servers map[socketmux.AddressFamily]netip.AddrPort) map[socketmux.AddressFamily]netip.AddrPort {
	if servers == nil {
		return nil
	}
	clone := make(map[socketmux.AddressFamily]netip.AddrPort, len(servers))
	for family, server := range servers {
		clone[family] = server
	}
	return clone
}

func familyMatches(addr netip.Addr, family socketmux.AddressFamily) bool {
	return addr.IsValid() && !addr.Is4In6() && (family == socketmux.IPv4 && addr.Is4() || family == socketmux.IPv6 && addr.Is6())
}

// Generation owns one physical mux and its sole shared ICE engine.
type Generation struct {
	id           networkstate.Generation
	snapshot     networkstate.Snapshot
	plan         BindPlan
	mux          *socketmux.Mux
	phases       *icesession.PhaseManager
	observations []Observation
	engine       *iceengine.Engine
	stunClient   *stunprobe.Client
	dataPlane    io.Closer
	context      context.Context
	cancel       context.CancelFunc

	mu                sync.Mutex
	lifecycle         Lifecycle
	closeOnce         sync.Once
	closeErr          error
	dataPlaneBuilding bool
	dataPlaneWG       sync.WaitGroup
}

// ID returns the immutable semantic network generation.
func (g *Generation) ID() networkstate.Generation { return g.id }

// Snapshot returns an immutable copy of the generation input.
func (g *Generation) Snapshot() networkstate.Snapshot { return g.snapshot.Clone() }

// BindPlan returns the concrete generation addresses.
func (g *Generation) BindPlan() BindPlan { return g.plan }

// Mux returns the generation-owned physical transport.
func (g *Generation) Mux() *socketmux.Mux { return g.mux }

// Engine returns the sole generation-owned ICE engine.
func (g *Generation) Engine() *iceengine.Engine { return g.engine }

// Observations returns copied generation-bound STUN results.
func (g *Generation) Observations() []Observation {
	return append([]Observation(nil), g.observations...)
}

// Lifecycle returns the current one-way lifecycle state.
func (g *Generation) Lifecycle() Lifecycle {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lifecycle
}

// NewPreparedSession creates a session only while this generation is current.
func (g *Generation) NewPreparedSession(ctx context.Context) (*icesession.PreparedSession, error) {
	if g == nil || ctx == nil {
		return nil, ErrClosed
	}
	g.mu.Lock()
	if g.lifecycle != LifecycleReady {
		state := g.lifecycle
		g.mu.Unlock()
		if state == LifecycleStale {
			return nil, ErrStale
		}
		return nil, ErrClosed
	}
	g.mu.Unlock()
	prepareContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(g.context, cancel)
	session, err := g.engine.Prepare(prepareContext)
	stop()
	cancel()
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	ready := g.lifecycle == LifecycleReady
	g.mu.Unlock()
	if !ready || session.TransportGeneration() != uint64(g.id) {
		_ = session.Close()
		return nil, ErrStale
	}
	return session, nil
}

// InstallDataPlane installs one generation owner and closes late results after cancellation.
func (g *Generation) InstallDataPlane(factory DataPlaneFactory) (io.Closer, error) {
	if g == nil || factory == nil {
		return nil, ErrInvalidDataPlane
	}
	g.mu.Lock()
	if g.lifecycle != LifecycleReady {
		state := g.lifecycle
		g.mu.Unlock()
		if state == LifecycleStale {
			return nil, ErrStale
		}
		return nil, ErrClosed
	}
	if g.dataPlane != nil || g.dataPlaneBuilding {
		g.mu.Unlock()
		return nil, ErrDataPlaneExists
	}
	g.dataPlaneBuilding = true
	g.dataPlaneWG.Add(1)
	ctx := g.context
	mux := g.mux
	id := g.id
	g.mu.Unlock()

	dataPlane, err := factory(ctx, mux, id)
	g.mu.Lock()
	g.dataPlaneBuilding = false
	ready := g.lifecycle == LifecycleReady && ctx.Err() == nil
	if err == nil && dataPlane == nil {
		err = ErrInvalidDataPlane
	}
	if err == nil && ready {
		g.dataPlane = dataPlane
	}
	g.dataPlaneWG.Done()
	g.mu.Unlock()
	if err != nil {
		if dataPlane != nil {
			_ = dataPlane.Close()
		}
		return nil, err
	}
	if !ready {
		_ = dataPlane.Close()
		if g.Lifecycle() == LifecycleStale {
			return nil, ErrStale
		}
		return nil, ErrClosed
	}
	return dataPlane, nil
}

// DataPlane reports the currently installed generation-scoped data plane.
func (g *Generation) DataPlane() io.Closer {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.dataPlane
}

// MarkStale fences new work and cancels generation-bound preparation.
func (g *Generation) MarkStale() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lifecycle != LifecycleReady {
		return false
	}
	g.lifecycle = LifecycleStale
	g.cancel()
	return true
}

// Close releases Data Plane, ICE, and phase ownership before the physical mux.
func (g *Generation) Close() error {
	if g == nil {
		return nil
	}
	g.closeOnce.Do(func() {
		g.mu.Lock()
		g.lifecycle = LifecycleClosing
		g.cancel()
		g.mu.Unlock()
		g.dataPlaneWG.Wait()
		g.mu.Lock()
		dataPlane := g.dataPlane
		g.dataPlane = nil
		g.mu.Unlock()
		var dataPlaneErr error
		if dataPlane != nil {
			dataPlaneErr = dataPlane.Close()
		}
		engineErr := g.engine.Close()
		var stunErr error
		if g.stunClient != nil {
			stunErr = g.stunClient.Close()
		}
		g.closeErr = errors.Join(dataPlaneErr, engineErr, stunErr, g.phases.Close(), g.mux.Close())
		g.mu.Lock()
		g.lifecycle = LifecycleClosed
		g.mu.Unlock()
	})
	return g.closeErr
}
