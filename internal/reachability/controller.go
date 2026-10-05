// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package reachability

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type State uint8

const (
	StateDisabled State = iota
	StateVerifying
	StateVerified
	StateUnverified
	StateExpired
	StateClosed
)

func (s State) String() string {
	switch s {
	case StateDisabled:
		return "DISABLED"
	case StateVerifying:
		return "VERIFYING"
	case StateVerified:
		return "VERIFIED"
	case StateUnverified:
		return "UNVERIFIED"
	case StateExpired:
		return "EXPIRED"
	case StateClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}

type Attestor interface {
	Request(context.Context, peer.ID, irviov1.ReachabilityFamily, uint16, uint64, uint64) (*irviov1.SignedReachabilityAttestation, error)
}

type VerificationRequest struct {
	Family            irviov1.ReachabilityFamily
	ExternalPort      uint16
	NetworkGeneration uint64
	PublicEpoch       uint64
	Verifiers         []peer.ID
}

type Snapshot struct {
	State      State
	Generation uint64
	Requested  bool
	Proof      Proof
	LastError  error
	UpdatedAt  time.Time
}

// Ready allows renewal only while quorum proof remains valid, never after failure or expiry.
func (s Snapshot) Ready(now time.Time) bool {
	return s.Requested && (s.State == StateVerified || s.State == StateVerifying) &&
		s.Proof.ExpiresAt.After(now) && len(s.Proof.Attestations) >= DefaultQuorum
}

type ControllerConfig struct {
	Subject        peer.ID
	Attestor       Attestor
	Quorum         int
	MaxVerifiers   int
	ReverifyLead   time.Duration
	RetryDelay     time.Duration
	Now            func() time.Time
	Publish        func(context.Context, *Proof) error
	ReverifyJitter func(time.Duration) time.Duration
	EventCapacity  int
	// RefreshVerifiers runs after failure without altering subject, endpoint, generation, or quorum.
	RefreshVerifiers func(context.Context) ([]peer.ID, error)
}

type Controller struct {
	config ControllerConfig

	mu         sync.RWMutex
	publishMu  sync.Mutex
	snapshot   Snapshot
	request    VerificationRequest
	cancelWork context.CancelFunc
	events     chan Snapshot
	closed     bool
	closeOnce  sync.Once
	done       chan struct{}
}

func NewController(config ControllerConfig) (*Controller, error) {
	if config.Subject == "" || config.Attestor == nil {
		return nil, ErrInvalidConfig
	}
	if config.Quorum == 0 {
		config.Quorum = DefaultQuorum
	}
	if config.MaxVerifiers == 0 {
		config.MaxVerifiers = DefaultVerifierLimit
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = time.Minute
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.EventCapacity == 0 {
		config.EventCapacity = 8
	}
	if config.Publish == nil {
		config.Publish = func(context.Context, *Proof) error { return nil }
	}
	if config.ReverifyJitter == nil {
		config.ReverifyJitter = defaultReverifyJitter
	}
	if config.Quorum <= 0 || config.Quorum > config.MaxVerifiers || config.MaxVerifiers > DefaultVerifierLimit ||
		config.RetryDelay <= 0 || config.EventCapacity <= 0 {
		return nil, ErrInvalidConfig
	}
	now := config.Now().UTC()
	return &Controller{
		config: config, snapshot: Snapshot{State: StateDisabled, UpdatedAt: now},
		events: make(chan Snapshot, config.EventCapacity), done: make(chan struct{}),
	}, nil
}

func (c *Controller) Enable(ctx context.Context, request VerificationRequest) error {
	if ctx == nil || !validVerificationRequest(request, c.config.Subject, c.config.Quorum, c.config.MaxVerifiers) {
		return ErrInvalidConfig
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	if c.cancelWork != nil {
		c.cancelWork()
	}
	work, cancel := context.WithCancel(context.Background())
	verifyContext, cancelVerify := context.WithCancel(ctx)
	c.cancelWork = func() {
		cancelVerify()
		cancel()
	}
	c.request = cloneVerificationRequest(request)
	c.snapshot.Generation++
	token := c.snapshot.Generation
	c.snapshot.Requested = true
	c.snapshot.State = StateVerifying
	c.snapshot.Proof = Proof{}
	c.snapshot.LastError = nil
	c.snapshot.UpdatedAt = c.config.Now().UTC()
	c.publishEventLocked()
	c.mu.Unlock()

	result := c.collect(verifyContext, request)
	cancelVerify()
	completeErr := c.complete(token, request, result)
	if errors.Is(completeErr, ErrStaleGeneration) {
		cancel()
		return completeErr
	}
	go c.maintain(work, token)
	return completeErr
}

type verificationResult struct {
	proof Proof
	err   error
}

func (c *Controller) collect(ctx context.Context, request VerificationRequest) verificationResult {
	type response struct {
		value *irviov1.SignedReachabilityAttestation
		err   error
	}
	limit := len(request.Verifiers)
	if limit > c.config.MaxVerifiers {
		limit = c.config.MaxVerifiers
	}
	results := make(chan response, limit)
	work, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
	}()
	for _, verifier := range request.Verifiers[:limit] {
		workers.Add(1)
		go func(remote peer.ID) {
			defer workers.Done()
			value, err := c.config.Attestor.Request(work, remote, request.Family, request.ExternalPort, request.NetworkGeneration, request.PublicEpoch)
			results <- response{value: value, err: err}
		}(verifier)
	}
	attestations := make([]*irviov1.SignedReachabilityAttestation, 0, limit)
	var failures []error
	for index := 0; index < limit; index++ {
		select {
		case <-ctx.Done():
			return verificationResult{err: ctx.Err()}
		case result := <-results:
			if result.err != nil {
				failures = append(failures, result.err)
				continue
			}
			attestations = append(attestations, result.value)
			if proof, err := findQuorumProof(c.config.Subject, attestations, ProofPolicy{Quorum: c.config.Quorum, Now: c.config.Now}); err == nil {
				cancel()
				return verificationResult{proof: proof}
			}
		}
	}
	proof, err := findQuorumProof(c.config.Subject, attestations, ProofPolicy{Quorum: c.config.Quorum, Now: c.config.Now})
	if err != nil {
		return verificationResult{err: errors.Join(append(failures, err)...)}
	}
	return verificationResult{proof: proof}
}

func findQuorumProof(subject peer.ID, attestations []*irviov1.SignedReachabilityAttestation, policy ProofPolicy) (Proof, error) {
	if len(attestations) < policy.Quorum {
		return Proof{}, ErrInsufficient
	}
	indexes := make([]int, policy.Quorum)
	var result Proof
	var found bool
	var search func(int, int)
	search = func(depth, start int) {
		if found {
			return
		}
		if depth == len(indexes) {
			selected := make([]*irviov1.SignedReachabilityAttestation, len(indexes))
			for index, source := range indexes {
				selected[index] = attestations[source]
			}
			proof, err := BuildProof(subject, selected, policy)
			if err == nil {
				result, found = proof, true
			}
			return
		}
		for index := start; index <= len(attestations)-(len(indexes)-depth); index++ {
			indexes[depth] = index
			search(depth+1, index+1)
		}
	}
	search(0, 0)
	if !found {
		return Proof{}, ErrInsufficient
	}
	return result, nil
}

func (c *Controller) complete(token uint64, request VerificationRequest, result verificationResult) error {
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	c.mu.Lock()
	if c.closed || token != c.snapshot.Generation || !c.snapshot.Requested || !sameVerificationRequest(c.request, request) {
		c.mu.Unlock()
		return ErrStaleGeneration
	}
	if result.err != nil {
		if !c.snapshot.Proof.ExpiresAt.IsZero() && !c.snapshot.Proof.ExpiresAt.After(c.config.Now()) {
			c.snapshot.State = StateExpired
		} else {
			c.snapshot.State = StateUnverified
		}
		c.snapshot.LastError = result.err
		c.snapshot.Proof = Proof{}
	} else {
		c.snapshot.State = StateVerified
		c.snapshot.LastError = nil
		c.snapshot.Proof = result.proof
	}
	c.snapshot.UpdatedAt = c.config.Now().UTC()
	proof := c.snapshot.Proof
	c.publishEventLocked()
	c.mu.Unlock()
	var published *Proof
	if result.err == nil {
		published = &proof
	}
	if err := c.config.Publish(context.Background(), published); err != nil {
		return err
	}
	return result.err
}

func (c *Controller) maintain(ctx context.Context, token uint64) {
	for {
		c.mu.RLock()
		if c.closed || token != c.snapshot.Generation || !c.snapshot.Requested {
			c.mu.RUnlock()
			return
		}
		state, proof, request := c.snapshot.State, c.snapshot.Proof, cloneVerificationRequest(c.request)
		c.mu.RUnlock()
		delay := c.config.RetryDelay
		if state == StateVerified {
			lead := c.config.ReverifyLead
			if lead == 0 || lead >= proof.ExpiresAt.Sub(c.config.Now()) {
				lead = proof.ExpiresAt.Sub(c.config.Now()) / 3
			}
			delay = proof.ExpiresAt.Add(-lead).Sub(c.config.Now())
			if delay < 0 {
				delay = 0
			}
			jittered := c.config.ReverifyJitter(delay)
			if jittered >= 0 && jittered <= delay {
				delay = jittered
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		c.mu.Lock()
		if c.closed || token != c.snapshot.Generation || !c.snapshot.Requested {
			c.mu.Unlock()
			return
		}
		c.snapshot.State = StateVerifying
		c.snapshot.UpdatedAt = c.config.Now().UTC()
		c.publishEventLocked()
		c.mu.Unlock()
		attempt, cancel := context.WithTimeout(ctx, DefaultAttemptTTL)
		if state != StateVerified && c.config.RefreshVerifiers != nil {
			ids, err := c.config.RefreshVerifiers(attempt)
			if err == nil {
				next := cloneVerificationRequest(request)
				next.Verifiers = ids
				if validVerificationRequest(next, c.config.Subject, c.config.Quorum, c.config.MaxVerifiers) {
					c.mu.Lock()
					if c.closed || token != c.snapshot.Generation || !c.snapshot.Requested {
						c.mu.Unlock()
						cancel()
						return
					}
					c.request = cloneVerificationRequest(next)
					request = next
					c.mu.Unlock()
				}
			}
		}
		result := c.collect(attempt, request)
		cancel()
		if err := c.complete(token, request, result); errors.Is(err, ErrStaleGeneration) {
			return
		}
	}
}

func (c *Controller) InvalidateNetwork(ctx context.Context, networkGeneration uint64) error {
	if ctx == nil || networkGeneration == 0 {
		return ErrInvalidConfig
	}
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	if !c.snapshot.Requested || c.request.NetworkGeneration == networkGeneration {
		c.mu.Unlock()
		return nil
	}
	if c.cancelWork != nil {
		c.cancelWork()
	}
	c.snapshot.Generation++
	c.snapshot.State = StateUnverified
	c.snapshot.Proof = Proof{}
	c.snapshot.LastError = ErrStaleGeneration
	c.snapshot.UpdatedAt = c.config.Now().UTC()
	c.publishEventLocked()
	c.mu.Unlock()
	return c.config.Publish(ctx, nil)
}

func (c *Controller) Disable(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	if c.cancelWork != nil {
		c.cancelWork()
	}
	c.snapshot.Generation++
	c.snapshot.State = StateDisabled
	c.snapshot.Requested = false
	c.snapshot.Proof = Proof{}
	c.snapshot.LastError = nil
	c.snapshot.UpdatedAt = c.config.Now().UTC()
	c.publishEventLocked()
	c.mu.Unlock()
	return c.config.Publish(ctx, nil)
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneSnapshot(c.snapshot)
}

func (c *Controller) Events() <-chan Snapshot { return c.events }

func (c *Controller) publishEventLocked() {
	value := cloneSnapshot(c.snapshot)
	select {
	case c.events <- value:
	default:
		select {
		case <-c.events:
		default:
		}
		select {
		case c.events <- value:
		default:
		}
	}
}

func (c *Controller) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.publishMu.Lock()
		defer c.publishMu.Unlock()
		c.mu.Lock()
		c.closed = true
		if c.cancelWork != nil {
			c.cancelWork()
		}
		c.snapshot.Generation++
		c.snapshot.State = StateClosed
		c.snapshot.Requested = false
		c.snapshot.Proof = Proof{}
		c.snapshot.UpdatedAt = c.config.Now().UTC()
		c.publishEventLocked()
		close(c.done)
		close(c.events)
		c.mu.Unlock()
	})
	return nil
}

func defaultReverifyJitter(delay time.Duration) time.Duration {
	window := delay / 10
	if window > 30*time.Second {
		window = 30 * time.Second
	}
	if window <= 0 {
		return delay
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return delay
	}
	offset := time.Duration(binary.LittleEndian.Uint64(random[:]) % uint64(window+1))
	return delay - offset
}

func validVerificationRequest(request VerificationRequest, subject peer.ID, quorum, limit int) bool {
	if request.ExternalPort == 0 || request.NetworkGeneration == 0 || request.PublicEpoch == 0 ||
		request.Family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4 &&
			request.Family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6 ||
		len(request.Verifiers) < quorum || len(request.Verifiers) > limit {
		return false
	}
	seen := make(map[peer.ID]struct{}, len(request.Verifiers))
	for _, verifier := range request.Verifiers {
		if verifier == "" || verifier == subject {
			return false
		}
		if _, exists := seen[verifier]; exists {
			return false
		}
		seen[verifier] = struct{}{}
	}
	return true
}

func cloneVerificationRequest(value VerificationRequest) VerificationRequest {
	value.Verifiers = append([]peer.ID(nil), value.Verifiers...)
	return value
}

func sameVerificationRequest(a, b VerificationRequest) bool {
	if a.Family != b.Family || a.ExternalPort != b.ExternalPort || a.NetworkGeneration != b.NetworkGeneration ||
		a.PublicEpoch != b.PublicEpoch || len(a.Verifiers) != len(b.Verifiers) {
		return false
	}
	for index := range a.Verifiers {
		if a.Verifiers[index] != b.Verifiers[index] {
			return false
		}
	}
	return true
}

func cloneSnapshot(value Snapshot) Snapshot {
	cloned := make([]*irviov1.SignedReachabilityAttestation, len(value.Proof.Attestations))
	for index, attestation := range value.Proof.Attestations {
		if attestation != nil {
			cloned[index] = proto.Clone(attestation).(*irviov1.SignedReachabilityAttestation)
		}
	}
	value.Proof.Attestations = cloned
	return value
}
