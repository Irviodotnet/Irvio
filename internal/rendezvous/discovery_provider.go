// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"crypto/rand"
	"math/big"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/discovery"
)

const (
	DefaultDiscoveryDeadline = 5 * time.Second
	DefaultNegativeMinimum   = 5 * time.Second
	DefaultNegativeMaximum   = 15 * time.Second
	DefaultRefreshMinimum    = 24 * time.Minute
	DefaultRefreshMaximum    = 36 * time.Minute
	failedServerCooldown     = 2 * time.Minute
)

type discoveryCall struct {
	done   chan struct{}
	values []peer.ID
	err    error
}

// DiscoveryServerProvider coalesces bounded lookups and caches verified servers, not live Exit presence.
type DiscoveryServerProvider struct {
	node   *discovery.Node
	now    func() time.Time
	slots  chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu             sync.Mutex
	cache          []peer.ID
	inflight       map[string]*discoveryCall
	negative       map[string]time.Time
	refresh        map[string]time.Time
	failed         map[peer.ID]time.Time
	closed         bool
	lookupOverride func(context.Context, string) ([]peer.ID, error)
}

func NewDiscoveryServerProvider(node *discovery.Node, concurrency int) (*DiscoveryServerProvider, error) {
	if node == nil || concurrency < 1 || concurrency > DefaultMaxConcurrentDHTLookup {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &DiscoveryServerProvider{
		node: node, now: time.Now, slots: make(chan struct{}, concurrency),
		ctx: ctx, cancel: cancel, inflight: make(map[string]*discoveryCall),
		negative: make(map[string]time.Time), refresh: make(map[string]time.Time),
	}, nil
}

func (p *DiscoveryServerProvider) Servers(ctx context.Context, country string, limit int) ([]peer.ID, error) {
	if ctx == nil || limit < 1 || limit > MaxRendezvousCache || country != "" && !validCountry(country) {
		return nil, ErrInvalid
	}
	now := p.now()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrClosed
	}
	cached := append([]peer.ID(nil), p.cache...)
	if until := p.negative[country]; until.After(now) {
		p.mu.Unlock()
		return truncatePeers(cached, limit), nil
	}
	if len(cached) != 0 && p.refresh[country].After(now) {
		p.mu.Unlock()
		return truncatePeers(cached, limit), nil
	}
	if call := p.inflight[country]; call != nil {
		if len(cached) != 0 {
			p.mu.Unlock()
			return truncatePeers(cached, limit), nil
		}
		p.mu.Unlock()
		select {
		case <-call.done:
			return truncatePeers(call.values, limit), call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &discoveryCall{done: make(chan struct{})}
	p.inflight[country] = call
	if len(cached) != 0 {
		p.wg.Add(1)
		go p.refreshBackground(country, call)
		p.mu.Unlock()
		return truncatePeers(cached, limit), nil
	}
	p.mu.Unlock()

	values, err := p.lookup(ctx, country)
	p.finishLookup(country, call, values, err)
	return truncatePeers(call.values, limit), call.err
}

func (p *DiscoveryServerProvider) refreshBackground(country string, call *discoveryCall) {
	defer p.wg.Done()
	ctx, cancel := context.WithTimeout(p.ctx, DefaultDiscoveryDeadline)
	defer cancel()
	values, err := p.lookup(ctx, country)
	p.finishLookup(country, call, values, err)
}

func (p *DiscoveryServerProvider) finishLookup(country string, call *discoveryCall, values []peer.ID, err error) {
	now := p.now()
	p.mu.Lock()
	filtered := make([]peer.ID, 0, len(values))
	for _, id := range values {
		if !p.failed[id].After(now) {
			filtered = append(filtered, id)
		}
	}
	values = filtered
	if len(values) != 0 {
		for _, id := range values {
			if !containsPeer(p.cache, id) {
				p.cache = append(p.cache, id)
				if len(p.cache) > MaxRendezvousCache {
					p.cache = append([]peer.ID(nil), p.cache[len(p.cache)-MaxRendezvousCache:]...)
				}
			}
		}
		delete(p.negative, country)
		p.refresh[country] = now.Add(randomBetween(DefaultRefreshMinimum, DefaultRefreshMaximum))
		if err != nil {
			p.refresh[country] = now.Add(randomBetween(DefaultNegativeMinimum, DefaultNegativeMaximum))
		}
		// Refresh partial caches when cooldown expires, not after the normal 24-36 minute interval.
		for _, until := range p.failed {
			if until.After(now) && until.Before(p.refresh[country]) {
				p.refresh[country] = until
			}
		}
	} else if err == nil {
		p.negative[country] = now.Add(randomBetween(DefaultNegativeMinimum, DefaultNegativeMaximum))
	} else if len(p.cache) != 0 {
		p.refresh[country] = now.Add(randomBetween(DefaultNegativeMinimum, DefaultNegativeMaximum))
	}
	call.values, call.err = append([]peer.ID(nil), values...), err
	if p.inflight[country] == call {
		delete(p.inflight, country)
		close(call.done)
	}
	p.mu.Unlock()
}

// ReportFailure removes stale hints and applies a bounded rediscovery cooldown.
func (p *DiscoveryServerProvider) ReportFailure(id peer.ID) {
	if id == "" {
		return
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.failed[id].After(now) {
		return
	}
	if p.failed == nil {
		p.failed = make(map[peer.ID]time.Time)
	}
	for peerID, until := range p.failed {
		if !until.After(now) {
			delete(p.failed, peerID)
		}
	}
	if len(p.failed) >= MaxRendezvousCache {
		var oldest peer.ID
		var expiry time.Time
		for peerID, until := range p.failed {
			if oldest == "" || until.Before(expiry) {
				oldest, expiry = peerID, until
			}
		}
		delete(p.failed, oldest)
	}
	p.failed[id] = now.Add(failedServerCooldown)
	kept := p.cache[:0]
	for _, cached := range p.cache {
		if cached != id {
			kept = append(kept, cached)
		}
	}
	p.cache = kept
	clear(p.refresh)
	clear(p.negative)
}

func (p *DiscoveryServerProvider) lookup(ctx context.Context, country string) ([]peer.ID, error) {
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	deadline := time.Now().Add(DefaultDiscoveryDeadline)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	lookupContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	lookup := p.lookupOverride
	if lookup == nil {
		lookup = p.node.FindRendezvous
	}
	values, err := lookup(lookupContext, country)
	if err != nil {
		// Preserve authenticated partial lookup results and their timeout error.
		return values, err
	}
	if len(values) == 0 && country != "" {
		values, err = lookup(lookupContext, "")
	}
	if err != nil {
		return values, err
	}
	return values, nil
}

func randomBetween(minimum, maximum time.Duration) time.Duration {
	if maximum <= minimum {
		return minimum
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(maximum-minimum)+1))
	if err != nil {
		return minimum
	}
	return minimum + time.Duration(value.Int64())
}

func (p *DiscoveryServerProvider) Snapshot() (cached, inFlight int) {
	if p == nil {
		return 0, 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.cache), len(p.inflight)
}

func (p *DiscoveryServerProvider) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.cancel()
	p.mu.Unlock()
	p.wg.Wait()
	return nil
}

var _ RendezvousServerProvider = (*DiscoveryServerProvider)(nil)
