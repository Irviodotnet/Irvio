// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/protocol"
)

const (
	InitialCandidateTarget = 5
	MaxAcquisitionRPCs     = 4
	MaxRendezvousCache     = 32
	MaxCandidateRouteCache = MaxRendezvousCache * MaxCandidates
	DefaultRoundLifetime   = 20 * time.Second
)

type providerRound struct {
	country    string
	started    time.Time
	servers    []peer.ID
	rpcs       int
	candidates map[peer.ID]Candidate
	hops       map[peer.ID][]brokerHop
	partial    bool
	busy       bool
}

type providerRouteKey struct {
	peer  peer.ID
	epoch uint64
}

// CandidateProvider acquires five records, then up to ten within the same four-GET budget.
type CandidateProvider struct {
	local     peer.ID
	client    *Client
	servers   RendezvousServerProvider
	forwarder *VerifiedForwarder
	now       func() time.Time

	mu        sync.Mutex
	primary   map[string]peer.ID
	fallback  map[string][]peer.ID
	cache     []peer.ID
	rounds    map[string]*providerRound
	routes    map[providerRouteKey]Candidate
	hops      map[providerRouteKey][]brokerHop
	routeFIFO []providerRouteKey
	logger    *slog.Logger
}

func NewCandidateProvider(local peer.ID, client *Client, servers RendezvousServerProvider, forwarder *VerifiedForwarder) (*CandidateProvider, error) {
	if local == "" || local.Validate() != nil || client == nil || servers == nil || forwarder == nil {
		return nil, fmt.Errorf("%w: candidate provider dependencies", ErrInvalid)
	}
	return &CandidateProvider{
		local: local, client: client, servers: servers, forwarder: forwarder, now: time.Now,
		primary: make(map[string]peer.ID), fallback: make(map[string][]peer.ID),
		rounds: make(map[string]*providerRound), routes: make(map[providerRouteKey]Candidate),
		hops: make(map[providerRouteKey][]brokerHop),
	}, nil
}

// SetLogger adds bounded diagnostics without exposing signed descriptors or changing selection.
func (p *CandidateProvider) SetLogger(logger *slog.Logger) {
	p.mu.Lock()
	p.logger = logger
	p.mu.Unlock()
}

func (p *CandidateProvider) GetCandidates(ctx context.Context, request exitselector.CandidateRequest) (exitselector.CandidateBatch, error) {
	if ctx == nil || !validCountry(string(request.Country)) || request.Limit < 1 ||
		request.Limit > MaxCandidates || len(request.ExcludePeerIDs) > MaxExcludedPeers {
		return exitselector.CandidateBatch{}, ErrInvalid
	}
	country := string(request.Country)
	now := p.now().UTC()
	p.mu.Lock()
	round := p.rounds[country]
	if round == nil || now.Sub(round.started) >= DefaultRoundLifetime || round.rpcs >= MaxAcquisitionRPCs {
		p.mu.Unlock()
		servers, err := p.selectServers(ctx, country)
		if err != nil {
			return exitselector.CandidateBatch{}, err
		}
		if len(servers) == 0 {
			return exitselector.CandidateBatch{Country: request.Country, GeneratedAt: now}, nil
		}
		p.mu.Lock()
		round = &providerRound{
			country: country, started: now, servers: servers,
			candidates: make(map[peer.ID]Candidate), hops: make(map[peer.ID][]brokerHop),
		}
		p.rounds[country] = round
	}
	if round.busy {
		p.mu.Unlock()
		return exitselector.CandidateBatch{}, ErrBusy
	}
	round.busy = true
	firstWave := round.rpcs == 0
	target := min(request.Limit, MaxCandidates)
	if firstWave {
		target = min(target, InitialCandidateTarget)
	}
	p.mu.Unlock()

	err := p.acquire(ctx, round, target, request.ExcludePeerIDs, firstWave)

	p.mu.Lock()
	round.busy = false
	batch := p.batchLocked(round, request, target, now)
	logger := p.logger
	acquired, rpcs, partial := len(round.candidates), round.rpcs, round.partial
	p.mu.Unlock()
	if logger != nil {
		logger.Info("RENDEZVOUS_CANDIDATE_BATCH",
			"country", country, "acquired", acquired, "usable", len(batch.Candidates),
			"rpcs", rpcs, "partial", partial, "error", err,
		)
	}
	if err != nil && len(batch.Candidates) == 0 {
		return exitselector.CandidateBatch{}, err
	}
	return batch, nil
}

