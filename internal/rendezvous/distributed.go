// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/libp2p/go-libp2p/core/peer"
)

type RendezvousServerProvider interface {
	Servers(context.Context, string, int) ([]peer.ID, error)
}

// Failure feedback invalidates hints, not Exit eligibility; explicit providers remain overrides.
func reportServerFailure(provider RendezvousServerProvider, id peer.ID, err error) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrRateLimited) || errors.Is(err, ErrBusy) {
		return
	}
	if reporter, ok := provider.(interface{ ReportFailure(peer.ID) }); ok {
		reporter.ReportFailure(id)
	}
}

func (p UnionServerProvider) ReportFailure(id peer.ID) {
	for _, provider := range p {
		reportServerFailure(provider, id, ErrRouteFailed)
	}
}

func (p FallbackServerProvider) ReportFailure(id peer.ID) {
	reportServerFailure(p.Primary, id, ErrRouteFailed)
	reportServerFailure(p.Fallback, id, ErrRouteFailed)
}

type StaticServerProvider struct {
	Peers []peer.ID
}

func (p StaticServerProvider) Servers(_ context.Context, _ string, limit int) ([]peer.ID, error) {
	if limit < 1 {
		return nil, ErrInvalid
	}
	result := make([]peer.ID, 0, min(limit, len(p.Peers)))
	seen := make(map[peer.ID]struct{}, len(p.Peers))
	for _, value := range p.Peers {
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

// UnionServerProvider combines configured and discovered peers, succeeding if any source supplies peers.
type UnionServerProvider []RendezvousServerProvider

func (p UnionServerProvider) Servers(ctx context.Context, country string, limit int) ([]peer.ID, error) {
	if ctx == nil || limit < 1 || limit > MaxRendezvousCache || len(p) == 0 {
		return nil, ErrInvalid
	}
	result := make([]peer.ID, 0, limit)
	seen := make(map[peer.ID]struct{}, limit)
	var failures []error
	for _, provider := range p {
		if provider == nil {
			failures = append(failures, ErrInvalid)
			continue
		}
		values, err := provider.Servers(ctx, country, limit)
		if err != nil {
			failures = append(failures, err)
		}
		for _, id := range values {
			if id == "" {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, id)
			if len(result) == limit {
				return result, nil
			}
		}
	}
	if len(result) != 0 {
		return result, nil
	}
	return nil, errors.Join(failures...)
}

// FallbackServerProvider keeps the configured working set until unavailable, then discovers alternatives.
type FallbackServerProvider struct {
	Primary  RendezvousServerProvider
	Fallback RendezvousServerProvider
}

func (p FallbackServerProvider) Servers(ctx context.Context, country string, limit int) ([]peer.ID, error) {
	if ctx == nil || p.Primary == nil || p.Fallback == nil {
		return nil, ErrInvalid
	}
	values, primaryErr := p.Primary.Servers(ctx, country, limit)
	if len(values) != 0 {
		return values, nil
	}
	fallback, fallbackErr := p.Fallback.Servers(ctx, country, limit)
	if len(fallback) != 0 {
		return fallback, nil
	}
	return nil, errors.Join(primaryErr, fallbackErr)
}

type FanoutSamplerOptions struct {
	Local       peer.ID
	Client      *Client
	Provider    RendezvousServerProvider
	Forwarder   *VerifiedForwarder
	Concurrency int
}

type FanoutSampler struct {
	local     peer.ID
	client    *Client
	provider  RendezvousServerProvider
	forwarder *VerifiedForwarder
	slots     chan struct{}
	rpcs      atomic.Uint64
	failures  atomic.Uint64
}

type FanoutSnapshot struct {
	RPCs     uint64
	Failures uint64
	InFlight int
}

func NewFanoutSampler(options FanoutSamplerOptions) (*FanoutSampler, error) {
	if options.Local == "" || options.Client == nil || options.Provider == nil || options.Forwarder == nil {
		return nil, fmt.Errorf("%w: fanout dependencies", ErrInvalid)
	}
	if options.Concurrency == 0 {
		options.Concurrency = DefaultMaxOutgoingSample
	}
	if options.Concurrency < 1 || options.Concurrency > DefaultMaxOutgoingSample {
		return nil, fmt.Errorf("%w: fanout concurrency", ErrInvalid)
	}
	return &FanoutSampler{
		local: options.Local, client: options.Client, provider: options.Provider,
		forwarder: options.Forwarder, slots: make(chan struct{}, options.Concurrency),
	}, nil
}

func (s *FanoutSampler) SampleRemote(ctx context.Context, requester peer.ID, country string, limit int, excluded []peer.ID) ([]Candidate, bool, error) {
	if ctx == nil || !validCountry(country) || limit < 1 || limit > MaxCandidates || len(excluded) > MaxExcludedPeers {
		return nil, true, ErrInvalid
	}
	servers, err := s.provider.Servers(ctx, country, MaxOwnerRoutes+2)
	if err != nil && len(servers) == 0 {
		return nil, true, err
	}
	if ctx.Err() != nil {
		return nil, true, ctx.Err()
	}
	discoveryPartial := err != nil
	selected := make([]peer.ID, 0, MaxOwnerRoutes)
	seen := make(map[peer.ID]struct{}, len(servers))
	for _, server := range servers {
		if server == "" || server == s.local || server == requester {
			continue
		}
		if _, duplicate := seen[server]; duplicate {
			continue
		}
		seen[server] = struct{}{}
		selected = append(selected, server)
		if len(selected) == MaxOwnerRoutes {
			break
		}
	}
	if len(selected) == 0 {
		return nil, true, nil
	}
	type result struct {
		candidates []Candidate
		partial    bool
		err        error
	}
	results := make(chan result, len(selected))
	var workers sync.WaitGroup
	for _, server := range selected {
		workers.Add(1)
		go func(server peer.ID) {
			defer workers.Done()
			select {
			case s.slots <- struct{}{}:
				defer func() { <-s.slots }()
			case <-ctx.Done():
				results <- result{partial: true, err: ctx.Err()}
				return
			}
			s.rpcs.Add(1)
			candidates, partial, err := s.client.SampleLocal(ctx, server, country, limit, excluded)
			if err != nil {
				s.failures.Add(1)
				reportServerFailure(s.provider, server, err)
			}
			results <- result{candidates: candidates, partial: partial, err: err}
		}(server)
	}
	workers.Wait()
	close(results)
	all := make([]Candidate, 0, MaxOwnerRoutes*limit)
	partial := discoveryPartial
	successes := 0
	for result := range results {
		if result.err != nil {
			partial = true
			continue
		}
		successes++
		partial = partial || result.partial
		for _, candidate := range result.candidates {
			if len(all) >= MaxOwnerRoutes*limit {
				partial = true
				break
			}
			if err := s.forwarder.Remember(candidate); err != nil {
				partial = true
				continue
			}
			all = append(all, candidate)
		}
	}
	if successes == 0 {
		return nil, true, ErrRouteFailed
	}
	merged, err := MergeCandidates(all...)
	if err != nil {
		return nil, true, err
	}
	return merged, partial, nil
}

func (s *FanoutSampler) Snapshot() FanoutSnapshot {
	return FanoutSnapshot{RPCs: s.rpcs.Load(), Failures: s.failures.Load(), InFlight: len(s.slots)}
}

var _ RemoteSampler = (*FanoutSampler)(nil)
