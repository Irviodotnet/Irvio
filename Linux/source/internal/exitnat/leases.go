// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"context"
	"errors"
	"net/netip"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

// PeerChange changes only the exact authenticated AWG peer and must not re-enter lease lifecycle operations.
type PeerChange func(context.Context, tunnellease.Lease) error

type leaseResource struct {
	value  tunnellease.Lease
	handle *Lease
	active bool
}

// LeaseResources adapts existing Core lease lifecycle to NAT and generation-owned AWG peer operations.
type LeaseResources struct {
	mu              sync.Mutex
	packetMu        sync.RWMutex
	backend         *Backend
	install, remove PeerChange
	byID            map[tunnellease.ID]*leaseResource
	bySource        map[netip.Addr]*leaseResource
	limit           int
	closed          bool
}

var _ tunnellease.ResourceActivator = (*LeaseResources)(nil)
var _ tunnellease.ResourceRenewer = (*LeaseResources)(nil)
var _ tunnellease.ResourceRevoker = (*LeaseResources)(nil)

// NewLeaseResources takes backend ownership only on success; peer hooks must already enforce Core authorization.
func NewLeaseResources(backend *Backend, install, remove PeerChange) (*LeaseResources, error) {
	if backend == nil || install == nil || remove == nil {
		return nil, ErrConfig
	}
	if err := backend.Err(); err != nil {
		return nil, err
	}
	return &LeaseResources{backend: backend, install: install, remove: remove, limit: backend.engine.c.MaxLeases, byID: make(map[tunnellease.ID]*leaseResource), bySource: make(map[netip.Addr]*leaseResource)}, nil
}

func (r *LeaseResources) ActivateLease(ctx context.Context, value tunnellease.Lease) error {
	if ctx == nil {
		return ErrConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrStale
	}
	if current := r.byID[value.ID]; current != nil {
		if current.value == value && current.active {
			return r.backend.Renew(current.handle, value)
		}
		return ErrStale
	}
	if r.bySource[value.IPv4.Addr()] != nil {
		return ErrStale
	}
	if len(r.byID) >= r.limit {
		return ErrCapacity
	}
	handle, err := r.backend.AddLease(value)
	if err != nil {
		return err
	}
	resource := &leaseResource{value: value, handle: handle}
	r.packetMu.Lock()
	r.byID[value.ID], r.bySource[value.IPv4.Addr()] = resource, resource
	r.packetMu.Unlock()
	if err := r.install(ctx, value); err != nil {
		return errors.Join(err, r.revokeLocked(ctx, resource))
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, r.revokeLocked(ctx, resource))
	}
	if err := r.backend.Renew(handle, value); err != nil {
		return errors.Join(err, r.revokeLocked(ctx, resource))
	}
	r.packetMu.Lock()
	resource.active = true
	r.packetMu.Unlock()
	return nil
}

func (r *LeaseResources) RenewLease(ctx context.Context, previous, renewed tunnellease.Lease) error {
	if ctx == nil {
		return ErrConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	resource := r.byID[previous.ID]
	if r.closed || resource == nil || !resource.active || resource.value != previous || renewed.Generation <= previous.Generation {
		return ErrStale
	}
	if err := r.backend.Renew(resource.handle, renewed); err != nil {
		return err
	}
	resource.value = renewed
	return nil
}

func (r *LeaseResources) RevokeLease(ctx context.Context, value tunnellease.Lease, _ tunnellease.RevocationReason) error {
	if ctx == nil {
		return ErrConfig
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	resource := r.byID[value.ID]
	if resource == nil {
		return nil
	}
	if resource.value != value {
		return ErrStale
	}
	return r.revokeLocked(ctx, resource)
}

func (r *LeaseResources) revokeLocked(ctx context.Context, resource *leaseResource) error {
	r.packetMu.Lock()
	resource.active = false
	r.packetMu.Unlock()
	if err := r.backend.Revoke(resource.handle); err != nil {
		return err
	}
	if err := r.remove(ctx, resource.value); err != nil {
		return err
	}
	r.packetMu.Lock()
	delete(r.byID, resource.value.ID)
	delete(r.bySource, resource.value.IPv4.Addr())
	r.packetMu.Unlock()
	return nil
}

// Outbound accepts only AWG-authenticated plaintext after cryptokey source validation, never raw host input.
func (r *LeaseResources) Outbound(ctx context.Context, packet []byte) error {
	if ctx == nil {
		return ErrConfig
	}
	p, err := parse(packet)
	if err != nil {
		return err
	}
	r.packetMu.RLock()
	resource := r.bySource[p.source]
	if r.closed || resource == nil || !resource.active {
		r.packetMu.RUnlock()
		return ErrStale
	}
	handle := resource.handle
	r.packetMu.RUnlock()
	return r.backend.Outbound(ctx, handle, packet)
}

// Close fences new packets before cleanup and retains failed peer removals for a later retry.
func (r *LeaseResources) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrConfig
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.packetMu.Lock()
	r.closed = true
	r.packetMu.Unlock()
	var result error
	for _, resource := range r.byID {
		result = errors.Join(result, r.revokeLocked(ctx, resource))
	}
	return errors.Join(result, r.backend.Close())
}
