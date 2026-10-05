// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/protocol"
	"hydra.local/hydra/internal/serviceindex"
)

const maxCandidateLifetime = 24 * time.Hour

func normalizeCountry(country CountryIntent) (CountryIntent, error) {
	normalized, err := serviceindex.NormalizeCountry(string(country))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidCountry, err)
	}
	return CountryIntent(normalized), nil
}

func validateCandidate(candidate Candidate, country CountryIntent, local peer.ID, now time.Time) error {
	if candidate.PeerID == "" || candidate.PeerID == local || candidate.PeerID.Validate() != nil {
		return ErrInvalidCandidate
	}
	normalized, err := normalizeCountry(CountryIntent(candidate.Country))
	if err != nil || normalized != country {
		return ErrInvalidCandidate
	}
	endpoint := candidate.ControlEndpoint
	address := endpoint.Addr()
	if candidate.Transport != CandidateTransportDirect && candidate.Transport != CandidateTransportBrokered {
		return ErrInvalidCandidate
	}
	if candidate.Transport == CandidateTransportDirect &&
		(!endpoint.IsValid() || endpoint.Port() == 0 || address.IsUnspecified() || address.IsMulticast() ||
			address.IsLoopback() || address.Is4In6() || address.IsLinkLocalUnicast() || address.Zone() != "") {
		return ErrInvalidCandidate
	}
	if candidate.Transport == CandidateTransportBrokered &&
		(candidate.DescriptorID == ([32]byte{}) || candidate.DescriptorIncarnation == 0 || candidate.DescriptorSequence == 0) {
		return ErrInvalidCandidate
	}
	if candidate.ProtocolVersion != protocol.ProtocolVersion || candidate.Capabilities&protocol.CapExit == 0 || !candidate.Accepting {
		return ErrInvalidCandidate
	}
	if candidate.Uptime != UptimeNew && candidate.Uptime != UptimeMature {
		return ErrInvalidCandidate
	}
	if candidate.NetworkEpoch == 0 || candidate.GeneratedAt.IsZero() || candidate.ExpiresAt.IsZero() ||
		!candidate.ExpiresAt.After(now) || !candidate.ExpiresAt.After(candidate.GeneratedAt) ||
		candidate.ExpiresAt.Sub(candidate.GeneratedAt) > maxCandidateLifetime || candidate.GeneratedAt.After(now.Add(5*time.Minute)) {
		return ErrInvalidCandidate
	}
	return nil
}

func cloneCandidate(candidate Candidate) Candidate { return candidate }

func cloneEvaluation(value *CandidateEvaluation) *CandidateEvaluation {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Candidate = cloneCandidate(value.Candidate)
	return &copy
}

func cloneSelection(selection Selection) Selection {
	return Selection{Active: cloneEvaluation(selection.Active), Reserve: cloneEvaluation(selection.Reserve), Cold: cloneEvaluation(selection.Cold)}
}

type EvaluationConfig struct {
	LocalPeer            peer.ID
	Country              CountryIntent
	Now                  time.Time
	MinimumBandwidthMbps uint32
	ProbeConcurrency     int
	ProbeTimeout         time.Duration
	LatencyTolerance     time.Duration
	Suppressed           map[peer.ID]uint64
	SuppressedRoutes     map[peer.ID]map[uint64]struct{}
	Excluded             map[peer.ID]struct{}
}

type evaluationHooks struct {
	started   func(Candidate)
	completed func(Candidate, ProbeResult)
}

type evaluatedCandidate struct {
	value CandidateEvaluation
	ok    bool
}

// EvaluateBatch filters candidates, probes concurrently, and applies bounded M9 ranking.
func EvaluateBatch(ctx context.Context, batch CandidateBatch, probe CandidateProbe, config EvaluationConfig) (Selection, error) {
	return evaluateBatch(ctx, batch, probe, config, evaluationHooks{})
}

