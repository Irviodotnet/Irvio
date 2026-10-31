// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"sync"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/libp2p/go-libp2p/core/peer"
)

// PrivateCandidateProvider retains only the latest exact-target lookup for at most thirty seconds.
type PrivateCandidateProvider struct {
	lookup   *PrivateLookup
	client   *Client
	now      func() time.Time
	gate     chan struct{}
	mu       sync.Mutex
	cached   Candidate
	selected exitselector.Candidate
}

func NewPrivateCandidateProvider(local peer.ID, lookup *PrivateLookup, client *Client) (*PrivateCandidateProvider, error) {
	if local.Validate() != nil || lookup == nil || client == nil || lookup.target == local {
		return nil, ErrInvalid
	}
	return &PrivateCandidateProvider{lookup: lookup, client: client, now: time.Now, gate: make(chan struct{}, 1)}, nil
}

func (p *PrivateCandidateProvider) GetCandidates(ctx context.Context, request exitselector.CandidateRequest) (exitselector.CandidateBatch, error) {
	target := exitselector.Target{Kind: exitselector.TargetPrivatePeer, PeerID: p.lookup.target}
	if ctx == nil || request.Target != target || request.Country != "" || request.Limit < 1 || request.Limit > MaxCandidates || len(request.ExcludePeerIDs) > MaxExcludedPeers {
		return exitselector.CandidateBatch{}, ErrInvalid
	}
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return exitselector.CandidateBatch{}, ctx.Err()
	}
	p.mu.Lock()
	p.cached, p.selected = Candidate{}, exitselector.Candidate{}
	p.mu.Unlock()
	batch := exitselector.CandidateBatch{Target: target, GeneratedAt: p.now()}
	for _, excluded := range request.ExcludePeerIDs {
		if excluded == target.PeerID {
			return batch, nil
		}
	}
	result, err := p.lookup.Lookup(ctx)
	if err != nil {
		return batch, err
	}
	now := p.now()
	wire, err := PrivateCandidateToProto(result.Candidate)
	if err != nil {
		return batch, err
	}
	candidate, err := PrivateCandidateFromProto(wire, target.PeerID, p.lookup.locator, now)
	if err != nil {
		return batch, err
	}
	announcement := candidate.Descriptor.Announcement
	if !hasSessionFeature(announcement.SessionFeatures, "session-establish-v1") || !hasSessionFeature(announcement.SessionFeatures, "session-control-v1") {
		return batch, ErrInvalid
	}
	expires := now.Add(30 * time.Second)
	if expiry := time.Unix(announcement.ExpiresAtUnix, 0); expiry.Before(expires) {
		expires = expiry
	}
	var hops []brokerHop
	for _, owner := range candidate.Routes {
		receipt, ok := candidateReceipt(candidate, owner)
		if !ok {
			return batch, ErrUnauthorized
		}
		if receipt.Receipt.ExpiresAt.Before(expires) {
			expires = receipt.Receipt.ExpiresAt
		}
		hops = append(hops, brokerHop{ingress: owner.ServerPeer, owner: owner, receipt: receipt})
	}
	if !expires.After(now) {
		return batch, ErrStale
	}
	selected := exitselector.Candidate{
		PeerID: target.PeerID, ProtocolVersion: announcement.ProtocolVersion, Capabilities: announcement.Capabilities,
		Accepting: announcement.AcceptingNewSessions, Uptime: exitselector.UptimeNew,
		NetworkEpoch: descriptorEpoch(candidate.Descriptor), GeneratedAt: now, ExpiresAt: expires,
		Transport: exitselector.CandidateTransportBrokered, DescriptorID: candidate.Descriptor.ID,
		DescriptorIncarnation: candidate.Descriptor.Version.Incarnation, DescriptorSequence: candidate.Descriptor.Version.Sequence,
		SignalingRouteEpoch: signalingRouteEpoch(hops),
	}
	if err := ctx.Err(); err != nil {
		return batch, err
	}
	p.mu.Lock()
	p.cached, p.selected = cloneCandidate(candidate), selected
	p.mu.Unlock()
	batch.GeneratedAt, batch.AvailablePeerCount, batch.Candidates = now, 1, []exitselector.Candidate{selected}
	return batch, nil
}

func (p *PrivateCandidateProvider) Resolve(candidate exitselector.Candidate) (Candidate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if candidate != p.selected || candidate.PeerID != p.lookup.target || !p.selected.ExpiresAt.After(p.now()) {
		return Candidate{}, ErrStale
	}
	return cloneCandidate(p.cached), nil
}

func (p *PrivateCandidateProvider) BrokerCarrier(candidate exitselector.Candidate) (*BrokerCarrier, error) {
	resolved, err := p.Resolve(candidate)
	if err != nil {
		return nil, err
	}
	return NewPrivateBrokerCarrier(p.client, p.lookup.target, p.lookup.locator, resolved)
}

var _ exitselector.CandidateProvider = (*PrivateCandidateProvider)(nil)
