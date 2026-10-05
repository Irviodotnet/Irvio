// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"container/list"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/pion/stun/v3"

	"hydra.local/hydra/internal/socketmux"
)

const retiredCleanupLimit = 16

// MaxResponseSources bounds the response-source policy retained per transaction.
const MaxResponseSources = 4

type transactionID = TransactionID

type transactionState uint8

const (
	transactionActive transactionState = iota + 1
	transactionCompleted
	transactionRetired
)

type responseEvent struct {
	message *stun.Message
	source  netip.AddrPort
	family  socketmux.AddressFamily
	err     error
}

type transaction struct {
	id        transactionID
	policy    responseSourcePolicy
	family    socketmux.AddressFamily
	createdAt time.Time
	state     transactionState
	delivery  chan responseEvent
}

type retiredTransaction struct {
	id        transactionID
	policy    responseSourcePolicy
	family    socketmux.AddressFamily
	expiresAt time.Time
	element   *list.Element
}

type responseSourcePolicy struct {
	success    netip.AddrPort
	errors     [MaxResponseSources]netip.AddrPort
	errorCount uint8
}

func (p responseSourcePolicy) matches(source netip.AddrPort, class stun.MessageClass) bool {
	if class == stun.ClassSuccessResponse {
		return source == p.success
	}
	if class != stun.ClassErrorResponse {
		return false
	}
	for index := 0; index < int(p.errorCount); index++ {
		if source == p.errors[index] {
			return true
		}
	}
	return false
}

// Registry is a bounded, concurrent active and retired transaction index.
type Registry struct {
	config RegistryConfig

	mu           sync.Mutex
	active       map[transactionID]*transaction
	retired      map[transactionID]*retiredTransaction
	retiredOrder list.List
	lastNow      time.Time
	attached     bool
	stats        registryCounters
}

// NewRegistry creates an empty transaction registry before socket construction.
func NewRegistry(config RegistryConfig) (*Registry, error) {
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("validate STUN registry config: %w", err)
	}
	return &Registry{
		config:  config,
		active:  make(map[transactionID]*transaction, config.MaxInFlight),
		retired: make(map[transactionID]*retiredTransaction, config.MaxRetired),
	}, nil
}

func (r *Registry) attach() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.attached {
		return fmt.Errorf("STUN registry already has a dispatcher")
	}
	r.attached = true
	return nil
}

func (r *Registry) detach() {
	r.mu.Lock()
	r.attached = false
	r.mu.Unlock()
}

func (r *Registry) register(id transactionID, policy responseSourcePolicy, family socketmux.AddressFamily, now time.Time) (*transaction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now = r.advanceNowLocked(now)
	r.cleanupExpiredLocked(now, retiredCleanupLimit)
	if len(r.active) >= r.config.MaxInFlight {
		return nil, ErrInFlightLimit
	}
	if _, exists := r.active[id]; exists {
		return nil, ErrDuplicateTransaction
	}
	if retired, exists := r.retired[id]; exists {
		if now.Before(retired.expiresAt) {
			return nil, ErrDuplicateTransaction
		}
		r.removeRetiredLocked(retired)
	}
	tx := &transaction{
		id: id, policy: policy, family: family, createdAt: now,
		state: transactionActive, delivery: make(chan responseEvent, 1),
	}
	r.active[id] = tx
	return tx, nil
}

func (r *Registry) retire(tx *transaction, eventErr error, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now = r.advanceNowLocked(now)
	current, exists := r.active[tx.id]
	if !exists || current != tx {
		return false
	}
	delete(r.active, tx.id)
	tx.state = transactionRetired
	r.addRetiredLocked(tx.id, tx.policy, tx.family, now)
	if eventErr != nil {
		select {
		case tx.delivery <- responseEvent{err: eventErr}:
		default:
			r.stats.dispatcherQueueDrops.Add(1)
		}
	}
	return true
}

