// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnellease

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

type RenewalClient interface {
	Renew(context.Context, Lease) (Lease, error)
	Release(context.Context, Lease) error
}

type RenewalTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type RenewalClock interface {
	Now() time.Time
	NewTimer(time.Duration) RenewalTimer
}

type RenewalJitter func(time.Duration) time.Duration

type realRenewalClock struct{}
type realRenewalTimer struct{ timer *time.Timer }

func (realRenewalClock) Now() time.Time { return time.Now() }
func (realRenewalClock) NewTimer(d time.Duration) RenewalTimer {
	return realRenewalTimer{timer: time.NewTimer(d)}
}
func (t realRenewalTimer) C() <-chan time.Time { return t.timer.C }
func (t realRenewalTimer) Stop() bool          { return t.timer.Stop() }

type RenewalEventType uint8

const (
	RenewalScheduled RenewalEventType = iota + 1
	RenewalStarted
	RenewalSucceeded
	RenewalFailed
	RenewalExpired
	RenewalClosed
)

type RenewalEvent struct {
	Type       RenewalEventType
	Generation uint64
	ValidUntil time.Time
	At         time.Time
	Err        error
}

type RenewerConfig struct {
	Client         RenewalClient
	Lease          Lease
	Clock          RenewalClock
	LeadTime       time.Duration
	RetryDelay     time.Duration
	RequestTimeout time.Duration
	EventCapacity  int
	Jitter         RenewalJitter
	ReleaseOnClose bool
}

func DefaultRenewerConfig(client RenewalClient, lease Lease) RenewerConfig {
	return RenewerConfig{
		Client: client, Lease: lease, Clock: realRenewalClock{},
		LeadTime: 20 * time.Second, RetryDelay: 2 * time.Second,
		RequestTimeout: 5 * time.Second, EventCapacity: 16, Jitter: cryptoRenewalJitter,
	}
}

// Renewer maintains one lease with a bounded timer without changing its ID or addresses.
type Renewer struct {
	config RenewerConfig
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	events chan RenewalEvent

	mu        sync.RWMutex
	lease     Lease
	started   bool
	closed    bool
	lastErr   error
	closeOnce sync.Once
	closeErr  error
}

func NewRenewer(config RenewerConfig) (*Renewer, error) {
	if config.Client == nil || config.Clock == nil || config.LeadTime <= 0 ||
		config.RetryDelay <= 0 || config.RequestTimeout <= 0 ||
		config.EventCapacity < 1 || config.EventCapacity > 1024 || config.Jitter == nil ||
		Validate(config.Lease, config.Clock.Now()) != nil {
		return nil, ErrInvalidConfig
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Renewer{
		config: config, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		events: make(chan RenewalEvent, config.EventCapacity), lease: config.Lease,
	}, nil
}

func (r *Renewer) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	if r.started {
		r.mu.Unlock()
		return ErrInvalidConfig
	}
	r.started = true
	r.mu.Unlock()
	stop := context.AfterFunc(ctx, r.cancel)
	go func() {
		defer stop()
		r.run()
	}()
	return nil
}

func (r *Renewer) Current() (Lease, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lease, r.lastErr
}

func (r *Renewer) Events() <-chan RenewalEvent { return r.events }

func (r *Renewer) run() {
	defer close(r.done)
	defer close(r.events)
	for {
		lease, _ := r.Current()
		now := r.config.Clock.Now()
		delay := nextRenewalDelay(lease.ValidUntil, now, r.config.LeadTime, r.config.Jitter)
		timer := r.config.Clock.NewTimer(delay)
		r.publish(RenewalEvent{Type: RenewalScheduled, Generation: lease.Generation, ValidUntil: lease.ValidUntil, At: now})
		select {
		case <-r.ctx.Done():
			timer.Stop()
			r.publish(RenewalEvent{Type: RenewalClosed, Generation: lease.Generation, ValidUntil: lease.ValidUntil, At: r.config.Clock.Now()})
			return
		case <-timer.C():
		}
		r.publish(RenewalEvent{Type: RenewalStarted, Generation: lease.Generation, ValidUntil: lease.ValidUntil, At: r.config.Clock.Now()})
		requestContext, cancel := context.WithTimeout(r.ctx, r.config.RequestTimeout)
		renewed, err := r.config.Client.Renew(requestContext, lease)
		cancel()
		if err == nil {
			if renewed.ID != lease.ID || renewed.ClientPeer != lease.ClientPeer ||
				renewed.ExitPeer != lease.ExitPeer || renewed.IPv4 != lease.IPv4 ||
				renewed.IPv6 != lease.IPv6 || renewed.Generation <= lease.Generation ||
				!renewed.ValidUntil.After(lease.ValidUntil) {
				err = fmt.Errorf("renewed lease changed immutable ownership: %w", ErrStaleGeneration)
			}
		}
		if err == nil {
			r.mu.Lock()
			if !r.closed && r.lease.Generation == lease.Generation {
				r.lease = renewed
				r.lastErr = nil
			} else {
				err = ErrStaleGeneration
			}
			r.mu.Unlock()
		}
		if err == nil {
			r.publish(RenewalEvent{Type: RenewalSucceeded, Generation: renewed.Generation, ValidUntil: renewed.ValidUntil, At: r.config.Clock.Now()})
			continue
		}
		r.mu.Lock()
		r.lastErr = err
		r.mu.Unlock()
		now = r.config.Clock.Now()
		if !lease.ValidUntil.After(now) {
			r.publish(RenewalEvent{Type: RenewalExpired, Generation: lease.Generation, ValidUntil: lease.ValidUntil, At: now, Err: err})
			return
		}
		r.publish(RenewalEvent{Type: RenewalFailed, Generation: lease.Generation, ValidUntil: lease.ValidUntil, At: now, Err: err})
		retry := r.config.RetryDelay
		if remaining := lease.ValidUntil.Sub(now); retry > remaining {
			retry = remaining
		}
		timer = r.config.Clock.NewTimer(retry)
		select {
		case <-r.ctx.Done():
			timer.Stop()
			r.publish(RenewalEvent{Type: RenewalClosed, Generation: lease.Generation, ValidUntil: lease.ValidUntil, At: r.config.Clock.Now()})
			return
		case <-timer.C():
		}
	}
}

func nextRenewalDelay(validUntil, now time.Time, lead time.Duration, jitter RenewalJitter) time.Duration {
	extra := time.Duration(0)
	if maximum := lead / 4; maximum > 0 {
		extra = jitter(maximum)
		if extra < 0 || extra > maximum {
			extra = 0
		}
	}
	delay := validUntil.Sub(now) - lead - extra
	if delay < 0 {
		return 0
	}
	return delay
}

func cryptoRenewalJitter(maximum time.Duration) time.Duration {
	if maximum <= 0 {
		return 0
	}
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(maximum)))
	if err != nil {
		return 0
	}
	return time.Duration(value.Int64())
}

func (r *Renewer) publish(event RenewalEvent) {
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

func (r *Renewer) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalidConfig
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		started := r.started
		lease := r.lease
		r.mu.Unlock()
		r.cancel()
		if started {
			select {
			case <-r.done:
			case <-ctx.Done():
				r.closeErr = ctx.Err()
			}
		}
		if r.config.ReleaseOnClose {
			r.closeErr = errors.Join(r.closeErr, r.config.Client.Release(ctx, lease))
		}
	})
	return r.closeErr
}
