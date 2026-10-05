// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnellease

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// LeaseState is the resource-aware state of one Exit-local lease.
type LeaseState uint8

const (
	LeaseAllocated LeaseState = iota + 1
	LeaseActive
	LeaseRenewing
	LeaseRevoking
	LeaseQuarantined
	LeaseRevoked
)

// RevocationReason is a typed trigger for the single cleanup path.
type RevocationReason uint8

const (
	RevokeExplicitRelease RevocationReason = iota + 1
	RevokeExpired
	RevokeSessionLost
	RevokeSuperseded
	RevokeShutdown
	RevokeAdministrative
)

// ResourceRevoker must idempotently remove all lease resources for quarantine retries.
type ResourceRevoker interface {
	RevokeLease(context.Context, Lease, RevocationReason) error
}

// ResourceRevokerFunc adapts a function to ResourceRevoker.
type ResourceRevokerFunc func(context.Context, Lease, RevocationReason) error

func (f ResourceRevokerFunc) RevokeLease(ctx context.Context, lease Lease, reason RevocationReason) error {
	return f(ctx, lease, reason)
}

// ResourceActivator installs all Exit resources before returning the lease.
type ResourceActivator interface {
	ActivateLease(context.Context, Lease) error
}

type ResourceRenewer interface {
	RenewLease(context.Context, Lease, Lease) error
}

type ResourceRenewerFunc func(context.Context, Lease, Lease) error

func (f ResourceRenewerFunc) RenewLease(ctx context.Context, previous, renewed Lease) error {
	return f(ctx, previous, renewed)
}

type ResourceActivatorFunc func(context.Context, Lease) error

func (f ResourceActivatorFunc) ActivateLease(ctx context.Context, lease Lease) error {
	return f(ctx, lease)
}

// LifecycleConfig bounds one resource-aware Exit lease owner.
type LifecycleConfig struct {
	Allocator           *Allocator
	Activator           ResourceActivator
	Renewer             ResourceRenewer
	Revoker             ResourceRevoker
	Now                 func() time.Time
	SweepInterval       time.Duration
	RetryDelay          time.Duration
	RevocationTimeout   time.Duration
	MaxPending          int
	RequireSessionToken bool
}

// DefaultLifecycleConfig supplies conservative production defaults.
func DefaultLifecycleConfig(allocator *Allocator, revoker ResourceRevoker) LifecycleConfig {
	return LifecycleConfig{
		Allocator: allocator, Revoker: revoker, Now: time.Now,
		SweepInterval: time.Second, RetryDelay: 5 * time.Second,
		RevocationTimeout: 5 * time.Second, MaxPending: DefaultMaxActive,
	}
}

type lifecycleEntry struct {
	lease              Lease
	state              LeaseState
	activationDeadline time.Time
	retryAt            time.Time
	lastReason         RevocationReason
	lastError          error
}

// LeaseStatus is a redacted immutable lifecycle snapshot.
type LeaseStatus struct {
	Lease              Lease
	State              LeaseState
	ActivationDeadline time.Time
	RetryAt            time.Time
	LastReason         RevocationReason
	HasError           bool
}

// LifecycleStats contains race-safe bounded counters.
type LifecycleStats struct {
	Allocated          uint64
	Renewed            uint64
	Superseded         uint64
	RevocationsStarted uint64
	Revoked            uint64
	Quarantined        uint64
	RevocationRetries  uint64
	StaleOperations    uint64
}

// Lifecycle serializes lease mutations and uses one expiry worker for the allocator.
type Lifecycle struct {
	config LifecycleConfig
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu          sync.Mutex
	entries     map[ID]*lifecycleEntry
	stats       LifecycleStats
	closing     bool
	closed      bool
	supersedeWG sync.WaitGroup

	closeOnce sync.Once
	closeErr  error
}

