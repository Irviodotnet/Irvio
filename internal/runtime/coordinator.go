// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"hydra.local/hydra/internal/exitselector"
	"hydra.local/hydra/internal/killswitch"
	"hydra.local/hydra/internal/networkstate"
	"hydra.local/hydra/internal/underlay"
)

type commandType uint8

const (
	commandConnect commandType = iota + 1
	commandDisconnect
	commandClose
)

type command struct {
	kind    commandType
	ctx     context.Context
	country exitselector.CountryIntent
	reply   chan error
}

type resultType uint8

const (
	resultProtected resultType = iota + 1
	resultUnderlay
	resultDisconnected
	resultTransport
)

type result struct {
	kind       resultType
	generation uint64
	sequence   uint64
	protection uint64
	network    networkstate.State
	underlay   underlay.State
	transport  networkstate.Generation
	err        error
	reply      chan error
}

// Runtime is the sole logical owner of the production Irvio lifecycle.
type Runtime struct {
	config Config

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	cmd    chan command
	result chan result
	events chan Event

	mu              sync.RWMutex
	status          Status
	started         bool
	closed          bool
	shutdownErr     error
	closeMu         sync.Mutex
	closeAttempted  bool
	shutdownCleanup func() error
}

func New(config Config) (*Runtime, error) {
	if config.UnderlayInterval == 0 {
		config.UnderlayInterval = 5 * time.Second
	}
	if config.Network == nil || config.Underlay == nil || config.Protection == nil ||
		config.NewSelector == nil || config.Now == nil || config.OperationTimeout <= 0 ||
		config.OperationTimeout > time.Minute || config.UnderlayInterval <= 0 ||
		config.UnderlayInterval > time.Minute || config.EventCapacity < 1 || config.EventCapacity > 4096 {
		return nil, ErrInvalidConfig
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{
		config: config, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		cmd: make(chan command, 16), result: make(chan result, 16),
		events: make(chan Event, config.EventCapacity),
		status: Status{State: StateStopped, UpdatedAt: config.Now().UTC()},
	}, nil
}

func (r *Runtime) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	if r.started {
		r.mu.Unlock()
		return ErrAlreadyStarted
	}
	r.started = true
	r.status.State = StateStarting
	r.status.RuntimeGeneration = 1
	r.status.NetworkGeneration = r.config.Network.Current().Generation
	r.status.LastReason = ReasonStart
	r.status.UpdatedAt = r.config.Now().UTC()
	r.mu.Unlock()
	stop := context.AfterFunc(ctx, r.cancel)
	go func() {
		defer stop()
		r.run()
	}()
	return nil
}

// Connect commits protection before selection; Status/Events report later promotion.
func (r *Runtime) Connect(ctx context.Context, country exitselector.CountryIntent) error {
	return r.send(command{kind: commandConnect, ctx: ctx, country: country})
}

func (r *Runtime) Disconnect(ctx context.Context) error {
	return r.send(command{kind: commandDisconnect, ctx: ctx})
}

func (r *Runtime) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *Runtime) Events() <-chan Event { return r.events }

func (r *Runtime) send(value command) error {
	if value.ctx == nil {
		return ErrInvalidConfig
	}
	r.mu.RLock()
	started, closed := r.started, r.closed
	r.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	if !started {
		return ErrNotStarted
	}
	value.reply = make(chan error, 1)
	select {
	case r.cmd <- value:
	case <-value.ctx.Done():
		return value.ctx.Err()
	case <-r.done:
		return ErrClosed
	}
	select {
	case err := <-value.reply:
		return err
	case <-value.ctx.Done():
		return value.ctx.Err()
	case <-r.done:
		return ErrClosed
	}
}

