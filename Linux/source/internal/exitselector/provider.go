// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// CandidateSource keeps the eligible pool provider-side; clients receive at most ten sampled candidates.
type CandidateSource interface {
	EligibleCandidates(context.Context, CountryIntent) ([]Candidate, error)
}

type SamplingProvider struct {
	Source    CandidateSource
	LocalPeer peer.ID
	Random    Random
	Now       func() time.Time
}

func (p SamplingProvider) GetCandidates(ctx context.Context, request CandidateRequest) (CandidateBatch, error) {
	if ctx == nil || p.Source == nil || p.Random == nil || p.LocalPeer == "" || p.LocalPeer.Validate() != nil {
		return CandidateBatch{}, ErrInvalidConfig
	}
	country, err := normalizeCountry(request.Country)
	if err != nil {
		return CandidateBatch{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = MaxCandidateBatch
	}
	if limit < 1 || limit > MaxCandidateBatch || len(request.ExcludePeerIDs) > 4096 {
		return CandidateBatch{}, ErrInvalidProvider
	}
	pool, err := p.Source.EligibleCandidates(ctx, country)
	if err != nil {
		return CandidateBatch{}, err
	}
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	excluded := make(map[peer.ID]struct{}, len(request.ExcludePeerIDs)+1)
	excluded[p.LocalPeer] = struct{}{}
	for _, id := range request.ExcludePeerIDs {
		if id == "" || id.Validate() != nil {
			return CandidateBatch{}, fmt.Errorf("%w: invalid excluded PeerID", ErrInvalidProvider)
		}
		excluded[id] = struct{}{}
	}
	seen := make(map[peer.ID]struct{}, len(pool))
	eligible := make([]Candidate, 0, len(pool))
	for _, candidate := range pool {
		if _, skip := excluded[candidate.PeerID]; skip {
			continue
		}
		if _, duplicate := seen[candidate.PeerID]; duplicate {
			continue
		}
		seen[candidate.PeerID] = struct{}{}
		if validateCandidate(candidate, country, p.LocalPeer, now) != nil {
			continue
		}
		eligible = append(eligible, cloneCandidate(candidate))
	}
	available := len(eligible)
	for index := 0; index < len(eligible) && index < limit; index++ {
		offset, randomErr := p.Random.Index(len(eligible) - index)
		if randomErr != nil {
			return CandidateBatch{}, fmt.Errorf("sample Exit candidates: %w", randomErr)
		}
		selected := index + offset
		eligible[index], eligible[selected] = eligible[selected], eligible[index]
	}
	if len(eligible) > limit {
		eligible = eligible[:limit]
	}
	return CandidateBatch{Country: country, AvailablePeerCount: available, GeneratedAt: now, Candidates: eligible}, nil
}