// NewLifecycle enables managed expiry and starts the bounded expiry worker.
func NewLifecycle(config LifecycleConfig) (*Lifecycle, error) {
	if config.Allocator == nil || config.Revoker == nil {
		return nil, ErrInvalidConfig
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.SweepInterval <= 0 || config.SweepInterval > time.Hour ||
		config.RetryDelay <= 0 || config.RetryDelay > time.Hour ||
		config.RevocationTimeout <= 0 || config.RevocationTimeout > time.Minute ||
		config.MaxPending < 1 || config.MaxPending > 4096 {
		return nil, ErrInvalidConfig
	}
	config.Allocator.mu.Lock()
	if config.Allocator.closed {
		config.Allocator.mu.Unlock()
		return nil, ErrClosed
	}
	config.Allocator.config.ManagedExpiry = true
	active := make([]Lease, 0, len(config.Allocator.active))
	for _, lease := range config.Allocator.active {
		active = append(active, lease)
	}
	config.Allocator.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	lifecycle := &Lifecycle{
		config: config, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		entries: make(map[ID]*lifecycleEntry, config.MaxPending),
	}
	for _, lease := range active {
		lifecycle.entries[lease.ID] = &lifecycleEntry{lease: lease, state: LeaseActive}
	}
	go lifecycle.run()
	return lifecycle, nil
}

// RequireSessionTokens enables production authentication only before any allocation.
func (l *Lifecycle) RequireSessionTokens() error {
	if l == nil {
		return ErrClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.closing {
		return ErrClosed
	}
	if len(l.entries) != 0 {
		return ErrInvalidConfig
	}
	l.config.RequireSessionToken = true
	return nil
}

// Allocate reserves one address set and registers it with resource-aware expiry.
func (l *Lifecycle) Allocate(client peer.ID) (Lease, error) {
	return l.allocate(client, SessionToken{}, time.Time{})
}

// AllocateForSession is idempotent for one token and safely supersedes newer sessions of the same peer.
func (l *Lifecycle) AllocateForSession(client peer.ID, token SessionToken) (Lease, error) {
	return l.allocate(client, token, time.Time{})
}

// AllocateProvisionalForSession reserves addresses; activation must finish before the revocation deadline.
func (l *Lifecycle) AllocateProvisionalForSession(client peer.ID, token SessionToken, activationDeadline time.Time) (Lease, error) {
	if activationDeadline.IsZero() {
		return Lease{}, ErrInvalidConfig
	}
	return l.allocate(client, token, activationDeadline.UTC())
}

func (l *Lifecycle) allocate(client peer.ID, token SessionToken, activationDeadline time.Time) (Lease, error) {
	if l == nil {
		return Lease{}, ErrClosed
	}
	l.mu.Lock()
	if l.closed || l.closing {
		l.mu.Unlock()
		return Lease{}, ErrClosed
	}
	if l.config.RequireSessionToken && token.IsZero() {
		l.mu.Unlock()
		return Lease{}, ErrInvalidConfig
	}
	now := l.config.Now().UTC()
	provisional := !activationDeadline.IsZero()
	if provisional && (!activationDeadline.After(now) || activationDeadline.After(now.Add(time.Minute))) {
		l.mu.Unlock()
		return Lease{}, ErrInvalidConfig
	}
	if len(l.entries) >= l.config.MaxPending {
		if existing := l.entryForClientLocked(client); existing != nil {
			if existing.state == LeaseActive {
				if token.IsZero() || existing.lease.Session == token {
					lease := existing.lease
					l.mu.Unlock()
					return lease, nil
				}
				l.supersedeWG.Add(1)
				l.mu.Unlock()
				return l.supersede(client, token, existing, !provisional, activationDeadline)
			}
			l.mu.Unlock()
			return Lease{}, ErrRevocationPending
		}
		l.mu.Unlock()
		return Lease{}, ErrExhausted
	}
	if existing := l.entryForClientLocked(client); existing != nil {
		if existing.state != LeaseActive {
			l.mu.Unlock()
			return Lease{}, ErrRevocationPending
		}
		if token.IsZero() || existing.lease.Session == token {
			lease := existing.lease
			l.mu.Unlock()
			return lease, nil
		}
		l.supersedeWG.Add(1)
		l.mu.Unlock()
		return l.supersede(client, token, existing, !provisional, activationDeadline)
	}
	lease, err := l.config.Allocator.Allocate(client)
	if err != nil {
		l.mu.Unlock()
		return Lease{}, err
	}
	entry, exists := l.entries[lease.ID]
	if exists {
		if entry.state != LeaseActive {
			l.mu.Unlock()
			return Lease{}, ErrRevocationPending
		}
		if token.IsZero() || entry.lease.Session == token {
			lease := entry.lease
			l.mu.Unlock()
			return lease, nil
		}
		l.supersedeWG.Add(1)
		l.mu.Unlock()
		return l.supersede(client, token, entry, !provisional, activationDeadline)
	}
	lease.Session = token
	entry = &lifecycleEntry{lease: lease, state: LeaseAllocated, activationDeadline: activationDeadline}
	l.entries[lease.ID] = entry
	l.stats.Allocated++
	if provisional {
		l.mu.Unlock()
		return lease, nil
	}
	// Fence before releasing the lifecycle lock so expiry cannot revoke resources during activation.
	entry.state = LeaseRenewing
	l.supersedeWG.Add(1)
	l.mu.Unlock()
	defer l.supersedeWG.Done()
	if err := l.activate(entry, lease); err != nil {
		return Lease{}, err
	}
	return lease, nil
}

func (l *Lifecycle) activate(expected *lifecycleEntry, lease Lease) error {
	var err error
	if l.config.Activator != nil {
		ctx, cancel := context.WithTimeout(l.ctx, l.config.RevocationTimeout)
		err = l.config.Activator.ActivateLease(ctx, lease)
		cancel()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	current, exists := l.entries[lease.ID]
	if !exists || current != expected || current.lease.Generation != lease.Generation {
		l.stats.StaleOperations++
		return ErrStaleGeneration
	}
	if err != nil {
		current.state = LeaseQuarantined
		current.retryAt = l.config.Now().UTC().Add(l.config.RetryDelay)
		current.lastError = err
		current.lastReason = RevokeAdministrative
		l.stats.Quarantined++
		return fmt.Errorf("activate tunnel lease resources: %w", err)
	}
	current.state = LeaseActive
	return nil
}

// supersede revokes old resources before advancing the same addresses to a newer generation.
func (l *Lifecycle) supersede(client peer.ID, token SessionToken, expected *lifecycleEntry, activate bool, activationDeadline time.Time) (Lease, error) {
	defer l.supersedeWG.Done()
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return Lease{}, ErrClosed
	}
	current, exists := l.entries[expected.lease.ID]
	if !exists || current != expected || current.lease.ClientPeer != client {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return Lease{}, ErrStaleGeneration
	}
	if current.state != LeaseActive {
		l.mu.Unlock()
		return Lease{}, ErrRevocationPending
	}
	current.state = LeaseRevoking
	current.lastReason = RevokeSuperseded
	current.lastError = nil
	lease := current.lease
	l.stats.RevocationsStarted++
	l.mu.Unlock()

	ctx, cancel := context.WithTimeout(l.ctx, l.config.RevocationTimeout)
	err := l.config.Revoker.RevokeLease(ctx, lease, RevokeSuperseded)
	cancel()

	l.mu.Lock()
	current, exists = l.entries[lease.ID]
	if !exists || current != expected || current.lease.Generation != lease.Generation {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return Lease{}, ErrStaleGeneration
	}
	if err != nil {
		current.state = LeaseQuarantined
		current.retryAt = l.config.Now().UTC().Add(l.config.RetryDelay)
		current.lastError = err
		l.stats.Quarantined++
		l.mu.Unlock()
		return Lease{}, fmt.Errorf("revoke superseded tunnel lease resources: %w", err)
	}
	renewed, err := l.config.Allocator.Renew(client, lease.ID, lease.Generation)
	if err != nil {
		current.state = LeaseQuarantined
		current.retryAt = l.config.Now().UTC().Add(l.config.RetryDelay)
		current.lastError = err
		l.stats.Quarantined++
		l.mu.Unlock()
		return Lease{}, fmt.Errorf("advance superseding tunnel lease ownership: %w", err)
	}
	renewed.Session = token
	current.lease = renewed
	current.state = LeaseAllocated
	if activate {
		current.state = LeaseRenewing
	}
	current.activationDeadline = activationDeadline
	current.retryAt = time.Time{}
	current.lastError = nil
	l.stats.Renewed++
	l.stats.Superseded++
	l.mu.Unlock()
	if !activate {
		return renewed, nil
	}
	if err := l.activate(current, renewed); err != nil {
		return Lease{}, err
	}
	return renewed, nil
}

// ActivateSession promotes ready provisional paths idempotently while fencing stale owners.
func (l *Lifecycle) ActivateSession(client peer.ID, id ID, generation uint64, token SessionToken) (Lease, error) {
	if l == nil {
		return Lease{}, ErrClosed
	}
	l.mu.Lock()
	if l.closed || l.closing {
		l.mu.Unlock()
		return Lease{}, ErrClosed
	}
	entry := l.entries[id]
	if entry == nil {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return Lease{}, ErrUnknownLease
	}
	if entry.lease.ClientPeer != client {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return Lease{}, ErrWrongPeer
	}
	if generation == 0 || entry.lease.Generation != generation ||
		l.config.RequireSessionToken && (token.IsZero() || entry.lease.Session != token) {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return Lease{}, ErrStaleGeneration
	}
	if entry.state == LeaseActive {
		lease := entry.lease
		l.mu.Unlock()
		return lease, nil
	}
	if entry.state != LeaseAllocated {
		l.mu.Unlock()
		return Lease{}, ErrRevocationPending
	}
	now := l.config.Now().UTC()
	if !entry.lease.ValidUntil.After(now) || !entry.activationDeadline.After(now) {
		l.mu.Unlock()
		return Lease{}, ErrLeaseExpired
	}
	lease := entry.lease
	entry.state = LeaseRenewing
	l.supersedeWG.Add(1)
	l.mu.Unlock()
	defer l.supersedeWG.Done()
	if err := l.activate(entry, lease); err != nil {
		return Lease{}, err
	}
	return lease, nil
}

// Renew atomically advances a live lease generation before its expiry.
func (l *Lifecycle) Renew(client peer.ID, id ID, generation uint64) (Lease, error) {
	return l.RenewSession(client, id, generation, SessionToken{})
}

// RenewSession rejects a valid lease ID from any stale signaling session.
func (l *Lifecycle) RenewSession(client peer.ID, id ID, generation uint64, token SessionToken) (Lease, error) {
	if l == nil {
		return Lease{}, ErrClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Lease{}, ErrClosed
	}
	entry, exists := l.entries[id]
	if !exists {
		l.stats.StaleOperations++
		return Lease{}, ErrUnknownLease
	}
	if entry.lease.ClientPeer != client {
		l.stats.StaleOperations++
		return Lease{}, ErrWrongPeer
	}
	if l.config.RequireSessionToken && (token.IsZero() || entry.lease.Session != token) {
		l.stats.StaleOperations++
		return Lease{}, ErrStaleGeneration
	}
	if entry.state != LeaseActive {
		l.stats.StaleOperations++
		return Lease{}, ErrRevocationPending
	}
	entry.state = LeaseRenewing
	previous := entry.lease
	renewed, err := l.config.Allocator.Renew(client, id, generation)
	if err != nil {
		entry.state = LeaseActive
		l.stats.StaleOperations++
		return Lease{}, err
	}
	renewed.Session = token
	if l.config.Renewer != nil {
		renewContext, cancel := context.WithTimeout(l.ctx, l.config.RevocationTimeout)
		renewErr := l.config.Renewer.RenewLease(renewContext, previous, renewed)
		cancel()
		if renewErr != nil {
			entry.lease = renewed
			entry.state = LeaseQuarantined
			entry.retryAt = l.config.Now().UTC().Add(l.config.RetryDelay)
			entry.lastReason = RevokeAdministrative
			entry.lastError = renewErr
			l.stats.Quarantined++
			return Lease{}, fmt.Errorf("renew tunnel lease resource ownership: %w", renewErr)
		}
	}
	entry.lease = renewed
	entry.state = LeaseActive
	entry.retryAt = time.Time{}
	entry.lastError = nil
	l.stats.Renewed++
	return renewed, nil
}

// MutateAndRenewSession fences expiry and retains addresses; success advances revision/TTL, failure restores normal cleanup.
func (l *Lifecycle) MutateAndRenewSession(
	ctx context.Context,
	client peer.ID,
	id ID,
	generation uint64,
	token SessionToken,
	mutate func(context.Context, Lease) error,
) (Lease, error) {
	if l == nil || ctx == nil || mutate == nil {
		return Lease{}, ErrInvalidConfig
	}
	l.mu.Lock()
	if l.closed || l.closing {
		l.mu.Unlock()
		return Lease{}, ErrClosed
	}
	entry := l.entries[id]
	if entry == nil {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return Lease{}, ErrUnknownLease
	}
	if entry.lease.ClientPeer != client {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return Lease{}, ErrWrongPeer
	}
	if generation == 0 || entry.lease.Generation != generation ||
		l.config.RequireSessionToken && (token.IsZero() || entry.lease.Session != token) {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return Lease{}, ErrStaleGeneration
	}
	if entry.state != LeaseActive || !entry.lease.ValidUntil.After(l.config.Now().UTC()) {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return Lease{}, ErrLeaseExpired
	}
	previous := entry.lease
	entry.state = LeaseRenewing
	l.supersedeWG.Add(1)
	l.mu.Unlock()
	defer l.supersedeWG.Done()

	mutationContext, cancel := context.WithTimeout(ctx, l.config.RevocationTimeout)
	err := mutate(mutationContext, previous)
	cancel()

	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.entries[id]
	if current != entry || current == nil || current.lease.Generation != generation || current.state != LeaseRenewing {
		l.stats.StaleOperations++
		return Lease{}, ErrStaleGeneration
	}
	if err != nil {
		entry.state = LeaseActive
		return Lease{}, err
	}
	renewed, err := l.config.Allocator.renewManagedMutation(client, id, generation)
	if err != nil {
		entry.state = LeaseQuarantined
		entry.retryAt = l.config.Now().UTC().Add(l.config.RetryDelay)
		entry.lastReason = RevokeAdministrative
		entry.lastError = err
		l.stats.Quarantined++
		return Lease{}, err
	}
	renewed.Session = token
	if l.config.Renewer != nil {
		renewContext, renewCancel := context.WithTimeout(l.ctx, l.config.RevocationTimeout)
		renewErr := l.config.Renewer.RenewLease(renewContext, previous, renewed)
		renewCancel()
		if renewErr != nil {
			entry.lease = renewed
			entry.state = LeaseQuarantined
			entry.retryAt = l.config.Now().UTC().Add(l.config.RetryDelay)
			entry.lastReason = RevokeAdministrative
			entry.lastError = renewErr
			l.stats.Quarantined++
			return Lease{}, fmt.Errorf("renew mutated tunnel lease resource ownership: %w", renewErr)
		}
	}
	entry.lease = renewed
	entry.state = LeaseActive
	entry.retryAt = time.Time{}
	entry.lastError = nil
	l.stats.Renewed++
	return renewed, nil
}

// Release sends explicit release through the same revocation path as expiry.
func (l *Lifecycle) Release(ctx context.Context, client peer.ID, id ID, generation uint64) error {
	return l.ReleaseSession(ctx, client, id, generation, SessionToken{})
}

func (l *Lifecycle) ReleaseSession(ctx context.Context, client peer.ID, id ID, generation uint64, token SessionToken) error {
	if l != nil && l.config.RequireSessionToken {
		l.mu.Lock()
		entry := l.entries[id]
		valid := entry != nil && !token.IsZero() && entry.lease.Session == token
		l.mu.Unlock()
		if !valid {
			return ErrStaleGeneration
		}
	}
	return l.revoke(ctx, client, id, generation, RevokeExplicitRelease, false)
}

// Revoke unifies session loss, supersession, and shutdown cleanup.
func (l *Lifecycle) Revoke(ctx context.Context, client peer.ID, id ID, generation uint64, reason RevocationReason) error {
	if reason < RevokeExplicitRelease || reason > RevokeAdministrative {
		return ErrInvalidConfig
	}
	return l.revoke(ctx, client, id, generation, reason, false)
}

// Reconcile processes expiry and quarantined retries with production or test clocks.
func (l *Lifecycle) Reconcile(ctx context.Context) error {
	if l == nil || ctx == nil {
		return ErrClosed
	}
	now := l.config.Now().UTC()
	type work struct {
		client     peer.ID
		id         ID
		generation uint64
		reason     RevocationReason
		retry      bool
	}
	var pending []work
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return ErrClosed
	}
	for id, entry := range l.entries {
		switch {
		case entry.state == LeaseActive && !entry.lease.ValidUntil.After(now):
			pending = append(pending, work{client: entry.lease.ClientPeer, id: id, generation: entry.lease.Generation, reason: RevokeExpired})
		case entry.state == LeaseAllocated && (!entry.activationDeadline.After(now) || !entry.lease.ValidUntil.After(now)):
			pending = append(pending, work{client: entry.lease.ClientPeer, id: id, generation: entry.lease.Generation, reason: RevokeExpired})
		case entry.state == LeaseQuarantined && !entry.retryAt.After(now):
			pending = append(pending, work{client: entry.lease.ClientPeer, id: id, generation: entry.lease.Generation, reason: entry.lastReason, retry: true})
		}
	}
	l.mu.Unlock()
	var result error
	for _, item := range pending {
		result = errors.Join(result, l.revoke(ctx, item.client, item.id, item.generation, item.reason, item.retry))
	}
	return result
}

// Status returns bounded redacted snapshots without exposing resource handles.
func (l *Lifecycle) Status() []LeaseStatus {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]LeaseStatus, 0, len(l.entries))
	for _, entry := range l.entries {
		result = append(result, LeaseStatus{
			Lease: entry.lease, State: entry.state, ActivationDeadline: entry.activationDeadline, RetryAt: entry.retryAt,
			LastReason: entry.lastReason, HasError: entry.lastError != nil,
		})
	}
	return result
}

