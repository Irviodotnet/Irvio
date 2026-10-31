// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

const maxWindowsUnderlayLeases = 1024

var ErrWindowsUnderlayBusy = errors.New("Windows underlay grants are owned or awaiting cleanup")

// WindowsUnderlayOwner exclusively owns dynamic grants within one protection installation.
type WindowsUnderlayOwner struct {
	updater                          *WindowsNetworkUpdater
	generation                       uint64
	next                             uint64
	leases                           map[uint64]WindowsBypass
	listeners                        map[uint64]WindowsControlListener
	datagrams                        map[uint64]WindowsDatagramSocket
	dirty, draining, closing, closed bool
	dirtyCause                       error
}

type WindowsUnderlayLease struct {
	owner          *WindowsUnderlayOwner
	generation, id uint64
}

// OwnUnderlay refuses to adopt unowned grants or replace a live dynamic owner.
func (u *WindowsNetworkUpdater) OwnUnderlay(ctx context.Context, generation uint64) (*WindowsUnderlayOwner, error) {
	if u == nil || u.backend == nil || generation == 0 {
		return nil, ErrInvalidConfig
	}
	b := u.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, b.policy); err != nil {
		return nil, err
	}
	if u.epoch != b.epoch {
		return nil, ErrStaleGeneration
	}
	if b.underlayOwner != nil || len(b.network.Underlay) != 0 || len(b.network.ControlListeners) != 0 || len(b.network.DatagramSockets) != 0 || b.network.Application == "" {
		return nil, ErrWindowsUnderlayBusy
	}
	if err := b.verify(ctx); err != nil {
		return nil, err
	}
	o := &WindowsUnderlayOwner{updater: u, generation: generation, leases: make(map[uint64]WindowsBypass), listeners: make(map[uint64]WindowsControlListener), datagrams: make(map[uint64]WindowsDatagramSocket)}
	b.underlayOwner = o
	return o, nil
}

func (o *WindowsUnderlayOwner) live(ctx context.Context) error {
	b := o.updater.backend
	if err := b.check(ctx, b.policy); err != nil {
		return err
	}
	if o.closed || o.updater.epoch != b.epoch || b.underlayOwner != o {
		return ErrStaleGeneration
	}
	return nil
}

func (o *WindowsUnderlayOwner) grants(skip uint64) []WindowsBypass {
	seen := make(map[WindowsBypass]bool)
	var result []WindowsBypass
	for id, e := range o.leases {
		if id != skip && !seen[e] {
			seen[e] = true
			result = append(result, e)
		}
	}
	slices.SortFunc(result, func(a, b WindowsBypass) int {
		for _, order := range []int{cmp.Compare(a.Interface, b.Interface), a.Source.Compare(b.Source), a.Remote.Compare(b.Remote), cmp.Compare(a.Protocol, b.Protocol), cmp.Compare(a.LocalPort, b.LocalPort)} {
			if order != 0 {
				return order
			}
		}
		return 0
	})
	return result
}

func (o *WindowsUnderlayOwner) listenerGrants(skip uint64) []WindowsControlListener {
	seen := make(map[WindowsControlListener]bool)
	var result []WindowsControlListener
	for id, e := range o.listeners {
		if id != skip && !seen[e] {
			seen[e] = true
			result = append(result, e)
		}
	}
	slices.SortFunc(result, func(a, b WindowsControlListener) int {
		if order := cmp.Compare(a.Interface, b.Interface); order != 0 {
			return order
		}
		return a.Local.Compare(b.Local)
	})
	return result
}

func (o *WindowsUnderlayOwner) datagramGrants(skip uint64) []WindowsDatagramSocket {
	seen := make(map[WindowsDatagramSocket]bool)
	var result []WindowsDatagramSocket
	for id, e := range o.datagrams {
		if id != skip && !seen[e] {
			seen[e] = true
			result = append(result, e)
		}
	}
	slices.SortFunc(result, func(a, b WindowsDatagramSocket) int {
		if order := cmp.Compare(a.Interface, b.Interface); order != 0 {
			return order
		}
		return a.Local.Compare(b.Local)
	})
	return result
}

