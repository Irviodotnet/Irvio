// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

var ErrPrivateLookupIncomplete = errors.New("private candidate lookup incomplete")

// PrivateOwnerFinder returns only authenticated, currently eligible private Rendezvous owners.
type PrivateOwnerFinder interface {
	FindPrivateOwners(context.Context, [32]byte) ([]peer.ID, error)
}

type PrivateLookupClient interface {
	LookupPrivate(context.Context, peer.ID, peer.ID, [32]byte) (Candidate, error)
}

type PrivateLookupResult struct {
	Candidate Candidate
	Partial   bool
}

// PrivateLookup pins one identity and revalidates discovery on each bounded round without public fallback.
type PrivateLookup struct {
	owners  PrivateOwnerFinder
	client  PrivateLookupClient
	target  peer.ID
	locator [32]byte
	gate    chan struct{}
}

func NewPrivateLookup(owners PrivateOwnerFinder, client PrivateLookupClient, target peer.ID, locator [32]byte) (*PrivateLookup, error) {
	_, peerErr := peer.IDFromBytes([]byte(target))
	if owners == nil || client == nil || peerErr != nil || locator == ([32]byte{}) {
		return nil, ErrInvalid
	}
	return &PrivateLookup{owners: owners, client: client, target: target, locator: locator, gate: make(chan struct{}, 1)}, nil
}

func (p *PrivateLookup) Lookup(ctx context.Context) (PrivateLookupResult, error) {
	if ctx == nil {
		return PrivateLookupResult{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return PrivateLookupResult{}, ctx.Err()
	}
	discoveryCtx, stopDiscovery := context.WithTimeout(ctx, 6*time.Second)
	owners, discoveryErr := p.owners.FindPrivateOwners(discoveryCtx, p.locator)
	stopDiscovery()
	jobs := make(chan peer.ID, 8)
	seen := make(map[peer.ID]struct{})
	for _, owner := range owners {
		if len(seen) == 8 {
			break
		}
		if owner.Validate() != nil {
			continue
		}
		if _, duplicate := seen[owner]; duplicate {
			continue
		}
		seen[owner] = struct{}{}
		jobs <- owner
	}
	close(jobs)
	type response struct {
		candidate Candidate
		err       error
	}
	responses := make(chan response, 8)
	var workers sync.WaitGroup
	for i := 0; i < 3; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for owner := range jobs {
				if ctx.Err() != nil {
					return
				}
				callCtx, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
				candidate, err := p.client.LookupPrivate(callCtx, owner, p.target, p.locator)
				stop()
				if err == nil {
					wire, encodeErr := PrivateCandidateToProto(candidate)
					if encodeErr != nil {
						err = encodeErr
					} else {
						candidate, err = PrivateCandidateFromProto(wire, p.target, p.locator, time.Now())
					}
					if err == nil && (len(candidate.Routes) != 1 || len(candidate.Receipts) != 1 || candidate.Routes[0].ServerPeer != owner) {
						err = ErrWrongPeer
					}
				}
				responses <- response{candidate, err}
			}
		}()
	}
	workers.Wait()
	close(responses)
	partial := discoveryErr != nil || ctx.Err() != nil
	var candidates []Candidate
	for response := range responses {
		if response.err != nil {
			partial = true
			continue
		}
		candidates = append(candidates, response.candidate)
	}
	if len(candidates) == 0 {
		return PrivateLookupResult{Partial: true}, errors.Join(ErrPrivateLookupIncomplete, discoveryErr, ctx.Err())
	}
	merged, err := MergeCandidates(candidates...)
	if err != nil {
		return PrivateLookupResult{Partial: true}, err
	}
	if len(merged) != 1 || merged[0].Descriptor.Subject != p.target {
		return PrivateLookupResult{}, ErrWrongPeer
	}
	if ctx.Err() != nil {
		return PrivateLookupResult{Partial: true}, errors.Join(ErrPrivateLookupIncomplete, ctx.Err())
	}
	return PrivateLookupResult{Candidate: merged[0], Partial: partial}, nil
}
