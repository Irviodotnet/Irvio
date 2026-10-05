// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"container/list"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

const cleanupLimit = 32

type ufragRegistration struct {
	ufrag      string
	families   map[socketmux.AddressFamily]uint64
	generation uint64
}

type retiredEntry struct {
	transaction retiredTransaction
	element     *list.Element
}

// Registry must precede mux creation so classifiers share the same bounded ICE routing index.
type Registry struct {
	config RegistryConfig

	mu           sync.Mutex
	ufrags       map[string]*ufragRegistration
	active       map[transactionID]transaction
	retired      map[transactionID]*retiredEntry
	retiredOrder list.List
	nextGen      uint64
	attached     bool
	closed       bool
	stats        counters
}

// NewRegistry creates an empty ICE registry.
func NewRegistry(config RegistryConfig) (*Registry, error) {
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("validate ICE registry: %w", err)
	}
	return &Registry{
		config:  config,
		ufrags:  make(map[string]*ufragRegistration, config.MaxUfrags),
		active:  make(map[transactionID]transaction, config.MaxTransactions),
		retired: make(map[transactionID]*retiredEntry, config.MaxRetired),
	}, nil
}

func (r *Registry) attach() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	if r.attached {
		return fmt.Errorf("ICE registry already has an adapter")
	}
	r.attached = true
	return nil
}

func (r *Registry) registerUfrag(ufrag string, family socketmux.AddressFamily) error {
	if !validUfrag(ufrag) {
		return ErrInvalidUfrag
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	registration := r.ufrags[ufrag]
	if registration == nil {
		if len(r.ufrags) >= r.config.MaxUfrags {
			return ErrTooManyUfrags
		}
		r.nextGen++
		registration = &ufragRegistration{
			ufrag: ufrag, families: make(map[socketmux.AddressFamily]uint64, 2), generation: r.nextGen,
		}
		r.ufrags[ufrag] = registration
		r.stats.ufragsRegistered.Add(1)
		updatePeak(&r.stats.peakActiveUfrags, uint64(len(r.ufrags)))
	}
	registration.families[family]++
	return nil
}

func (r *Registry) releaseUfragFamily(ufrag string, family socketmux.AddressFamily, force bool, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	registration := r.ufrags[ufrag]
	if registration == nil {
		return
	}
	refs := registration.families[family]
	if refs == 0 {
		return
	}
	if !force && refs > 1 {
		registration.families[family] = refs - 1
		return
	}
	delete(registration.families, family)
	r.retireMatchingLocked(ufrag, family, now)
	if len(registration.families) == 0 {
		delete(r.ufrags, ufrag)
		r.stats.ufragsRemoved.Add(1)
	}
}

func (r *Registry) removeUfrag(ufrag string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.ufrags[ufrag]; !exists {
		return
	}
	delete(r.ufrags, ufrag)
	r.stats.ufragsRemoved.Add(1)
	for id, tx := range r.active {
		if tx.ufrag == ufrag {
			delete(r.active, id)
			r.addRetiredLocked(tx, now)
		}
	}
}

func (r *Registry) hasUfrag(ufrag string, family socketmux.AddressFamily) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked(time.Now(), cleanupLimit)
	registration := r.ufrags[ufrag]
	return !r.closed && registration != nil && registration.families[family] > 0
}

func (r *Registry) registerTransaction(id transactionID, ufrag string, remote netip.AddrPort, family socketmux.AddressFamily, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	r.cleanupLocked(now, cleanupLimit)
	registration := r.ufrags[ufrag]
	if registration == nil || registration.families[family] == 0 {
		return ErrInvalidUfrag
	}
	if existing, exists := r.active[id]; exists {
		if existing.ufrag != ufrag || existing.remote != remote || existing.family != family {
			return ErrDuplicateTransaction
		}
		existing.expiresAt = now.Add(r.config.TransactionTTL)
		r.active[id] = existing
		return nil
	}
	if retired := r.retired[id]; retired != nil {
		if now.Before(retired.transaction.expiresAt) {
			return ErrDuplicateTransaction
		}
		r.removeRetiredLocked(retired)
	}
	if len(r.active) >= r.config.MaxTransactions {
		return ErrTooManyTransactions
	}
	r.active[id] = transaction{
		id: id, ufrag: ufrag, remote: remote, family: family,
		createdAt: now, expiresAt: now.Add(r.config.TransactionTTL),
	}
	r.stats.outboundTransactionsRegistered.Add(1)
	return nil
}