func (o *WindowsUnderlayOwner) apply(ctx context.Context, grants []WindowsBypass, listeners []WindowsControlListener, datagrams []WindowsDatagramSocket) error {
	b := o.updater.backend
	n := cloneWindowsNetwork(b.network)
	n.Underlay = grants
	n.ControlListeners = listeners
	n.DatagramSockets = datagrams
	if err := b.replaceNetwork(ctx, n); err != nil {
		o.dirty = true
		o.dirtyCause = err
		return err
	}
	o.dirty = false
	o.dirtyCause = nil
	return nil
}

// Acquire returns an owned lease even on a possibly committed failure; callers must close it.
func (o *WindowsUnderlayOwner) Acquire(ctx context.Context, generation uint64, grant WindowsBypass) (*WindowsUnderlayLease, error) {
	return o.acquire(ctx, generation, &grant, nil, nil)
}

// AcquireListener requires retaining an exclusive bound listener until this lease is revoked.
func (o *WindowsUnderlayOwner) AcquireListener(ctx context.Context, generation uint64, grant WindowsControlListener) (*WindowsUnderlayLease, error) {
	return o.acquire(ctx, generation, nil, &grant, nil)
}

// AcquireDatagramSocket requires holding the exclusive bound socket until this lease is revoked.
func (o *WindowsUnderlayOwner) AcquireDatagramSocket(ctx context.Context, generation uint64, grant WindowsDatagramSocket) (*WindowsUnderlayLease, error) {
	return o.acquire(ctx, generation, nil, nil, &grant)
}

func (o *WindowsUnderlayOwner) acquire(ctx context.Context, generation uint64, grant *WindowsBypass, listener *WindowsControlListener, datagram *WindowsDatagramSocket) (*WindowsUnderlayLease, error) {
	if o == nil || o.updater == nil {
		return nil, ErrInvalidConfig
	}
	b := o.updater.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := o.live(ctx); err != nil {
		return nil, err
	}
	if generation != o.generation {
		return nil, ErrStaleGeneration
	}
	if o.closing || o.draining || o.dirty {
		return nil, errors.Join(ErrWindowsUnderlayBusy, o.dirtyCause)
	}
	if len(o.leases)+len(o.listeners)+len(o.datagrams) >= maxWindowsUnderlayLeases || o.next == ^uint64(0) {
		return nil, fmt.Errorf("Windows control grant capacity exhausted")
	}
	n := cloneWindowsNetwork(b.network)
	if grant != nil {
		n.Underlay = []WindowsBypass{*grant}
	} else if listener != nil {
		n.ControlListeners = []WindowsControlListener{*listener}
	} else {
		n.DatagramSockets = []WindowsDatagramSocket{*datagram}
	}
	if _, err := compileWindowsPolicy(b.owner, n); err != nil {
		return nil, err
	}
	o.next++
	lease := &WindowsUnderlayLease{owner: o, generation: generation, id: o.next}
	if grant != nil {
		o.leases[lease.id] = *grant
	} else if listener != nil {
		o.listeners[lease.id] = *listener
	} else {
		o.datagrams[lease.id] = *datagram
	}
	grants := o.grants(0)
	listeners := o.listenerGrants(0)
	datagrams := o.datagramGrants(0)
	if len(grants) > 128 || len(listeners) > 8 || len(datagrams) > 4 {
		delete(o.leases, lease.id)
		delete(o.listeners, lease.id)
		delete(o.datagrams, lease.id)
		return nil, fmt.Errorf("too many Windows control endpoints")
	}
	var err error
	if slices.Equal(grants, b.network.Underlay) && slices.Equal(listeners, b.network.ControlListeners) && slices.Equal(datagrams, b.network.DatagramSockets) {
		err = b.verify(ctx)
		if err != nil {
			o.dirty = true
			o.dirtyCause = err
		}
	} else {
		err = o.apply(ctx, grants, listeners, datagrams)
	}
	if err != nil && ctx.Err() != nil {
		// Roll back a cancelled acquisition before another socket can observe its temporary dirty owner.
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if cleanupErr := o.apply(cleanup, o.grants(lease.id), o.listenerGrants(lease.id), o.datagramGrants(lease.id)); cleanupErr != nil {
			return lease, errors.Join(err, cleanupErr)
		}
		delete(o.leases, lease.id)
		delete(o.listeners, lease.id)
		delete(o.datagrams, lease.id)
		return nil, err
	}
	return lease, err
}