func (r *Runtime) run() {
	defer close(r.done)
	defer close(r.events)
	networkChanges := r.config.Network.Changes()
	networkErrors := r.config.Network.Errors()
	underlayTicker := time.NewTicker(r.config.UnderlayInterval)
	defer underlayTicker.Stop()
	state := StateIdle
	generation := uint64(1)
	protectionGeneration := uint64(0)
	var selector Selector
	var selectorEvents <-chan exitselector.Event
	var operationCancel context.CancelFunc
	var underlayCancel context.CancelFunc
	var underlaySequence uint64
	var transportCancel context.CancelFunc
	var transportSequence uint64
	var transportReady networkstate.Generation
	var latestUnderlay underlay.State
	set := func(next State, reason Reason, err error) {
		state = next
		r.mu.Lock()
		r.status.State = next
		r.status.RuntimeGeneration = generation
		r.status.LastReason = reason
		r.status.UpdatedAt = r.config.Now().UTC()
		protection := r.config.Protection.Snapshot()
		r.status.ProtectedMode = protection.State
		r.status.ProtectionAdopted = protection.Adopted
		r.mu.Unlock()
		r.publish(err)
	}
	cancelOperation := func() {
		if operationCancel != nil {
			operationCancel()
			operationCancel = nil
		}
	}
	closeSelector := func() error {
		if selector == nil {
			return nil
		}
		err := selector.Close()
		selectorEvents = nil
		if err == nil {
			selector = nil
		}
		return err
	}
	startTransport := func(network networkstate.State) {
		if r.config.Transport == nil {
			transportReady = network.Generation
			return
		}
		if transportCancel != nil {
			transportCancel()
		}
		transportSequence++
		sequence := transportSequence
		ctx, cancel := context.WithTimeout(r.ctx, r.config.OperationTimeout)
		transportCancel = cancel
		go func() {
			err := r.config.Transport.Activate(ctx, network)
			cancel()
			r.deliver(result{kind: resultTransport, sequence: sequence, network: network, transport: network.Generation, err: err})
		}()
	}
	startUnderlay := func(network networkstate.State) {
		if underlayCancel != nil {
			underlayCancel()
		}
		underlaySequence++
		sequence := underlaySequence
		token := generation
		ctx, cancel := context.WithTimeout(r.ctx, r.config.OperationTimeout)
		underlayCancel = cancel
		go func() {
			state, err := r.config.Underlay.Evaluate(ctx, network)
			cancel()
			r.deliver(result{kind: resultUnderlay, generation: token, sequence: sequence, network: network, underlay: state, err: err})
		}()
	}
	set(StateIdle, ReasonStart, nil)
	initialNetwork := r.config.Network.Current()
	startTransport(initialNetwork)
	startUnderlay(initialNetwork)
	for {
		select {
		case <-r.ctx.Done():
			generation++
			cancelOperation()
			if underlayCancel != nil {
				underlayCancel()
			}
			if transportCancel != nil {
				transportCancel()
			}
			set(StateStopping, ReasonShutdown, nil)
			pending := []func() error{closeSelector, r.config.Underlay.Close}
			if r.config.Core != nil {
				pending = append(pending, r.config.Core.Close)
			}
			if r.config.Transport != nil {
				pending = append(pending, r.config.Transport.Close)
			}
			pending = append(pending, r.config.Network.Close)
			r.shutdownCleanup = func() error {
				var shutdownErr error
				for i, closeResource := range pending {
					if closeResource == nil {
						continue
					}
					if err := closeResource(); err != nil {
						shutdownErr = errors.Join(shutdownErr, err)
					} else {
						pending[i] = nil
					}
				}
				if shutdownErr != nil {
					return shutdownErr
				}
				ctx, cancel := context.WithTimeout(context.Background(), r.config.OperationTimeout)
				defer cancel()
				if r.config.Protection.Snapshot().State.Protected() {
					if err := r.config.Protection.Disconnect(ctx); err != nil {
						return err
					}
				}
				return r.config.Protection.Close(ctx)
			}
			shutdownErr := r.shutdownCleanup()
			if shutdownErr == nil {
				r.shutdownCleanup = nil
			}
			reason := ReasonShutdown
			if shutdownErr != nil {
				reason = ReasonChildFailure
			}
			set(StateClosed, reason, shutdownErr)
			r.mu.Lock()
			r.closed = true
			r.shutdownErr = shutdownErr
			r.mu.Unlock()
			return

		case value := <-r.cmd:
			switch value.kind {
			case commandConnect:
				if state != StateIdle {
					value.reply <- ErrAlreadyConnected
					continue
				}
				if value.country == "" {
					value.reply <- ErrInvalidConfig
					continue
				}
				generation++
				cancelOperation()
				operationContext, cancel := context.WithCancel(r.ctx)
				operationCancel = cancel
				token := generation
				set(StateProtecting, ReasonConnect, nil)
				if snapshot := r.config.Protection.Snapshot(); snapshot.State == killswitch.StateProtectedConnecting {
					go r.deliver(result{kind: resultProtected, generation: token, protection: snapshot.Generation})
				} else {
					go func() {
						protected, err := r.config.Protection.Protect(operationContext)
						r.deliver(result{kind: resultProtected, generation: token, protection: protected, err: err})
					}()
				}
				r.mu.Lock()
				r.status.Country = value.country
				r.mu.Unlock()
				value.reply <- nil

			case commandDisconnect:
				if state == StateIdle {
					value.reply <- ErrNotConnected
					continue
				}
				generation++
				cancelOperation()
				if err := closeSelector(); err != nil {
					set(StateNoExit, ReasonChildFailure, err)
					value.reply <- err
					continue
				}
				set(StateDisconnecting, ReasonDisconnect, nil)
				token := generation
				go func() {
					// Complete bounded fail-closed teardown even if the requesting API client disconnects.
					ctx, cancel := context.WithTimeout(r.ctx, r.config.OperationTimeout)
					err := r.config.Protection.Disconnect(ctx)
					cancel()
					r.deliver(result{kind: resultDisconnected, generation: token, err: err, reply: value.reply})
				}()

			case commandClose:
				r.cancel()
				value.reply <- nil
			}

		case completed := <-r.result:
			if completed.kind == resultUnderlay && completed.sequence != underlaySequence {
				continue
			}
			if completed.kind == resultTransport && completed.sequence != transportSequence {
				continue
			}
			if completed.kind != resultUnderlay && completed.kind != resultTransport && completed.generation != generation {
				if completed.kind == resultProtected && completed.err == nil &&
					r.config.Protection.Snapshot().Generation == completed.protection {
					ctx, cancel := context.WithTimeout(context.Background(), r.config.OperationTimeout)
					_ = r.config.Protection.Disconnect(ctx)
					cancel()
				}
				if completed.reply != nil {
					completed.reply <- ErrGenerationStale
				}
				continue
			}
			switch completed.kind {
			case resultProtected:
				if completed.err != nil {
					cancelOperation()
					set(StateIdle, ReasonChildFailure, completed.err)
					continue
				}
				protectionGeneration = completed.protection
				network := r.config.Network.Current()
				initial := selectorUnderlay(r.config.Underlay.Current(), network)
				if transportReady != network.Generation {
					initial.IPv4Usable, initial.IPv6Usable = false, false
				}
				created, err := r.config.NewSelector(r.Status().Country, initial)
				if err != nil {
					_ = r.config.Protection.Disconnect(context.Background())
					set(StateIdle, ReasonChildFailure, err)
					continue
				}
				selector = created
				if err := selector.Start(r.ctx); err != nil {
					_ = selector.Close()
					selector = nil
					_ = r.config.Protection.Disconnect(context.Background())
					set(StateIdle, ReasonChildFailure, err)
					continue
				}
				selectorEvents = selector.Events()
				_ = r.config.Protection.Transition(protectionGeneration, killswitch.StateProtectedConnecting)
				set(StateDiscovering, ReasonProtected, nil)

			case resultUnderlay:
				latestUnderlay = completed.underlay
				if completed.network.Generation < r.Status().NetworkGeneration {
					continue
				}
				r.mu.Lock()
				r.status.NetworkGeneration = completed.network.Generation
				r.status.Underlay = completed.underlay
				r.mu.Unlock()
				if selector != nil {
					value := selectorUnderlay(completed.underlay, completed.network)
					if transportReady != completed.network.Generation {
						value.IPv4Usable, value.IPv6Usable = false, false
					}
					_ = selector.UpdateUnderlay(value)
				}
				if completed.err != nil {
					r.publish(completed.err)
				}

			case resultTransport:
				if completed.err != nil {
					transportReady = 0
					set(StateNoExit, ReasonChildFailure, completed.err)
					continue
				}
				transportReady = completed.transport
				r.mu.Lock()
				r.status.TransportGeneration = completed.transport
				if r.config.Transport != nil {
					if current := r.config.Transport.Current(); current != nil && current.ID() == completed.transport {
						r.status.MuxPort = current.Mux().Port()
					}
				}
				r.mu.Unlock()
				if selector != nil {
					_ = selector.UpdateUnderlay(selectorUnderlay(latestUnderlay, completed.network))
				}

			case resultDisconnected:
				if completed.err != nil {
					set(StateNoExit, ReasonChildFailure, completed.err)
				} else {
					protectionGeneration = 0
					r.mu.Lock()
					r.status.Country = ""
					r.status.ActivePeer = ""
					r.status.ReservePeer = ""
					r.status.ColdPeer = ""
					r.status.SessionGeneration = 0
					r.status.TunnelLeaseID = ""
					r.status.TunnelLeaseExpiry = time.Time{}
					r.status.AWGKeyEpoch = 0
					r.status.SelectedEndpoint = netip.AddrPort{}
					r.mu.Unlock()
					set(StateIdle, ReasonDisconnect, nil)
				}
				if completed.reply != nil {
					completed.reply <- completed.err
				}
			}

		case network, ok := <-networkChanges:
			if !ok {
				networkChanges = nil
				continue
			}
			r.mu.Lock()
			previous := r.status.NetworkGeneration
			if network.Generation > previous {
				r.status.NetworkGeneration = network.Generation
			}
			r.mu.Unlock()
			if network.Generation > previous {
				reconciled := true
				if r.config.OnNetworkChange != nil {
					reconcileContext, cancel := context.WithTimeout(r.ctx, r.config.OperationTimeout)
					err := r.config.OnNetworkChange(reconcileContext, network)
					cancel()
					if err != nil {
						reconciled = false
						set(StateNoExit, ReasonChildFailure, err)
					}
				}
				transportReady = 0
				if selector != nil {
					_ = selector.UpdateUnderlay(exitselector.UnderlayState{NetworkGeneration: network.Generation})
				}
				if reconciled {
					startTransport(network)
					if state != StateIdle {
						_ = r.config.Protection.Transition(protectionGeneration, killswitch.StateNetworkMigration)
						set(StateNetworkMigration, ReasonNetworkChanged, nil)
					}
				}
			}
			startUnderlay(network)

		case <-underlayTicker.C:
			startUnderlay(r.config.Network.Current())

		case err, ok := <-networkErrors:
			if !ok {
				networkErrors = nil
				continue
			}
			if err != nil {
				set(StateNoExit, ReasonChildFailure, err)
			}

		case event, ok := <-selectorEvents:
			if !ok {
				selectorEvents = nil
				continue
			}
			if selector == nil {
				continue
			}
			snapshot := selector.Current()
			r.mu.Lock()
			r.status.SessionGeneration = snapshot.Connection.SessionGeneration
			r.status.TunnelLeaseID = snapshot.Connection.TunnelLeaseID
			r.status.TunnelLeaseExpiry = snapshot.Connection.TunnelLeaseValidUntil
			r.status.AWGKeyEpoch = snapshot.Connection.AWGKeyEpoch
			r.status.SelectedEndpoint = snapshot.Connection.SelectedEndpoint
			if snapshot.Active != nil {
				r.status.ActivePeer = snapshot.Active.Candidate.PeerID
			} else {
				r.status.ActivePeer = ""
			}
			if snapshot.Reserve != nil {
				r.status.ReservePeer = snapshot.Reserve.Candidate.PeerID
			} else {
				r.status.ReservePeer = ""
			}
			if snapshot.Cold != nil {
				r.status.ColdPeer = snapshot.Cold.Candidate.PeerID
			} else {
				r.status.ColdPeer = ""
			}
			r.mu.Unlock()
			switch event.Type {
			case exitselector.EventActivePromoted, exitselector.EventSamePeerRecovered, exitselector.EventExitFailoverCompleted:
				_ = r.config.Protection.Transition(protectionGeneration, killswitch.StateConnected)
				set(StateConnected, ReasonActivePromoted, nil)
			case exitselector.EventActiveDisconnected:
				_ = r.config.Protection.Transition(protectionGeneration, killswitch.StateSamePeerRecovery)
				set(StateSamePeerRecovery, ReasonActiveDisconnected, nil)
				startUnderlay(r.config.Network.Current())
			case exitselector.EventReserveConnectStarted, exitselector.EventColdConnectStarted, exitselector.EventFreshBatchRequested:
				set(StateExitReselection, ReasonActiveDisconnected, nil)
			case exitselector.EventSamePeerRecoveryStarted:
				_ = r.config.Protection.Transition(protectionGeneration, killswitch.StateSamePeerRecovery)
				set(StateSamePeerRecovery, ReasonActiveDisconnected, nil)
			case exitselector.EventNoExitAvailable:
				_ = r.config.Protection.Transition(protectionGeneration, killswitch.StateNoExit)
				set(StateNoExit, ReasonNoExit, nil)
			case exitselector.EventLocalPromotionFailed:
				_ = r.config.Protection.Transition(protectionGeneration, killswitch.StateNoExit)
				set(StateError, ReasonChildFailure, event.Err)
			case exitselector.EventConnectionUpdated:
				set(state, ReasonLeaseRenewal, nil)
			case exitselector.EventUnderlayLost:
				_ = r.config.Protection.Transition(protectionGeneration, killswitch.StateNoExit)
				set(StateNoExit, ReasonUnderlayChanged, nil)
			}
		}
	}
}