func (p *CandidateProvider) acquire(ctx context.Context, round *providerRound, target int, excluded []peer.ID, firstWave bool) error {
	if target < 1 {
		return nil
	}
	var failures []error
	if firstWave {
		requests := []struct {
			server peer.ID
			limit  int
		}{}
		if len(round.servers) == 1 {
			requests = append(requests, struct {
				server peer.ID
				limit  int
			}{round.servers[0], target})
		} else {
			requests = append(requests,
				struct {
					server peer.ID
					limit  int
				}{round.servers[0], min(3, target)},
				struct {
					server peer.ID
					limit  int
				}{round.servers[1], min(2, max(0, target-3))},
			)
		}
		failures = append(failures, p.queryWave(ctx, round, requests, excluded)...)
		// Return the first verified candidate promptly and reserve remaining RPCs for top-up.
		if p.roundSize(round, excluded) > 0 {
			return nil
		}
	}
	for p.roundSize(round, excluded) < target && p.roundRPCs(round) < MaxAcquisitionRPCs {
		index := p.roundRPCs(round) % len(round.servers)
		remaining := min(MaxCandidates-p.roundCandidateCount(round), target-p.roundSize(round, excluded))
		if remaining < 1 {
			break
		}
		currentExcluded := p.roundExclusions(round, excluded)
		failures = append(failures, p.queryWave(ctx, round, []struct {
			server peer.ID
			limit  int
		}{{round.servers[index], remaining}}, currentExcluded)...)
	}
	if len(failures) > 0 && p.roundCandidateCount(round) == 0 {
		return errors.Join(failures...)
	}
	return nil
}

func (p *CandidateProvider) queryWave(ctx context.Context, round *providerRound, requests []struct {
	server peer.ID
	limit  int
}, excluded []peer.ID) []error {
	type result struct {
		server     peer.ID
		candidates []Candidate
		partial    bool
		err        error
	}
	results := make(chan result, len(requests))
	var group sync.WaitGroup
	for _, request := range requests {
		if request.limit < 1 || p.roundRPCs(round) >= MaxAcquisitionRPCs {
			continue
		}
		p.mu.Lock()
		round.rpcs++
		p.mu.Unlock()
		group.Add(1)
		go func(server peer.ID, limit int) {
			defer group.Done()
			values, partial, err := p.client.GetCandidates(ctx, server, round.country, limit, truncatePeers(excluded, MaxExcludedPeers))
			results <- result{server: server, candidates: values, partial: partial, err: err}
		}(request.server, request.limit)
	}
	group.Wait()
	close(results)
	var failures []error
	for result := range results {
		if result.err != nil {
			reportServerFailure(p.servers, result.server, result.err)
			failures = append(failures, result.err)
			p.mu.Lock()
			kept := p.cache[:0]
			for _, id := range p.cache {
				if id != result.server {
					kept = append(kept, id)
				}
			}
			p.cache = kept
			round.partial = true
			p.mu.Unlock()
			continue
		}
		p.mu.Lock()
		round.partial = round.partial || result.partial
		for _, candidate := range result.candidates {
			if len(round.candidates) >= MaxCandidates {
				round.partial = true
				break
			}
			current, exists := round.candidates[candidate.Descriptor.Subject]
			if exists {
				comparison := candidate.Descriptor.Version.Compare(current.Descriptor.Version)
				merged, err := MergeCandidates(current, candidate)
				if err != nil {
					round.partial = true
					continue
				}
				round.candidates[candidate.Descriptor.Subject] = merged[0]
				if comparison > 0 {
					round.hops[candidate.Descriptor.Subject] = nil
				}
				if comparison >= 0 {
					round.hops[candidate.Descriptor.Subject] = appendBrokerHops(
						round.hops[candidate.Descriptor.Subject], result.server, candidate,
					)
				}
				continue
			}
			round.candidates[candidate.Descriptor.Subject] = cloneCandidate(candidate)
			round.hops[candidate.Descriptor.Subject] = appendBrokerHops(nil, result.server, candidate)
		}
		p.mu.Unlock()
	}
	return failures
}