// Advance fences new acquisitions before withdrawing every predecessor endpoint.
func (o *WindowsUnderlayOwner) Advance(ctx context.Context, generation uint64) error {
	if o == nil || o.updater == nil || generation == 0 {
		return ErrInvalidConfig
	}
	b := o.updater.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := o.live(ctx); err != nil {
		return err
	}
	if o.closing || generation < o.generation {
		return ErrStaleGeneration
	}
	if generation == o.generation && !o.draining && !o.dirty {
		return b.verify(ctx)
	}
	o.generation, o.draining = generation, true
	var canceled error
	if err := o.apply(ctx, nil, nil, nil); err != nil {
		if ctx.Err() == nil {
			return err
		}
		// Finish withdrawing predecessor grants even when migration is canceled by shutdown.
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if cleanupErr := o.apply(cleanup, nil, nil, nil); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		canceled = err
	}
	o.leases = make(map[uint64]WindowsBypass)
	o.listeners = make(map[uint64]WindowsControlListener)
	o.datagrams = make(map[uint64]WindowsDatagramSocket)
	o.draining = false
	return canceled
}

func (l *WindowsUnderlayLease) Close() error {
	if l == nil || l.owner == nil {
		return nil
	}
	o := l.owner
	b := o.updater.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	if o.closed || l.generation < o.generation && !o.draining {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := o.live(ctx); err != nil {
		return err
	}
	if o.closing {
		return ErrWindowsUnderlayBusy
	}
	if o.draining {
		return ErrStaleGeneration
	}
	_, dialExists := o.leases[l.id]
	_, listenerExists := o.listeners[l.id]
	_, datagramExists := o.datagrams[l.id]
	if !dialExists && !listenerExists && !datagramExists {
		return nil
	}
	grants := o.grants(l.id)
	listeners := o.listenerGrants(l.id)
	datagrams := o.datagramGrants(l.id)
	if slices.Equal(grants, b.network.Underlay) && slices.Equal(listeners, b.network.ControlListeners) && slices.Equal(datagrams, b.network.DatagramSockets) && !o.dirty {
		if err := b.verify(ctx); err != nil {
			o.dirty = true
			o.dirtyCause = err
			return err
		}
	} else if err := o.apply(ctx, grants, listeners, datagrams); err != nil {
		return err
	}
	delete(o.leases, l.id)
	delete(o.listeners, l.id)
	delete(o.datagrams, l.id)
	return nil
}

// Close retains ownership on native cleanup failure and never removes the protection policy.
func (o *WindowsUnderlayOwner) Close() error {
	if o == nil || o.updater == nil {
		return nil
	}
	b := o.updater.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	if o.closed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := o.live(ctx); err != nil {
		return err
	}
	o.closing = true
	if err := o.apply(ctx, nil, nil, nil); err != nil {
		return err
	}
	o.closed = true
	o.leases = nil
	o.listeners = nil
	o.datagrams = nil
	b.underlayOwner = nil
	return nil
}

var _ interface{ Close() error } = (*WindowsUnderlayLease)(nil)
