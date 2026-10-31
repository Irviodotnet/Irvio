// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/Irviodotnet/Irvio/internal/winfirewall"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

type WindowsSocketMode uint8

const (
	WindowsSocketsProtected WindowsSocketMode = iota + 1
	WindowsSocketsHosting
)

type WindowsSocketPermissionsConfig struct {
	Directory  string
	Generation networkstate.Generation
	Mode       WindowsSocketMode
	Underlay   *killswitch.WindowsUnderlayOwner
}

type windowsInboundRule interface {
	Install() error
	Close() error
}

// WindowsSocketPermissions composes inbound firewall and protected WFP grants without owning sockets.
type WindowsSocketPermissions struct {
	mu                sync.Mutex
	generation        networkstate.Generation
	closing, draining bool
	closeErr          error
	root              io.Closer
	entries           map[int]*windowsSocketPermission
	open              func(int, winroute.Binding, netip.AddrPort, uint32) (windowsInboundRule, error)
	grant             func(context.Context, networkstate.Generation, winroute.Binding, netip.AddrPort, uint32) (io.Closer, error)
	advance           func(context.Context, networkstate.Generation) error
}

type windowsSocketPermission struct {
	parent         *WindowsSocketPermissions
	slot           int
	firewall       windowsInboundRule
	wfp            io.Closer
	failed, closed bool
}

type windowsSocketJournalRoot struct {
	directory *winfile.Directory
	lock      *os.File
}

func (r *windowsSocketJournalRoot) Close() error {
	return errors.Join(winfile.Unlock(r.lock), r.lock.Close(), r.directory.Close())
}

func socketPermissionSlot(directory string, slot int) string {
	return filepath.Join(directory, fmt.Sprintf("socket-%02d", slot))
}

func openSocketPermissionRoot(directory string) (_ *windowsSocketJournalRoot, result error) {
	d, err := winfile.OpenDirectory(directory, true)
	if err != nil {
		return nil, err
	}
	f, err := d.OpenFile("socket-permissions.lock", true, true)
	if err != nil {
		return nil, errors.Join(err, d.Close())
	}
	if err := winfile.Lock(f); err != nil {
		return nil, errors.Join(err, f.Close(), d.Close())
	}
	r := &windowsSocketJournalRoot{directory: d, lock: f}
	for slot := 0; slot < 12; slot++ {
		if err := winfirewall.RecoverControl(socketPermissionSlot(directory, slot)); err != nil {
			return nil, errors.Join(err, r.Close())
		}
	}
	return r, nil
}

// RecoverWindowsSocketPermissions refuses a live runtime and removes only its bounded recorded inbound scopes.
func RecoverWindowsSocketPermissions(directory string) error {
	r, err := openSocketPermissionRoot(directory)
	if err != nil {
		return err
	}
	return r.Close()
}

func OpenWindowsSocketPermissions(c WindowsSocketPermissionsConfig) (*WindowsSocketPermissions, error) {
	if c.Directory == "" || c.Generation == 0 || (c.Mode != WindowsSocketsProtected && c.Mode != WindowsSocketsHosting) ||
		(c.Mode == WindowsSocketsProtected) != (c.Underlay != nil) {
		return nil, ErrInvalidConfig
	}
	root, err := openSocketPermissionRoot(c.Directory)
	if err != nil {
		return nil, err
	}
	m := &WindowsSocketPermissions{generation: c.Generation, root: root, entries: make(map[int]*windowsSocketPermission)}
	m.open = func(slot int, b winroute.Binding, local netip.AddrPort, protocol uint32) (windowsInboundRule, error) {
		return winfirewall.OpenOwnedInbound(socketPermissionSlot(c.Directory, slot), b.LUID, local, protocol)
	}
	if c.Mode == WindowsSocketsProtected {
		m.grant = func(ctx context.Context, generation networkstate.Generation, b winroute.Binding, local netip.AddrPort, protocol uint32) (io.Closer, error) {
			if protocol == 17 {
				return c.Underlay.AcquireDatagramSocket(ctx, uint64(generation), killswitch.WindowsDatagramSocket{Interface: b.LUID, Local: local})
			}
			return c.Underlay.AcquireListener(ctx, uint64(generation), killswitch.WindowsControlListener{Interface: b.LUID, Local: local})
		}
		m.advance = func(ctx context.Context, generation networkstate.Generation) error {
			return c.Underlay.Advance(ctx, uint64(generation))
		}
	}
	return m, nil
}

