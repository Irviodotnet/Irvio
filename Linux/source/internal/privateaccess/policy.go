// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"context"
	"errors"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/exitaccess"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/sync/semaphore"
)

const admissionSlots = 1024

type grantContext struct {
	revision uint64
	ctx      context.Context
	cancel   context.CancelFunc
}

type workState struct {
	count   int
	drained chan struct{}
}

type Work struct {
	Revision uint64
	Context  context.Context
	Done     func()
}

// Policy is one runtime's private admission fence; administrative revoke must go through it.
type Policy struct {
	store     *Store
	gate      *semaphore.Weighted
	mu        sync.Mutex
	grants    map[peer.ID]grantContext
	closed    bool
	work      map[peer.ID]*workState
	workCount int
}

func NewPolicy(store *Store) (*Policy, error) {
	if store == nil {
		return nil, ErrInvalid
	}
	if _, err := store.Snapshot(); err != nil {
		return nil, err
	}
	return &Policy{store: store, gate: semaphore.NewWeighted(admissionSlots), grants: make(map[peer.ID]grantContext), work: make(map[peer.ID]*workState)}, nil
}

// Begin tracks bounded asynchronous establishment without holding an admission lock through ICE.
func (p *Policy) Begin(ctx context.Context, client peer.ID, revision uint64) (Work, error) {
	permit, err := p.Acquire(ctx, client)
	if err != nil {
		return Work{}, err
	}
	defer permit.Release()
	if permit.Revision != revision {
		return Work{}, exitaccess.ErrDenied
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return Work{}, exitaccess.ErrDenied
	}
	if p.workCount >= 256 {
		return Work{}, ErrCapacity
	}
	work := p.work[client]
	if work == nil {
		work = &workState{drained: make(chan struct{})}
		p.work[client] = work
	}
	work.count++
	p.workCount++
	var once sync.Once
	done := func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			work.count--
			p.workCount--
			if work.count == 0 {
				close(work.drained)
				delete(p.work, client)
			}
		})
	}
	return Work{Revision: revision, Context: permit.Context, Done: done}, nil
}

func (p *Policy) Acquire(ctx context.Context, client peer.ID) (exitaccess.Permit, error) {
	if ctx == nil {
		return exitaccess.Permit{}, ErrInvalid
	}
	if err := p.gate.Acquire(ctx, 1); err != nil {
		return exitaccess.Permit{}, err
	}
	var once sync.Once
	release := func() { once.Do(func() { p.gate.Release(1) }) }
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		release()
		return exitaccess.Permit{}, exitaccess.ErrDenied
	}
	revision, err := p.store.GrantRevision(client)
	if err != nil {
		p.cancelAllLocked()
		release()
		return exitaccess.Permit{}, err
	}
	if revision == 0 {
		if grant, ok := p.grants[client]; ok {
			grant.cancel()
			delete(p.grants, client)
		}
		release()
		return exitaccess.Permit{}, exitaccess.ErrDenied
	}
	grant := p.grants[client]
	if grant.revision != revision {
		if grant.cancel != nil {
			grant.cancel()
		}
		grant.ctx, grant.cancel = context.WithCancel(context.Background())
		grant.revision = revision
		p.grants[client] = grant
	}
	return exitaccess.Permit{Revision: revision, Context: grant.ctx, Release: release}, nil
}

// Revoke returns success only after durable denial, cancellation and caller-owned resource cleanup.
func (p *Policy) Revoke(ctx context.Context, client peer.ID, cleanup func(context.Context, peer.ID) error) error {
	if ctx == nil || cleanup == nil {
		return ErrInvalid
	}
	if err := p.gate.Acquire(ctx, admissionSlots); err != nil {
		return err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.gate.Release(admissionSlots)
		return ErrClosed
	}
	work := p.work[client]
	err := p.store.Revoke(client)
	if grant, ok := p.grants[client]; ok {
		grant.cancel()
		delete(p.grants, client)
	}
	if err != nil && !errors.Is(err, ErrDenied) {
		p.cancelAllLocked()
	}
	p.mu.Unlock()
	err = errors.Join(err, cleanup(ctx, client))
	p.gate.Release(admissionSlots)
	if work != nil {
		select {
		case <-work.drained:
		case <-ctx.Done():
			err = errors.Join(err, ctx.Err())
		}
	}
	return err
}

func (p *Policy) cancelAllLocked() {
	for client, grant := range p.grants {
		grant.cancel()
		delete(p.grants, client)
	}
	p.closed = true
}

// Close denies new work and cancels attempts; the runtime still owns lease teardown.
func (p *Policy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelAllLocked()
}