func (r *Registry) complete(id transactionID, source netip.AddrPort, family socketmux.AddressFamily, message *stun.Message, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now = r.advanceNowLocked(now)
	tx, exists := r.active[id]
	if !exists {
		if retired, retiredExists := r.retired[id]; retiredExists && now.Before(retired.expiresAt) && retired.policy.matches(source, message.Type.Class) && retired.family == family {
			r.stats.retiredLate.Add(1)
		}
		return false
	}
	if !tx.policy.matches(source, message.Type.Class) || tx.family != family {
		return false
	}
	delete(r.active, id)
	tx.state = transactionCompleted
	r.addRetiredLocked(id, tx.policy, family, now)
	select {
	case tx.delivery <- responseEvent{message: message, source: source, family: family}:
		return true
	default:
		r.stats.dispatcherQueueDrops.Add(1)
		return false
	}
}

func (r *Registry) retireAll(eventErr error, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now = r.advanceNowLocked(now)
	for id, tx := range r.active {
		delete(r.active, id)
		tx.state = transactionRetired
		r.addRetiredLocked(id, tx.policy, tx.family, now)
		select {
		case tx.delivery <- responseEvent{err: eventErr}:
		default:
			r.stats.dispatcherQueueDrops.Add(1)
		}
	}
}

func (r *Registry) classify(id transactionID, source netip.AddrPort, family socketmux.AddressFamily, class stun.MessageClass, now time.Time) socketmux.PacketClass {
	r.mu.Lock()
	defer r.mu.Unlock()
	now = r.advanceNowLocked(now)
	r.cleanupExpiredLocked(now, retiredCleanupLimit)
	if tx, exists := r.active[id]; exists {
		if tx.policy.matches(source, class) && tx.family == family {
			return socketmux.Control
		}
		r.stats.wrongSource.Add(1)
		return socketmux.Data
	}
	if retired, exists := r.retired[id]; exists {
		if !now.Before(retired.expiresAt) {
			r.removeRetiredLocked(retired)
			r.stats.unknownTransaction.Add(1)
			return socketmux.Data
		}
		if retired.policy.matches(source, class) && retired.family == family {
			r.stats.retiredLate.Add(1)
			return socketmux.Drop
		}
		r.stats.wrongSource.Add(1)
		return socketmux.Data
	}
	r.stats.unknownTransaction.Add(1)
	return socketmux.Data
}

func (r *Registry) addRetiredLocked(id transactionID, policy responseSourcePolicy, family socketmux.AddressFamily, now time.Time) {
	r.cleanupExpiredLocked(now, retiredCleanupLimit)
	if existing, exists := r.retired[id]; exists {
		r.removeRetiredLocked(existing)
	}
	for len(r.retired) >= r.config.MaxRetired {
		oldest := r.retiredOrder.Front()
		if oldest == nil {
			break
		}
		r.removeRetiredLocked(oldest.Value.(*retiredTransaction))
	}
	entry := &retiredTransaction{id: id, policy: policy, family: family, expiresAt: now.Add(r.config.RetiredGrace)}
	entry.element = r.retiredOrder.PushBack(entry)
	r.retired[id] = entry
}

func (r *Registry) cleanupExpiredLocked(now time.Time, limit int) {
	for cleaned := 0; cleaned < limit; cleaned++ {
		oldest := r.retiredOrder.Front()
		if oldest == nil {
			return
		}
		entry := oldest.Value.(*retiredTransaction)
		if now.Before(entry.expiresAt) {
			return
		}
		r.removeRetiredLocked(entry)
	}
}

func (r *Registry) removeRetiredLocked(entry *retiredTransaction) {
	delete(r.retired, entry.id)
	r.retiredOrder.Remove(entry.element)
}

func (r *Registry) advanceNowLocked(now time.Time) time.Time {
	if now.Before(r.lastNow) {
		return r.lastNow
	}
	r.lastNow = now
	return now
}
