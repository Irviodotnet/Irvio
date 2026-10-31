// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

type socketPermissionRule struct{ install, close func() error }

func (r socketPermissionRule) Install() error { return r.install() }
func (r socketPermissionRule) Close() error   { return r.close() }

func TestWindowsSocketPermissionPartialFailure(t *testing.T) {
	for _, stage := range []string{"wfp", "open", "install", "close"} {
		t.Run(stage, func(t *testing.T) {
			boom := errors.New("injected permission failure")
			failClose, wfpCloses, fwCloses := true, 0, 0
			m := &WindowsSocketPermissions{generation: 1, entries: make(map[int]*windowsSocketPermission)}
			m.grant = func(context.Context, networkstate.Generation, winroute.Binding, netip.AddrPort, uint32) (io.Closer, error) {
				g := datagramCloseFunc(func() error { wfpCloses++; return nil })
				if stage == "wfp" {
					return g, boom
				}
				return g, nil
			}
			m.open = func(int, winroute.Binding, netip.AddrPort, uint32) (windowsInboundRule, error) {
				if stage == "open" {
					return nil, boom
				}
				return socketPermissionRule{install: func() error {
					if stage == "install" {
						return boom
					}
					return nil
				}, close: func() error {
					fwCloses++
					if failClose {
						return boom
					}
					return nil
				}}, nil
			}
			b := winroute.Binding{LUID: 1, Source: netip.MustParseAddr("192.0.2.1")}
			local := netip.AddrPortFrom(b.Source, 51820)
			lease, err := m.Datagram(context.Background(), 1, b, local)
			if lease == nil || (stage != "close" && !errors.Is(err, boom)) || (stage == "close" && err != nil) {
				t.Fatal("partial ownership lost", err)
			}
			err = lease.Close()
			if wfpCloses != 1 {
				t.Fatal("WFP not withdrawn independently", wfpCloses)
			}
			if stage == "close" || stage == "install" {
				if !errors.Is(err, boom) || len(m.entries) != 1 {
					t.Fatal("failed firewall owner lost", err)
				}
				if _, err := m.Datagram(context.Background(), 1, b, local); !errors.Is(err, killswitch.ErrWindowsUnderlayBusy) {
					t.Fatal("failed cleanup did not fence acquisitions", err)
				}
			}
			failClose = false
			if err := lease.Close(); err != nil || len(m.entries) != 0 || wfpCloses != 1 {
				t.Fatal("retry cleanup", err, fwCloses, wfpCloses)
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWindowsSocketPermissionCapacityAndGeneration(t *testing.T) {
	m := &WindowsSocketPermissions{generation: 1, entries: make(map[int]*windowsSocketPermission), open: func(int, winroute.Binding, netip.AddrPort, uint32) (windowsInboundRule, error) {
		return socketPermissionRule{install: func() error { return nil }, close: func() error { return nil }}, nil
	}}
	b := winroute.Binding{LUID: 1, Source: netip.MustParseAddr("192.0.2.1")}
	local := netip.AddrPortFrom(b.Source, 51820)
	var leases []io.Closer
	for range 4 {
		l, err := m.Datagram(context.Background(), 1, b, local)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, l)
	}
	if _, err := m.Datagram(context.Background(), 1, b, local); err == nil {
		t.Fatal("UDP capacity unbounded")
	}
	for range 8 {
		if _, err := m.Listener(context.Background(), 1, b, local); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Listener(context.Background(), 1, b, local); err == nil {
		t.Fatal("TCP capacity unbounded")
	}
	failAdvance := true
	m.advance = func(context.Context, networkstate.Generation) error {
		if failAdvance {
			return errors.New("uncertain WFP advance")
		}
		return nil
	}
	if m.Advance(context.Background(), 2) == nil || len(m.entries) != 0 {
		t.Fatal("advance did not drain/fence")
	}
	if _, err := m.Datagram(context.Background(), 2, b, local); !errors.Is(err, killswitch.ErrWindowsUnderlayBusy) {
		t.Fatal(err)
	}
	failAdvance = false
	if err := m.Advance(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	for _, l := range leases {
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Datagram(context.Background(), 1, b, local); !errors.Is(err, killswitch.ErrStaleGeneration) {
		t.Fatal(err)
	}
	if _, err := m.Datagram(context.Background(), 2, b, local); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Datagram(context.Background(), 2, b, local); !errors.Is(err, killswitch.ErrWindowsUnderlayBusy) {
		t.Fatal("closed owner reused", err)
	}
}

func TestWindowsSocketPermissionRootOwnership(t *testing.T) {
	c := WindowsSocketPermissionsConfig{Directory: filepath.Join(t.TempDir(), "permissions"), Generation: 1, Mode: WindowsSocketsProtected}
	if _, err := OpenWindowsSocketPermissions(c); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("protected mode without WFP accepted", err)
	}
	c.Mode = WindowsSocketsHosting
	m, err := OpenWindowsSocketPermissions(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	if other, err := OpenWindowsSocketPermissions(c); err == nil {
		other.Close()
		t.Fatal("second runtime stole journals")
	}
	if err := RecoverWindowsSocketPermissions(c.Directory); err == nil {
		t.Fatal("recovery stole live runtime")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RecoverWindowsSocketPermissions(c.Directory); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSocketPermissionFactoryRetainsPort(t *testing.T) {
	failing := true
	m := &WindowsSocketPermissions{generation: 1, entries: make(map[int]*windowsSocketPermission), open: func(int, winroute.Binding, netip.AddrPort, uint32) (windowsInboundRule, error) {
		return socketPermissionRule{install: func() error { return nil }, close: func() error {
			if failing {
				return errors.New("firewall revoke uncertain")
			}
			return nil
		}}, nil
	}}
	sockets := &WindowsTransportSockets{}
	t.Cleanup(func() {
		failing = false
		if err := sockets.Close(); err != nil {
			t.Error(err)
		}
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	factory, err := sockets.Factory(m.Datagram)
	if err != nil {
		t.Fatal(err)
	}
	b, err := winroute.ForSource(netip.MustParseAddr("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	state := networkstate.State{Generation: 1, Snapshot: networkstate.Snapshot{Links: []networkstate.Link{{Index: int(b.Index), NativeID: b.LUID, Up: true, Addresses: []netip.Prefix{netip.PrefixFrom(b.Source, 32)}}}}}
	c := socketmux.DefaultConfig()
	c.IPv4Bind = b.Source
	c.IPv6Policy = socketmux.FamilyDisabled
	mux, lease, err := factory(context.Background(), state, c)
	if err != nil {
		t.Fatal(err)
	}
	local, _ := mux.LocalAddr(socketmux.IPv4)
	if lease.Close() == nil || len(m.entries) != 1 || len(sockets.entries) != 1 {
		t.Fatal("failed composed close lost ownership")
	}
	c.Port = int(local.Port())
	other, err := socketmux.New(context.Background(), c)
	if err == nil {
		other.Close()
		t.Fatal("port escaped uncertain firewall permission")
	}
	failing = false
	if err := lease.Close(); err != nil || len(m.entries) != 0 || len(sockets.entries) != 0 {
		t.Fatal("composed retry failed", err)
	}
	other, err = socketmux.New(context.Background(), c)
	if err != nil {
		t.Fatal("port not released after both permissions", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSocketPermissionRootCloseError(t *testing.T) {
	boom := errors.New("root close failure")
	calls := 0
	m := &WindowsSocketPermissions{root: datagramCloseFunc(func() error { calls++; return boom })}
	for range 2 {
		if !errors.Is(m.Close(), boom) {
			t.Fatal("root close failure hidden")
		}
	}
	if calls != 1 {
		t.Fatal("root closed twice")
	}
}