func (p *CandidateProvider) selectServers(ctx context.Context, country string) ([]peer.ID, error) {
	discovered, err := p.servers.Servers(ctx, country, MaxRendezvousCache)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	eligible := make([]peer.ID, 0, len(discovered)+len(p.cache))
	seen := map[peer.ID]struct{}{p.local: {}}
	for _, id := range append(append([]peer.ID(nil), discovered...), p.cache...) {
		if id == "" || id.Validate() != nil {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		eligible = append(eligible, id)
	}
	if len(eligible) == 0 {
		return nil, err
	}
	for _, id := range discovered {
		if id != p.local {
			p.rememberServerLocked(id)
		}
	}
	primary := p.primary[country]
	if !containsPeer(eligible, primary) {
		shufflePeers(eligible)
		primary = eligible[0]
		p.primary[country] = primary
	}
	ordered := []peer.ID{primary}
	rest := make([]peer.ID, 0, len(eligible)-1)
	for _, id := range eligible {
		if id != primary {
			rest = append(rest, id)
		}
	}
	shufflePeers(rest)
	ordered = append(ordered, rest...)
	p.fallback[country] = append([]peer.ID(nil), ordered[1:min(len(ordered), 3)]...)
	return ordered[:min(len(ordered), 3)], nil
}

func (p *CandidateProvider) batchLocked(round *providerRound, request exitselector.CandidateRequest, target int, now time.Time) exitselector.CandidateBatch {
	excluded := make(map[peer.ID]struct{}, len(request.ExcludePeerIDs)+1)
	excluded[p.local] = struct{}{}
	for _, id := range request.ExcludePeerIDs {
		excluded[id] = struct{}{}
	}
	values := make([]exitselector.Candidate, 0, len(round.candidates))
	for id, candidate := range round.candidates {
		if _, skip := excluded[id]; skip {
			continue
		}
		converted, ok := p.convertCandidateLocked(round, candidate, now)
		if !ok {
			continue
		}
		values = append(values, converted)
	}
	shuffleExitCandidates(values)
	if len(values) > target {
		values = values[:target]
	}
	return exitselector.CandidateBatch{
		Country: request.Country, AvailablePeerCount: len(values),
		GeneratedAt: now, Candidates: values,
	}
}

func (p *CandidateProvider) convertCandidateLocked(round *providerRound, candidate Candidate, now time.Time) (exitselector.Candidate, bool) {
	announcement := candidate.Descriptor.Announcement
	country, state := candidate.CountryStatus()
	if announcement == nil || state != CountryObserved || !announcement.AcceptingNewSessions ||
		announcement.ProtocolVersion != protocol.ProtocolVersion || !hasSessionFeature(announcement.SessionFeatures, "session-establish-v1") ||
		!hasSessionFeature(announcement.SessionFeatures, "session-control-v1") {
		return exitselector.Candidate{}, false
	}
	expires := time.Unix(announcement.ExpiresAtUnix, 0).UTC()
	for _, receipt := range candidate.Receipts {
		if receipt.Receipt.ExpiresAt.Before(expires) {
			expires = receipt.Receipt.ExpiresAt
		}
	}
	if !expires.After(now) {
		return exitselector.Candidate{}, false
	}
	epoch := descriptorEpoch(candidate.Descriptor)
	key := providerRouteKey{peer: candidate.Descriptor.Subject, epoch: epoch}
	hops := round.hops[candidate.Descriptor.Subject]
	if len(hops) == 0 {
		return exitselector.Candidate{}, false
	}
	p.rememberCandidateRouteLocked(key, candidate, hops)
	if err := p.forwarder.Remember(candidate); err != nil {
		return exitselector.Candidate{}, false
	}
	return exitselector.Candidate{
		PeerID: candidate.Descriptor.Subject, Country: country,
		ProtocolVersion: announcement.ProtocolVersion, Capabilities: announcement.Capabilities,
		Accepting: announcement.AcceptingNewSessions, Uptime: exitselector.UptimeNew,
		NetworkEpoch: epoch, GeneratedAt: now, ExpiresAt: expires,
		Transport:    exitselector.CandidateTransportBrokered,
		DescriptorID: candidate.Descriptor.ID, DescriptorIncarnation: candidate.Descriptor.Version.Incarnation,
		DescriptorSequence: candidate.Descriptor.Version.Sequence, SignalingRouteEpoch: signalingRouteEpoch(hops),
	}, true
}

func (p *CandidateProvider) Resolve(candidate exitselector.Candidate) (Candidate, error) {
	if candidate.Transport != exitselector.CandidateTransportBrokered || candidate.PeerID == "" {
		return Candidate{}, ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	value, exists := p.routes[providerRouteKey{peer: candidate.PeerID, epoch: candidate.NetworkEpoch}]
	hops := p.hops[providerRouteKey{peer: candidate.PeerID, epoch: candidate.NetworkEpoch}]
	if !exists || value.Descriptor.ID != candidate.DescriptorID ||
		value.Descriptor.Version.Incarnation != candidate.DescriptorIncarnation ||
		value.Descriptor.Version.Sequence != candidate.DescriptorSequence ||
		candidate.SignalingRouteEpoch == 0 || signalingRouteEpoch(hops) != candidate.SignalingRouteEpoch {
		return Candidate{}, ErrStale
	}
	return cloneCandidate(value), nil
}

func (p *CandidateProvider) BrokerCarrier(candidate exitselector.Candidate) (*BrokerCarrier, error) {
	resolved, err := p.Resolve(candidate)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	hops := cloneBrokerHops(p.hops[providerRouteKey{peer: candidate.PeerID, epoch: candidate.NetworkEpoch}])
	logger := p.logger
	p.mu.Unlock()
	if len(hops) == 0 {
		return nil, ErrRouteFailed
	}
	carrier, err := newBrokerCarrier(p.client, resolved, hops)
	if err != nil {
		return nil, err
	}
	carrier.logger = logger
	return carrier, nil
}

func appendBrokerHops(current []brokerHop, ingress peer.ID, candidate Candidate) []brokerHop {
	for _, owner := range candidate.Routes {
		receipt, exists := candidateReceipt(candidate, owner)
		if !exists {
			continue
		}
		duplicate := false
		for _, hop := range current {
			if hop.ingress == ingress && hop.owner.ServerPeer == owner.ServerPeer &&
				hop.owner.ServerIncarnation == owner.ServerIncarnation && hop.owner.RegistrationID == owner.RegistrationID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			hop := brokerHop{ingress: ingress, owner: cloneOwnerRoute(owner), receipt: cloneSignedReceipt(receipt)}
			if len(current) < MaxOwnerRoutes {
				current = append(current, hop)
				continue
			}
			if !containsIngress(current, ingress) {
				for index := len(current) - 1; index >= 0; index-- {
					if ingressCount(current, current[index].ingress) > 1 {
						current[index] = hop
						break
					}
				}
			}
		}
	}
	return current
}

func cloneBrokerHops(values []brokerHop) []brokerHop {
	result := make([]brokerHop, 0, min(len(values), MaxOwnerRoutes))
	for _, value := range values[:min(len(values), MaxOwnerRoutes)] {
		result = append(result, cloneBrokerHop(value))
	}
	return result
}

func containsIngress(values []brokerHop, ingress peer.ID) bool {
	return ingressCount(values, ingress) != 0
}

func ingressCount(values []brokerHop, ingress peer.ID) int {
	count := 0
	for _, value := range values {
		if value.ingress == ingress {
			count++
		}
	}
	return count
}

func (p *CandidateProvider) rememberCandidateRouteLocked(key providerRouteKey, candidate Candidate, hops []brokerHop) {
	if _, exists := p.routes[key]; !exists {
		if len(p.routeFIFO) == MaxCandidateRouteCache {
			oldest := p.routeFIFO[0]
			copy(p.routeFIFO, p.routeFIFO[1:])
			p.routeFIFO = p.routeFIFO[:len(p.routeFIFO)-1]
			delete(p.routes, oldest)
			delete(p.hops, oldest)
		}
		p.routeFIFO = append(p.routeFIFO, key)
	}
	p.routes[key] = cloneCandidate(candidate)
	p.hops[key] = cloneBrokerHops(hops)
}

// MetadataProbe validates freshness without inventing NAT Exit RTT; READY still gates connection.
type MetadataProbe struct {
	Provider *CandidateProvider
	Now      func() time.Time
	Logger   *slog.Logger
}

func (p MetadataProbe) Probe(_ context.Context, candidate exitselector.Candidate) (exitselector.ProbeResult, error) {
	if p.Provider == nil {
		return exitselector.ProbeResult{}, ErrInvalid
	}
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	if _, err := p.Provider.Resolve(candidate); err != nil || !candidate.ExpiresAt.After(now) ||
		now.Sub(candidate.GeneratedAt) > 15*time.Second {
		if p.Logger != nil {
			p.Logger.Warn("RENDEZVOUS_METADATA_PROBE_FAILED", "peer_id", candidate.PeerID, "reason", errors.Join(err, ErrStale))
		}
		if err != nil {
			return exitselector.ProbeResult{}, err
		}
		return exitselector.ProbeResult{}, ErrStale
	}
	if p.Logger != nil {
		p.Logger.Info("RENDEZVOUS_METADATA_PROBE_SUCCEEDED", "peer_id", candidate.PeerID, "network_epoch", candidate.NetworkEpoch)
	}
	return exitselector.ProbeResult{
		Reachable: true, ObservedAt: now, NetworkEpoch: candidate.NetworkEpoch,
		Kind: exitselector.ProbeBrokerMetadata,
	}, nil
}

func descriptorEpoch(descriptor Descriptor) uint64 {
	if descriptor.Announcement == nil {
		return 0
	}
	return protocol.NetworkPathEpoch(descriptor.Announcement.SessionEpoch, descriptor.Announcement.Libp2PAddrs)
}

func signalingRouteEpoch(hops []brokerHop) uint64 {
	keys := make([]string, 0, len(hops))
	for _, hop := range hops {
		addresses := append([]string(nil), hop.owner.ControlAddresses...)
		sort.Strings(addresses)
		keys = append(keys, hop.ingress.String()+"/"+
			ownerKey(hop.owner.ServerPeer, hop.owner.ServerIncarnation, hop.owner.RegistrationID)+"/"+
			fmt.Sprint(hop.receipt.Receipt.Sequence)+"/"+fmt.Sprint(addresses))
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		hash.Write([]byte(key))
		hash.Write([]byte{0})
	}
	sum := hash.Sum(nil)
	epoch := binary.BigEndian.Uint64(sum[:8])
	if epoch == 0 {
		return 1
	}
	return epoch
}

func hasSessionFeature(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (p *CandidateProvider) roundRPCs(round *providerRound) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return round.rpcs
}

func (p *CandidateProvider) roundCandidateCount(round *providerRound) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(round.candidates)
}

func (p *CandidateProvider) roundSize(round *providerRound, excluded []peer.ID) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	blocked := make(map[peer.ID]struct{}, len(excluded))
	for _, id := range excluded {
		blocked[id] = struct{}{}
	}
	count := 0
	for id := range round.candidates {
		if _, skip := blocked[id]; !skip {
			count++
		}
	}
	return count
}