func (m *WindowsSocketPermissions) Datagram(ctx context.Context, generation networkstate.Generation, binding winroute.Binding, local netip.AddrPort) (io.Closer, error) {
	return m.acquire(ctx, generation, binding, local, 17)
}

func (m *WindowsSocketPermissions) Listener(ctx context.Context, generation networkstate.Generation, binding winroute.Binding, local netip.AddrPort) (io.Closer, error) {
	return m.acquire(ctx, generation, binding, local, 6)
}

// acquire returns partial ownership on failure so the socket caller cannot release its reservation early.
func (m *WindowsSocketPermissions) acquire(ctx context.Context, generation networkstate.Generation, binding winroute.Binding, local netip.AddrPort, protocol uint32) (io.Closer, error) {
	if m == nil || ctx == nil || generation == 0 || binding.LUID == 0 || binding.Source != local.Addr() || local.Port() == 0 {
		return nil, ErrInvalidConfig
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.open == nil || m.entries == nil {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if generation != m.generation {
		return nil, killswitch.ErrStaleGeneration
	}
	if m.closing || m.draining {
		return nil, killswitch.ErrWindowsUnderlayBusy
	}
	for _, entry := range m.entries {
		if entry.failed {
			return nil, killswitch.ErrWindowsUnderlayBusy
		}
	}
	start, end := 0, 4
	if protocol == 6 {
		start, end = 4, 12
	}
	slot := start
	for slot < end && m.entries[slot] != nil {
		slot++
	}
	if slot == end {
		return nil, fmt.Errorf("Windows socket permission capacity exhausted")
	}
	l := &windowsSocketPermission{parent: m, slot: slot}
	m.entries[slot] = l
	fail := func(err error) (io.Closer, error) { l.failed = true; return l, err }
	if m.grant != nil {
		var err error
		l.wfp, err = m.grant(ctx, generation, binding, local, protocol)
		if err != nil {
			return fail(err)
		}
		if l.wfp == nil {
			return fail(fmt.Errorf("Windows WFP socket owner missing"))
		}
	}
	var err error
	l.firewall, err = m.open(slot, binding, local, protocol)
	if err != nil {
		return fail(err)
	}
	if l.firewall == nil {
		return fail(fmt.Errorf("Windows inbound socket owner missing"))
	}
	if err := l.firewall.Install(); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return l, nil
}

func (l *windowsSocketPermission) Close() error {
	l.parent.mu.Lock()
	defer l.parent.mu.Unlock()
	return l.closeLocked()
}

func (l *windowsSocketPermission) closeLocked() error {
	if l.closed {
		return nil
	}
	var failure error
	if l.firewall != nil {
		if err := l.firewall.Close(); err != nil {
			failure = errors.Join(failure, err)
		} else {
			l.firewall = nil
		}
	}
	if l.wfp != nil {
		if err := l.wfp.Close(); err != nil {
			failure = errors.Join(failure, err)
		} else {
			l.wfp = nil
		}
	}
	if failure != nil {
		l.failed = true
		return failure
	}
	l.closed = true
	delete(l.parent.entries, l.slot)
	return nil
}

// Advance drains predecessor permissions before advancing the shared WFP owner; socket reservations stay with callers.
func (m *WindowsSocketPermissions) Advance(ctx context.Context, generation networkstate.Generation) error {
	if m == nil || ctx == nil || generation == 0 {
		return ErrInvalidConfig
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.open == nil || m.entries == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.closing || generation < m.generation {
		return killswitch.ErrStaleGeneration
	}
	if generation == m.generation && !m.draining {
		return nil
	}
	m.generation, m.draining = generation, true
	var failure error
	for _, entry := range m.entries {
		failure = errors.Join(failure, entry.closeLocked())
	}
	if failure != nil {
		return failure
	}
	if m.advance != nil {
		if err := m.advance(ctx, generation); err != nil {
			return err
		}
	}
	m.draining = false
	return nil
}

// Close drains both native permission owners before releasing crash-recovery ownership; it never removes ProtectedMode.
func (m *WindowsSocketPermissions) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closing = true
	var failure error
	for _, entry := range m.entries {
		failure = errors.Join(failure, entry.closeLocked())
	}
	if failure != nil {
		return failure
	}
	if m.root != nil {
		m.closeErr = m.root.Close()
		m.root = nil
	}
	return m.closeErr
}
