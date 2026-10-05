// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pion/ice/v4"

	"hydra.local/hydra/internal/icesession"
)

// LoadRemoteCandidates validates and loads the full non-trickle set exactly once.
func (a *Attempt) LoadRemoteCandidates(ctx context.Context) error {
	if a == nil {
		return ErrClosed
	}
	if ctx == nil {
		return fmt.Errorf("load remote ICE candidates: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	if a.state != StatePrepared {
		a.stats.stateMisuseErrors.Add(1)
		state := a.state
		a.mu.Unlock()
		if state == StateClosed {
			return ErrClosed
		}
		return fmt.Errorf("%w: load from state %d", ErrInvalidState, state)
	}
	a.state = StateLoading
	a.mu.Unlock()

	expected := a.remote.Description.Clone()
	converted := make([]ice.Candidate, 0, len(expected.Candidates))
	for _, candidate := range expected.Candidates {
		pionCandidate, err := icesession.CandidateToPion(candidate, a.config.Validation)
		if err != nil {
			a.stats.candidateLoadFailures.Add(1)
			return a.fail("load remote ICE candidates", errors.Join(ErrCandidateLoad, err))
		}
		converted = append(converted, pionCandidate)
	}
	for _, candidate := range converted {
		if err := a.agent.AddRemoteCandidate(candidate); err != nil {
			a.stats.candidateLoadFailures.Add(1)
			return a.fail("load remote ICE candidates", errors.Join(ErrCandidateLoad, err))
		}
	}

	loadContext, cancel := context.WithTimeout(ctx, a.config.CandidateLoadTimeout)
	defer cancel()
	ticker := time.NewTicker(a.config.CandidatePollInterval)
	defer ticker.Stop()
	for {
		actual, err := a.agent.GetRemoteCandidates()
		if err != nil {
			a.stats.candidateLoadFailures.Add(1)
			return a.fail("load remote ICE candidates", errors.Join(ErrCandidateLoad, contextFailure(loadContext, err, &a.stats)))
		}
		if len(actual) > len(expected.Candidates) {
			a.stats.candidateLoadFailures.Add(1)
			return a.fail("load remote ICE candidates", errors.Join(ErrCandidateLoad, fmt.Errorf("agent contains %d candidates, expected %d", len(actual), len(expected.Candidates))))
		}
		if len(actual) == len(expected.Candidates) {
			if err := verifyCandidateSet(actual, expected.Candidates, a.config.Validation); err != nil {
				a.stats.candidateLoadFailures.Add(1)
				return a.fail("load remote ICE candidates", errors.Join(ErrCandidateLoad, err))
			}
			a.mu.Lock()
			if a.state != StateLoading {
				state := a.state
				a.mu.Unlock()
				if state == StateClosed {
					return ErrClosed
				}
				return a.fail("load remote ICE candidates", errors.Join(ErrCandidateLoad, ErrInvalidState))
			}
			a.state = StateRemoteLoaded
			a.mu.Unlock()
			a.stats.remoteCandidatesAdded.Add(uint64(len(actual)))
			a.stats.remoteCandidateSetsLoaded.Add(1)
			return nil
		}
		select {
		case <-loadContext.Done():
			a.stats.candidateLoadFailures.Add(1)
			return a.fail("load remote ICE candidates", errors.Join(ErrCandidateLoad, contextFailure(loadContext, loadContext.Err(), &a.stats)))
		case <-ticker.C:
		}
	}
}

func verifyCandidateSet(actual []ice.Candidate, expected []icesession.Candidate, config icesession.ValidationConfig) error {
	want := make(map[icesession.Candidate]struct{}, len(expected))
	for _, candidate := range expected {
		want[candidate] = struct{}{}
	}
	seen := make(map[icesession.Candidate]struct{}, len(actual))
	for _, candidate := range actual {
		converted, err := icesession.CandidateFromPion(candidate, config)
		if err != nil {
			return err
		}
		if _, ok := want[converted]; !ok {
			return fmt.Errorf("agent contains unexpected remote candidate")
		}
		if _, duplicate := seen[converted]; duplicate {
			return fmt.Errorf("agent contains duplicate remote candidate")
		}
		seen[converted] = struct{}{}
	}
	if len(seen) != len(want) {
		return errors.New("agent remote candidate set is incomplete")
	}
	return nil
}