func (p *CandidateProvider) roundExclusions(round *providerRound, excluded []peer.ID) []peer.ID {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := append([]peer.ID(nil), excluded...)
	for id := range round.candidates {
		result = append(result, id)
	}
	return truncatePeers(result, MaxExcludedPeers)
}

func (p *CandidateProvider) rememberServerLocked(id peer.ID) {
	if containsPeer(p.cache, id) {
		return
	}
	if len(p.cache) >= MaxRendezvousCache {
		copy(p.cache, p.cache[1:])
		p.cache[len(p.cache)-1] = id
		return
	}
	p.cache = append(p.cache, id)
}

func truncatePeers(values []peer.ID, limit int) []peer.ID {
	result := make([]peer.ID, 0, min(limit, len(values)))
	seen := make(map[peer.ID]struct{}, len(values))
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
			break
		}
	}
	return result
}

func containsPeer(values []peer.ID, expected peer.ID) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func shufflePeers(values []peer.ID) {
	for index := len(values) - 1; index > 0; index-- {
		value, err := rand.Int(rand.Reader, big.NewInt(int64(index+1)))
		if err != nil {
			return
		}
		other := int(value.Int64())
		values[index], values[other] = values[other], values[index]
	}
}

func shuffleExitCandidates(values []exitselector.Candidate) {
	for index := len(values) - 1; index > 0; index-- {
		value, err := rand.Int(rand.Reader, big.NewInt(int64(index+1)))
		if err != nil {
			return
		}
		other := int(value.Int64())
		values[index], values[other] = values[other], values[index]
	}
}

var _ exitselector.CandidateProvider = (*CandidateProvider)(nil)
var _ exitselector.CandidateProbe = MetadataProbe{}
