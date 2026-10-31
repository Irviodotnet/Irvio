// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnellease

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type retiredLease struct {
	id      ID
	expires time.Time
}

// Allocator guarantees Exit-local active address uniqueness without persistence or network ownership.
type Allocator struct {
	config   Config
	reserved map[netip.Addr]struct{}
	active   map[ID]Lease
	byClient map[peer.ID]ID
	used     map[netip.Addr]ID
	recent   map[ID]time.Time
	order    []retiredLease

	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

func New(config Config) (*Allocator, error) {
	if err := validateConfig(&config); err != nil {
		return nil, err
	}
	reserved := make(map[netip.Addr]struct{}, len(config.Reserved))
	for _, address := range config.Reserved {
		if !validAddress(address) || !inConfiguredPool(config, address) {
			return nil, ErrInvalidConfig
		}
		reserved[address] = struct{}{}
	}
	return &Allocator{
		config: config, reserved: reserved, active: make(map[ID]Lease),
		byClient: make(map[peer.ID]ID), used: make(map[netip.Addr]ID),
		recent: make(map[ID]time.Time),
	}, nil
}

func validateConfig(config *Config) error {
	if config.ExitPeer == "" || config.IPv4Pool.IsValid() && !config.IPv4Pool.Addr().Is4() ||
		config.IPv6Pool.IsValid() && (!config.IPv6Pool.Addr().Is6() || config.IPv6Pool.Addr().Is4In6()) ||
		!config.IPv4Pool.IsValid() && !config.IPv6Pool.IsValid() {
		return ErrInvalidConfig
	}
	if config.TTL == 0 {
		config.TTL = DefaultTTL
	}
	if config.MaxActive == 0 {
		config.MaxActive = DefaultMaxActive
	}
	if config.MaxRecent == 0 {
		config.MaxRecent = DefaultMaxRecent
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		return ErrInvalidConfig
	}
	if config.TTL <= 0 || config.TTL > 24*time.Hour || config.MaxActive < 1 || config.MaxActive > 4096 ||
		config.MaxRecent < 1 || config.MaxRecent > 8192 {
		return ErrInvalidConfig
	}
	return nil
}

func validAddress(address netip.Addr) bool {
	return address.IsValid() && !address.IsUnspecified() && !address.IsMulticast() && !address.Is4In6()
}

func inConfiguredPool(config Config, address netip.Addr) bool {
	return address.Is4() && config.IPv4Pool.IsValid() && config.IPv4Pool.Contains(address) ||
		address.Is6() && config.IPv6Pool.IsValid() && config.IPv6Pool.Contains(address)
}

// Allocate creates at most one active lease per authenticated client.
func (a *Allocator) Allocate(client peer.ID) (Lease, error) {
	if a == nil {
		return Lease{}, ErrClosed
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return Lease{}, ErrClosed
	}
	if client == "" || client == a.config.ExitPeer {
		return Lease{}, ErrInvalidPeer
	}
	now := a.config.Now().UTC()
	if !a.config.ManagedExpiry {
		a.expireLocked(now)
	}
	if id, exists := a.byClient[client]; exists {
		lease := a.active[id]
		if !lease.ValidUntil.After(now) {
			return Lease{}, ErrLeaseExpired
		}
		return lease, nil
	}
	if len(a.active) >= a.config.MaxActive {
		return Lease{}, ErrExhausted
	}
	lease := Lease{ExitPeer: a.config.ExitPeer, ClientPeer: client, Generation: 1, ValidUntil: now.Add(a.config.TTL)}
	var err error
	lease.ID, err = a.newIDLocked()
	if err != nil {
		return Lease{}, err
	}
	if a.config.IPv4Pool.IsValid() {
		address, ok := a.nextAddressLocked(a.config.IPv4Pool)
		if !ok {
			return Lease{}, ErrExhausted
		}
		lease.IPv4 = netip.PrefixFrom(address, 32)
	}
	if a.config.IPv6Pool.IsValid() {
		address, ok := a.nextAddressLocked(a.config.IPv6Pool)
		if !ok {
			return Lease{}, ErrExhausted
		}
		lease.IPv6 = netip.PrefixFrom(address, 128)
	}
	a.active[lease.ID] = lease
	a.byClient[client] = lease.ID
	if lease.IPv4.IsValid() {
		a.used[lease.IPv4.Addr()] = lease.ID
	}
	if lease.IPv6.IsValid() {
		a.used[lease.IPv6.Addr()] = lease.ID
	}
	return lease, nil
}

func (a *Allocator) newIDLocked() (ID, error) {
	for range 8 {
		var id ID
		if _, err := io.ReadFull(a.config.Random, id[:]); err != nil {
			return ID{}, fmt.Errorf("generate tunnel lease ID: %w", err)
		}
		if id.IsZero() {
			continue
		}
		if _, exists := a.active[id]; exists {
			continue
		}
		if _, exists := a.recent[id]; !exists {
			return id, nil
		}
	}
	return ID{}, errors.New("generate unique tunnel lease ID")
}

func (a *Allocator) nextAddressLocked(pool netip.Prefix) (netip.Addr, bool) {
	masked := pool.Masked()
	address := masked.Addr()
	for scanned := 0; scanned < a.config.MaxActive+len(a.reserved)+4 && address.IsValid() && pool.Contains(address); scanned++ {
		candidate := address
		address = address.Next()
		if candidate == masked.Addr() {
			continue
		}
		if candidate.Is4() && pool.Bits() < 31 && (!address.IsValid() || !pool.Contains(address)) {
			continue
		}
		if _, reserved := a.reserved[candidate]; reserved {
			continue
		}
		if _, used := a.used[candidate]; !used {
			return candidate, true
		}
	}
	return netip.Addr{}, false
}

// Release authenticates lease ownership and rejects replays using bounded quarantine.
func (a *Allocator) Release(client peer.ID, id ID) error {
	return a.ReleaseGeneration(client, id, 0)
}

// ReleaseGeneration fences exact managed generations; zero retains legacy resource-free behavior.
func (a *Allocator) ReleaseGeneration(client peer.ID, id ID, generation uint64) error {
	if a == nil {
		return ErrClosed
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	now := a.config.Now().UTC()
	if !a.config.ManagedExpiry {
		a.expireLocked(now)
	}
	lease, exists := a.active[id]
	if !exists {
		if _, retired := a.recent[id]; retired {
			return ErrLeaseReleased
		}
		return ErrUnknownLease
	}
	if client == "" || lease.ClientPeer != client {
		return ErrWrongPeer
	}
	if generation != 0 && lease.Generation != generation {
		return ErrStaleGeneration
	}
	a.retireLocked(lease, now)
	return nil
}

// Renew extends one live lease and advances its anti-replay generation.
func (a *Allocator) Renew(client peer.ID, id ID, generation uint64) (Lease, error) {
	if a == nil {
		return Lease{}, ErrClosed
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return Lease{}, ErrClosed
	}
	now := a.config.Now().UTC()
	if !a.config.ManagedExpiry {
		a.expireLocked(now)
	}
	lease, exists := a.active[id]
	if !exists {
		if _, retired := a.recent[id]; retired {
			return Lease{}, ErrLeaseReleased
		}
		return Lease{}, ErrUnknownLease
	}
	if client == "" || lease.ClientPeer != client {
		return Lease{}, ErrWrongPeer
	}
	if generation == 0 || lease.Generation != generation {
		return Lease{}, ErrStaleGeneration
	}
	if !lease.ValidUntil.After(now) {
		return Lease{}, ErrLeaseExpired
	}
	if lease.Generation == ^uint64(0) {
		return Lease{}, ErrStaleGeneration
	}
	lease.Generation++
	lease.ValidUntil = now.Add(a.config.TTL)
	a.active[id] = lease
	return lease, nil
}

// renewManagedMutation keeps address ownership while Lifecycle fences expiry outside LeaseActive.
func (a *Allocator) renewManagedMutation(client peer.ID, id ID, generation uint64) (Lease, error) {
	if a == nil {
		return Lease{}, ErrClosed
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return Lease{}, ErrClosed
	}
	if !a.config.ManagedExpiry {
		return Lease{}, ErrInvalidConfig
	}
	lease, exists := a.active[id]
	if !exists {
		if _, retired := a.recent[id]; retired {
			return Lease{}, ErrLeaseReleased
		}
		return Lease{}, ErrUnknownLease
	}
	if client == "" || lease.ClientPeer != client {
		return Lease{}, ErrWrongPeer
	}
	if generation == 0 || lease.Generation != generation || lease.Generation == ^uint64(0) {
		return Lease{}, ErrStaleGeneration
	}
	lease.Generation++
	lease.ValidUntil = a.config.Now().UTC().Add(a.config.TTL)
	a.active[id] = lease
	return lease, nil
}

// Expired snapshots expired managed leases without releasing addresses.
func (a *Allocator) Expired(now time.Time) []Lease {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	result := make([]Lease, 0)
	for _, lease := range a.active {
		if !lease.ValidUntil.After(now) {
			result = append(result, lease)
		}
	}
	return result
}

// Lookup returns an immutable lease snapshot for lifecycle fencing.
func (a *Allocator) Lookup(id ID) (Lease, bool) {
	if a == nil {
		return Lease{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return Lease{}, false
	}
	lease, exists := a.active[id]
	return lease, exists
}

func (a *Allocator) retireLocked(lease Lease, now time.Time) {
	delete(a.active, lease.ID)
	delete(a.byClient, lease.ClientPeer)
	if lease.IPv4.IsValid() {
		delete(a.used, lease.IPv4.Addr())
	}
	if lease.IPv6.IsValid() {
		delete(a.used, lease.IPv6.Addr())
	}
	expires := now.Add(a.config.TTL)
	a.recent[lease.ID] = expires
	a.order = append(a.order, retiredLease{id: lease.ID, expires: expires})
	a.trimRecentLocked(now)
}

func (a *Allocator) expireLocked(now time.Time) {
	for _, lease := range a.active {
		if !lease.ValidUntil.After(now) {
			a.retireLocked(lease, now)
		}
	}
	a.trimRecentLocked(now)
}

func (a *Allocator) trimRecentLocked(now time.Time) {
	for len(a.order) > 0 && (!a.order[0].expires.After(now) || len(a.order) > a.config.MaxRecent) {
		oldest := a.order[0]
		a.order = a.order[1:]
		if expiry, exists := a.recent[oldest.id]; exists && expiry == oldest.expires {
			delete(a.recent, oldest.id)
		}
	}
}

func (a *Allocator) Active() []Lease {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	if !a.config.ManagedExpiry {
		a.expireLocked(a.config.Now().UTC())
	}
	result := make([]Lease, 0, len(a.active))
	for _, lease := range a.active {
		result = append(result, lease)
	}
	return result
}

func (a *Allocator) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.active = nil
		a.byClient = nil
		a.used = nil
		a.recent = nil
		a.order = nil
		a.mu.Unlock()
	})
	return nil
}