// LookupSession authenticates active ownership for BIND/STATE without changing revision.
func (l *Lifecycle) LookupSession(client peer.ID, id ID, generation uint64, token SessionToken) (Lease, error) {
	if l == nil {
		return Lease{}, ErrClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.closing {
		return Lease{}, ErrClosed
	}
	entry := l.entries[id]
	if entry == nil {
		l.stats.StaleOperations++
		return Lease{}, ErrUnknownLease
	}
	if entry.lease.ClientPeer != client {
		l.stats.StaleOperations++
		return Lease{}, ErrWrongPeer
	}
	if generation == 0 || entry.lease.Generation != generation ||
		l.config.RequireSessionToken && (token.IsZero() || entry.lease.Session != token) {
		l.stats.StaleOperations++
		return Lease{}, ErrStaleGeneration
	}
	if entry.state != LeaseActive {
		return Lease{}, ErrRevocationPending
	}
	if !entry.lease.ValidUntil.After(l.config.Now().UTC()) {
		return Lease{}, ErrLeaseExpired
	}
	return entry.lease, nil
}

// Stats returns a race-safe counter snapshot.
func (l *Lifecycle) Stats() LifecycleStats {
	if l == nil {
		return LifecycleStats{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stats
}

func (l *Lifecycle) entryForClientLocked(client peer.ID) *lifecycleEntry {
	for _, entry := range l.entries {
		if entry.lease.ClientPeer == client {
			return entry
		}
	}
	return nil
}

func (l *Lifecycle) revoke(ctx context.Context, client peer.ID, id ID, generation uint64, reason RevocationReason, retry bool) error {
	if l == nil || ctx == nil {
		return ErrClosed
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return ErrClosed
	}
	entry, exists := l.entries[id]
	if !exists {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return ErrUnknownLease
	}
	if entry.lease.ClientPeer != client {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return ErrWrongPeer
	}
	if generation == 0 || entry.lease.Generation != generation {
		l.stats.StaleOperations++
		l.mu.Unlock()
		return ErrStaleGeneration
	}
	if entry.state == LeaseRevoking || entry.state == LeaseRenewing {
		l.mu.Unlock()
		return ErrRevocationPending
	}
	if entry.state == LeaseQuarantined && !retry {
		l.mu.Unlock()
		return ErrRevocationPending
	}
	if entry.state != LeaseAllocated && entry.state != LeaseActive && entry.state != LeaseQuarantined {
		l.mu.Unlock()
		return ErrLeaseReleased
	}
	entry.state = LeaseRevoking
	entry.lastReason = reason
	entry.lastError = nil
	lease := entry.lease
	l.stats.RevocationsStarted++
	if retry {
		l.stats.RevocationRetries++
	}
	l.mu.Unlock()

	revokeContext, cancel := context.WithTimeout(ctx, l.config.RevocationTimeout)
	err := l.config.Revoker.RevokeLease(revokeContext, lease, reason)
	cancel()

	l.mu.Lock()
	defer l.mu.Unlock()
	current, stillCurrent := l.entries[id]
	if !stillCurrent || current != entry || current.lease.Generation != generation {
		l.stats.StaleOperations++
		return ErrStaleGeneration
	}
	if err != nil {
		entry.state = LeaseQuarantined
		entry.retryAt = l.config.Now().UTC().Add(l.config.RetryDelay)
		entry.lastError = err
		l.stats.Quarantined++
		return fmt.Errorf("revoke tunnel lease resources: %w", err)
	}
	if err := l.config.Allocator.ReleaseGeneration(client, id, generation); err != nil {
		entry.state = LeaseQuarantined
		entry.retryAt = l.config.Now().UTC().Add(l.config.RetryDelay)
		entry.lastError = err
		l.stats.Quarantined++
		return fmt.Errorf("finalize tunnel lease address release: %w", err)
	}
	entry.state = LeaseRevoked
	delete(l.entries, id)
	l.stats.Revoked++
	return nil
}

func (l *Lifecycle) run() {
	defer close(l.done)
	ticker := time.NewTicker(l.config.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(l.ctx, l.config.RevocationTimeout)
			_ = l.Reconcile(ctx)
			cancel()
		}
	}
}

// Close revokes all leases; failed cleanup must never make their addresses reusable.
func (l *Lifecycle) Close(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closing = true
		l.mu.Unlock()
		l.cancel()
		<-l.done
		l.supersedeWG.Wait()
		l.mu.Lock()
		type pending struct {
			client     peer.ID
			id         ID
			generation uint64
			retry      bool
		}
		values := make([]pending, 0, len(l.entries))
		for id, entry := range l.entries {
			values = append(values, pending{
				client: entry.lease.ClientPeer, id: id, generation: entry.lease.Generation,
				retry: entry.state == LeaseQuarantined,
			})
		}
		l.mu.Unlock()
		var result error
		for _, value := range values {
			result = errors.Join(result, l.revoke(ctx, value.client, value.id, value.generation, RevokeShutdown, value.retry))
		}
		l.mu.Lock()
		l.closed = true
		empty := len(l.entries) == 0
		l.mu.Unlock()
		if empty {
			result = errors.Join(result, l.config.Allocator.Close())
		}
		l.closeErr = result
	})
	return l.closeErr
}