func evaluateBatch(ctx context.Context, batch CandidateBatch, probe CandidateProbe, config EvaluationConfig, hooks evaluationHooks) (Selection, error) {
	if ctx == nil || probe == nil || config.LocalPeer == "" || config.LocalPeer.Validate() != nil || config.ProbeConcurrency < 1 || config.ProbeConcurrency > 5 || config.ProbeTimeout <= 0 || config.LatencyTolerance <= 0 {
		return Selection{}, ErrInvalidConfig
	}
	country, err := normalizeCountry(config.Country)
	if err != nil {
		return Selection{}, err
	}
	if batch.Country != country || len(batch.Candidates) > MaxCandidateBatch || batch.AvailablePeerCount < len(batch.Candidates) || batch.GeneratedAt.IsZero() {
		return Selection{}, ErrInvalidProvider
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if batch.GeneratedAt.After(now.Add(5 * time.Minute)) {
		return Selection{}, ErrInvalidProvider
	}
	seen := make(map[peer.ID]struct{}, len(batch.Candidates))
	filtered := make([]Candidate, 0, len(batch.Candidates))
	for _, candidate := range batch.Candidates {
		if _, duplicate := seen[candidate.PeerID]; duplicate {
			continue
		}
		seen[candidate.PeerID] = struct{}{}
		if _, excluded := config.Excluded[candidate.PeerID]; excluded {
			continue
		}
		if validateCandidate(candidate, country, config.LocalPeer, now) != nil || candidate.AvailableBandwidthMbps < config.MinimumBandwidthMbps {
			continue
		}
		if epoch, suppressed := config.Suppressed[candidate.PeerID]; suppressed && epoch == candidate.NetworkEpoch {
			continue
		}
		if routes := config.SuppressedRoutes[candidate.PeerID]; candidate.SignalingRouteEpoch != 0 {
			if _, suppressed := routes[candidate.SignalingRouteEpoch]; suppressed {
				continue
			}
		}
		filtered = append(filtered, cloneCandidate(candidate))
	}
	if len(filtered) == 0 {
		return Selection{}, nil
	}
	workers := config.ProbeConcurrency
	if workers > len(filtered) {
		workers = len(filtered)
	}
	jobs := make(chan Candidate)
	results := make(chan evaluatedCandidate, len(filtered))
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for candidate := range jobs {
				if hooks.started != nil {
					hooks.started(candidate)
				}
				probeContext, cancel := context.WithTimeout(ctx, config.ProbeTimeout)
				result, probeErr := probe.Probe(probeContext, candidate)
				cancel()
				if hooks.completed != nil {
					hooks.completed(candidate, result)
				}
				validKind := result.Kind == ProbeDirectControl || result.Kind == ProbeBrokerMetadata
				valid := probeErr == nil && result.Reachable && result.Latency >= 0 && !result.ObservedAt.IsZero() &&
					result.NetworkEpoch == candidate.NetworkEpoch && validKind
				results <- evaluatedCandidate{value: CandidateEvaluation{Candidate: candidate, Probe: result}, ok: valid}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, candidate := range filtered {
			select {
			case jobs <- candidate:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		group.Wait()
		close(results)
	}()
	evaluated := make([]CandidateEvaluation, 0, len(filtered))
	for result := range results {
		if result.ok {
			evaluated = append(evaluated, result.value)
		}
	}
	if err := ctx.Err(); err != nil {
		return Selection{}, err
	}
	sort.Slice(evaluated, func(i, j int) bool {
		leftDirect := evaluated[i].Probe.Kind == ProbeDirectControl
		rightDirect := evaluated[j].Probe.Kind == ProbeDirectControl
		if leftDirect && rightDirect {
			leftBucket := evaluated[i].Probe.Latency / config.LatencyTolerance
			rightBucket := evaluated[j].Probe.Latency / config.LatencyTolerance
			if leftBucket != rightBucket {
				return leftBucket < rightBucket
			}
		}
		if evaluated[i].Candidate.Uptime != evaluated[j].Candidate.Uptime {
			return evaluated[i].Candidate.Uptime == UptimeMature
		}
		return evaluated[i].Candidate.PeerID.String() < evaluated[j].Candidate.PeerID.String()
	})
	selection := Selection{}
	for index := range evaluated {
		value := evaluated[index]
		switch index {
		case 0:
			selection.Active = &value
		case 1:
			selection.Reserve = &value
		case 2:
			selection.Cold = &value
		default:
			return selection, nil
		}
	}
	return selection, nil
}