func (r *Registry) retireTransaction(id transactionID, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if tx, exists := r.active[id]; exists {
		delete(r.active, id)
		r.addRetiredLocked(tx, now)
	}
}

type responseDisposition uint8

const (
	responseUnknown responseDisposition = iota
	responseActive
	responseRetired
	responseWrongSource
	responseWrongFamily
)

func (r *Registry) classifyResponse(id transactionID, remote netip.AddrPort, family socketmux.AddressFamily, now time.Time) responseDisposition {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked(now, cleanupLimit)
	if tx, exists := r.active[id]; exists {
		if tx.family != family {
			return responseWrongFamily
		}
		if tx.remote != remote {
			return responseWrongSource
		}
		return responseActive
	}
	if retired := r.retired[id]; retired != nil && now.Before(retired.transaction.expiresAt) {
		if retired.transaction.family != family {
			return responseWrongFamily
		}
		if retired.transaction.remote != remote {
			return responseWrongSource
		}
		return responseRetired
	}
	return responseUnknown
}

func (r *Registry) responseOwner(id transactionID, remote netip.AddrPort, family socketmux.AddressFamily) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked(time.Now(), cleanupLimit)
	tx, exists := r.active[id]
	if !exists || tx.remote != remote || tx.family != family {
		return "", false
	}
	return tx.ufrag, true
}

// Transaction returns a copied diagnostic view when an outbound transaction is active.
func (r *Registry) Transaction(id [stun.TransactionIDSize]byte) (TransactionSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked(time.Now(), cleanupLimit)
	tx, exists := r.active[transactionID(id)]
	if !exists {
		return TransactionSnapshot{}, false
	}
	return TransactionSnapshot{
		LocalUfrag: tx.ufrag, Remote: tx.remote, Family: tx.family,
		CreatedAt: tx.createdAt, ExpiresAt: tx.expiresAt,
	}, true
}

func (r *Registry) close(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.attached = false
	for id, tx := range r.active {
		delete(r.active, id)
		r.addRetiredLocked(tx, now)
	}
	if len(r.ufrags) > 0 {
		r.stats.ufragsRemoved.Add(uint64(len(r.ufrags)))
	}
	clear(r.ufrags)
}

func (r *Registry) retireMatchingLocked(ufrag string, family socketmux.AddressFamily, now time.Time) {
	for id, tx := range r.active {
		if tx.ufrag == ufrag && tx.family == family {
			delete(r.active, id)
			r.addRetiredLocked(tx, now)
		}
	}
}

func (r *Registry) addRetiredLocked(tx transaction, now time.Time) {
	if existing := r.retired[tx.id]; existing != nil {
		r.removeRetiredLocked(existing)
	}
	for len(r.retired) >= r.config.MaxRetired {
		oldest := r.retiredOrder.Front()
		if oldest == nil {
			break
		}
		r.removeRetiredLocked(oldest.Value.(*retiredEntry))
	}
	entry := &retiredEntry{transaction: retiredTransaction{
		id: tx.id, remote: tx.remote, family: tx.family, expiresAt: now.Add(r.config.RetiredTTL),
	}}
	entry.element = r.retiredOrder.PushBack(entry)
	r.retired[tx.id] = entry
}

func (r *Registry) cleanupLocked(now time.Time, limit int) {
	cleaned := 0
	for id, tx := range r.active {
		if cleaned >= limit {
			break
		}
		if !now.Before(tx.expiresAt) {
			delete(r.active, id)
			r.addRetiredLocked(tx, now)
			cleaned++
		}
	}
	for cleaned < limit {
		oldest := r.retiredOrder.Front()
		if oldest == nil {
			return
		}
		entry := oldest.Value.(*retiredEntry)
		if now.Before(entry.transaction.expiresAt) {
			return
		}
		r.removeRetiredLocked(entry)
		cleaned++
	}
}

func (r *Registry) removeRetiredLocked(entry *retiredEntry) {
	delete(r.retired, entry.transaction.id)
	if entry.element != nil {
		r.retiredOrder.Remove(entry.element)
		entry.element = nil
	}
}

func (r *Registry) sizes() (ufrags, active, retired uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked(time.Now(), cleanupLimit)
	return uint64(len(r.ufrags)), uint64(len(r.active)), uint64(len(r.retired))
}