func (r *Runtime) deliver(value result) {
	select {
	case r.result <- value:
	case <-r.ctx.Done():
		if value.reply != nil {
			value.reply <- ErrClosed
		}
	}
}

func (r *Runtime) publish(err error) {
	event := Event{Status: r.Status(), Err: err}
	select {
	case r.events <- event:
	default:
		select {
		case <-r.events:
		default:
		}
		select {
		case r.events <- event:
		default:
		}
	}
}

func selectorUnderlay(evidence underlay.State, network networkstate.State) exitselector.UnderlayState {
	configured := exitselector.UnderlayFromNetworkState(network)
	return exitselector.UnderlayState{
		NetworkGeneration: network.Generation,
		IPv4Usable: evidence.IPv4.State == underlay.Reachable ||
			evidence.IPv4.State == underlay.Unknown && configured.IPv4Usable,
		IPv6Usable: evidence.IPv6.State == underlay.Reachable ||
			evidence.IPv6.State == underlay.Unknown && configured.IPv6Usable,
	}
}

func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalidConfig
	}
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	r.mu.Lock()
	started := r.started
	if !started {
		r.closed = true
		r.status.State = StateClosed
	}
	r.mu.Unlock()
	r.cancel()
	if !started {
		return nil
	}
	retry := r.closeAttempted
	r.closeAttempted = true
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
	}
	if retry && r.shutdownErr != nil && r.shutdownCleanup != nil {
		err := r.shutdownCleanup()
		r.mu.Lock()
		r.shutdownErr = err
		r.status.ProtectedMode = r.config.Protection.Snapshot().State
		if err == nil {
			r.status.LastReason = ReasonShutdown
			r.shutdownCleanup = nil
		}
		r.mu.Unlock()
	}
	return r.shutdownErr
}

var ErrGenerationStale = fmt.Errorf("%w: asynchronous completion", ErrInvalidState)
