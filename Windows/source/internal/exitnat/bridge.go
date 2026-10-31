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
	"sync/atomic"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun"

	"github.com/Irviodotnet/Irvio/internal/exitbridge"
	"github.com/Irviodotnet/Irvio/internal/icmpv4"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

type BridgedConfig struct {
	TUN             tun.Device
	Control         netip.AddrPort
	Limit           int
	Backend         func(Emit) (*Backend, error)
	Install, Remove PeerChange
	Traffic         *exitbridge.Traffic
}

type bridgeLease struct {
	id   tunnellease.ID
	peer *exitbridge.Peer
}

// BridgedResources composes exact Core lease ownership, AWG plaintext fencing and physical NAT.
type BridgedResources struct {
	bridge          *exitbridge.Bridge
	backend         *Backend
	resources       atomic.Pointer[LeaseResources]
	mu              sync.RWMutex
	bySource        map[netip.Addr]bridgeLease
	install, remove PeerChange
	closeMu         sync.Mutex
}

var _ tunnellease.ResourceActivator = (*BridgedResources)(nil)
var _ tunnellease.ResourceRenewer = (*BridgedResources)(nil)
var _ tunnellease.ResourceRevoker = (*BridgedResources)(nil)

// NewBridgedResources owns TUN after bridge creation; a nonnil error result retains failed cleanup for retry.
func NewBridgedResources(c BridgedConfig) (*BridgedResources, error) {
	if c.Backend == nil || c.Install == nil || c.Remove == nil {
		return nil, ErrConfig
	}
	r := &BridgedResources{bySource: make(map[netip.Addr]bridgeLease), install: c.Install, remove: c.Remove}
	var err error
	r.bridge, err = exitbridge.New(exitbridge.Config{TUN: c.TUN, Control: c.Control, Limit: c.Limit, Forward: r.forward, Traffic: c.Traffic})
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*BridgedResources, error) {
		if cleanup := r.Close(context.Background()); cleanup != nil {
			return r, errors.Join(cause, cleanup)
		}
		return nil, cause
	}
	r.backend, err = c.Backend(r.deliver)
	if err != nil || r.backend == nil {
		return fail(errors.Join(err, ErrConfig))
	}
	resources, err := NewLeaseResources(r.backend, r.add, r.drop)
	if err != nil {
		return fail(err)
	}
	r.resources.Store(resources)
	return r, nil
}

func (r *BridgedResources) Bridge() *exitbridge.Bridge { return r.bridge }
func (r *BridgedResources) Stats() Stats {
	if r.backend == nil {
		return Stats{}
	}
	return r.backend.Stats()
}
func (r *BridgedResources) Err() error {
	if r.resources.Load() == nil || r.backend == nil {
		return ErrStale
	}
	return errors.Join(r.bridge.Err(), r.backend.Err())
}

func (r *BridgedResources) ActivateLease(ctx context.Context, v tunnellease.Lease) error {
	if err := r.Err(); err != nil {
		return err
	}
	return r.resources.Load().ActivateLease(ctx, v)
}
func (r *BridgedResources) RenewLease(ctx context.Context, previous, renewed tunnellease.Lease) error {
	if err := r.Err(); err != nil {
		return err
	}
	return r.resources.Load().RenewLease(ctx, previous, renewed)
}
func (r *BridgedResources) RevokeLease(ctx context.Context, v tunnellease.Lease, reason tunnellease.RevocationReason) error {
	resources := r.resources.Load()
	if resources == nil {
		return ErrStale
	}
	return resources.RevokeLease(ctx, v, reason)
}

func (r *BridgedResources) add(ctx context.Context, v tunnellease.Lease) error {
	p, err := r.bridge.Attach(v.IPv4.Addr(), false)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.bySource[v.IPv4.Addr()] = bridgeLease{v.ID, p}
	r.mu.Unlock()
	return r.install(ctx, v)
}

// drop runs after NAT revoke has fenced reply delivery and never holds the lookup lock across AWG removal.
func (r *BridgedResources) drop(ctx context.Context, v tunnellease.Lease) error {
	r.mu.RLock()
	binding, ok := r.bySource[v.IPv4.Addr()]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	if binding.id != v.ID {
		return ErrStale
	}
	if err := r.bridge.Retire(ctx, binding.peer); err != nil {
		return err
	}
	if err := r.remove(ctx, v); err != nil {
		return err
	}
	if err := r.bridge.Forget(binding.peer); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.bySource, v.IPv4.Addr())
	r.mu.Unlock()
	return nil
}

func (r *BridgedResources) forward(ctx context.Context, p *exitbridge.Peer, packet []byte) error {
	resources := r.resources.Load()
	if resources == nil {
		return ErrStale
	}
	r.mu.RLock()
	binding := r.bySource[p.Address()]
	r.mu.RUnlock()
	if binding.peer != p {
		return ErrStale
	}
	return resources.Outbound(ctx, packet)
}

// deliver executes under the NAT engine's lease fence and only queues for the current exact attachment.
func (r *BridgedResources) deliver(ctx context.Context, _ Path, packet []byte) error {
	p, err := parse(packet)
	destination := p.destination
	if err != nil {
		icmp, icmpErr := icmpv4.ParseError(packet)
		if icmpErr != nil {
			return err
		}
		destination = icmp.Recipient
	}
	r.mu.RLock()
	binding, ok := r.bySource[destination]
	r.mu.RUnlock()
	if !ok {
		return ErrStale
	}
	return r.bridge.Reply(ctx, binding.peer, packet)
}

// Close requires live AWG readers until lease cleanup succeeds; the transport and adapter are closed afterward.
func (r *BridgedResources) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrConfig
	}
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if resources := r.resources.Load(); resources != nil {
		if err := resources.Close(ctx); err != nil {
			return err
		}
	} else if r.backend != nil {
		if err := r.backend.Close(); err != nil {
			return err
		}
	}
	return r.bridge.Close()
}
