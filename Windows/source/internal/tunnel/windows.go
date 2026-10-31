// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build windows

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
)

type windowsRing interface {
	ReceivePacket() ([]byte, error)
	ReleaseReceivePacket([]byte)
	AllocateSendPacket(int) ([]byte, error)
	SendPacket([]byte)
	ReadWaitEvent() windows.Handle
	End()
}

type windowsAdapter interface {
	LUID() uint64
	Start() (windowsRing, error)
	Close() error
}

type nativeWindowsAdapter struct{ *wintun.Adapter }

func (a nativeWindowsAdapter) Start() (windowsRing, error) { return a.StartSession(0x800000) }

type windowsLinks interface {
	Configure(uint64, int) (int, error)
	Add(uint64, netip.Prefix) error
	Remove(uint64, netip.Prefix) error
	Ready(context.Context, uint64, []netip.Prefix) error
	AutomaticSource(uint64, netip.Prefix, bool) error
}

// Windows keeps one Wintun adapter and ring alive across generation-local AWG borrowers.
type Windows struct {
	adapter   windowsAdapter
	ring      windowsRing
	links     windowsLinks
	name      string
	luid      uint64
	ifindex   int
	mtu       int
	addresses []netip.Prefix
	dynamic   map[netip.Prefix]struct{}
	mu        sync.Mutex
	closed    bool
	borrower  *windowsBorrowed
	closeOnce sync.Once
	closeErr  error
}

var _ DeviceOwner = (*Windows)(nil)

// NewWindows configures only its owned adapter through native IP Helper APIs.
func NewWindows(config Config) (*Windows, error) {
	return newWindows(config, func(name string) (windowsAdapter, error) {
		interfaces, err := net.Interfaces()
		if err != nil {
			return nil, err
		}
		for _, iface := range interfaces {
			if strings.EqualFold(iface.Name, name) {
				return nil, fmt.Errorf("TUN name already belongs to an interface: %w", ErrInvalidConfig)
			}
		}
		a, err := wintun.CreateAdapter(name, "IRVIO", nil)
		if err != nil {
			return nil, err
		}
		row := windows.MibIfRow2{InterfaceLuid: a.LUID()}
		if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
			return nil, errors.Join(err, a.Close())
		}
		if windows.UTF16ToString(row.Alias[:]) != name {
			return nil, errors.Join(fmt.Errorf("Windows assigned an unexpected TUN name"), a.Close())
		}
		return nativeWindowsAdapter{a}, nil
	}, nativeWindowsLinks{})
}

func newWindows(config Config, create func(string) (windowsAdapter, error), links windowsLinks) (*Windows, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	adapter, err := create(config.Name)
	if err != nil {
		return nil, fmt.Errorf("create Windows TUN: %w", err)
	}
	owner := &Windows{adapter: adapter, links: links, name: config.Name, mtu: config.MTU,
		luid: adapter.LUID(), addresses: append([]netip.Prefix(nil), config.Addresses...), dynamic: make(map[netip.Prefix]struct{})}
	fail := func(err error) (*Windows, error) { return nil, errors.Join(err, owner.Close()) }
	if owner.luid == 0 {
		return fail(fmt.Errorf("Windows TUN has no native identity"))
	}
	ring, err := adapter.Start()
	if err != nil {
		return fail(fmt.Errorf("start Windows TUN ring: %w", err))
	}
	owner.ring = ring
	owner.ifindex, err = links.Configure(owner.luid, config.MTU)
	if err != nil {
		return fail(err)
	}
	for _, prefix := range owner.addresses {
		if err := links.Add(owner.luid, prefix); err != nil {
			return fail(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := links.Ready(ctx, owner.luid, owner.addresses); err != nil {
		return fail(err)
	}
	return owner, nil
}

func (w *Windows) Name() string {
	if w == nil {
		return ""
	}
	return w.name
}
func (w *Windows) IfIndex() int {
	if w == nil {
		return 0
	}
	return w.ifindex
}
func (w *Windows) LUID() uint64 {
	if w == nil {
		return 0
	}
	return w.luid
}
func (w *Windows) Addresses() []netip.Prefix {
	if w == nil {
		return nil
	}
	return append([]netip.Prefix(nil), w.addresses...)
}

// Borrow provides one cancellable reader without transferring adapter or ring ownership.
func (w *Windows) Borrow() (awgtun.Device, error) {
	if w == nil {
		return nil, ErrClosed
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, ErrClosed
	}
	if w.borrower != nil {
		return nil, ErrAlreadyBorrowed
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("create TUN reader cancellation: %w", err)
	}
	b := &windowsBorrowed{owner: w, stop: stop, events: make(chan awgtun.Event, 1)}
	b.events <- awgtun.EventUp
	w.borrower = b
	return b, nil
}

func (w *Windows) release(b *windowsBorrowed) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.borrower == b {
		w.borrower = nil
	}
}

// AddAddresses installs dual-role host addresses and rolls back only changes owned by this call.
func (w *Windows) AddAddresses(prefixes []netip.Prefix) error {
	if w == nil || len(prefixes) == 0 {
		return ErrInvalidConfig
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	for _, p := range prefixes {
		if !validDynamicPrefix(p) {
			return ErrInvalidConfig
		}
		for _, own := range w.addresses {
			if own.Addr() == p.Addr() {
				return ErrInvalidConfig
			}
		}
	}
	var added []netip.Prefix
	rollback := func(cause error) error {
		for i := len(added) - 1; i >= 0; i-- {
			if err := w.links.Remove(w.luid, added[i]); err != nil {
				cause = errors.Join(cause, err)
			} else {
				delete(w.dynamic, added[i])
			}
		}
		return cause
	}
	for _, p := range prefixes {
		if _, ok := w.dynamic[p]; ok {
			continue
		}
		if err := w.links.Add(w.luid, p); err != nil {
			return rollback(err)
		}
		w.dynamic[p] = struct{}{}
		added = append(added, p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.links.Ready(ctx, w.luid, prefixes); err != nil {
		return rollback(err)
	}
	return nil
}

func (w *Windows) RemoveAddresses(prefixes []netip.Prefix) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	var result error
	for _, p := range prefixes {
		if _, ok := w.dynamic[p]; !ok {
			continue
		}
		if err := w.links.Remove(w.luid, p); err != nil {
			result = errors.Join(result, err)
		} else {
			delete(w.dynamic, p)
		}
	}
	return result
}

// SetAutomaticSource changes source selection only for an exact address owned by this adapter.
func (w *Windows) SetAutomaticSource(prefix netip.Prefix, enabled bool) error {
	if w == nil {
		return ErrInvalidConfig
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	_, owned := w.dynamic[prefix]
	for _, p := range w.addresses {
		owned = owned || p == prefix
	}
	if !owned {
		return ErrInvalidConfig
	}
	return w.links.AutomaticSource(w.luid, prefix, enabled)
}

// Close first joins all borrowed I/O, then ends the ring and removes the owned adapter.
func (w *Windows) Close() error {
	if w == nil {
		return nil
	}
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		b := w.borrower
		w.mu.Unlock()
		if b != nil {
			w.closeErr = b.Close()
		}
		if w.ring != nil {
			w.ring.End()
		}
		w.closeErr = errors.Join(w.closeErr, w.adapter.Close())
	})
	return w.closeErr
}
