// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/wfp"
)

func TestWindowsDatagramPolicyScopes(t *testing.T) {
	c := windowsFixture()
	e := WindowsDatagramSocket{Interface: 10, Local: netip.MustParseAddrPort("192.0.2.2:51820")}
	c.Network.DatagramSockets = []WindowsDatagramSocket{e}
	rules, err := compileWindowsPolicy(c.Owner, c.Network)
	if err != nil {
		t.Fatal(err)
	}
	connect, accept, deny, transport := 0, 0, 0, 0
	for _, r := range rules {
		if r.LocalPort != e.Local.Port() {
			continue
		}
		if r.Protocol != 17 || r.LocalInterface != e.Interface || r.LocalAddress != e.Local.Addr() {
			t.Fatal("unscoped UDP rule", r)
		}
		switch r.Layer {
		case wfp.ConnectV4:
			connect++
		case wfp.AcceptV4:
			if r.Action == wfp.Block {
				deny++
				if r.Priority != 390 || r.Application != "" || r.User != wfp.AnyUser {
					t.Fatal("foreign UDP socket not denied")
				}
				continue
			}
			accept++
		case wfp.TransportV4:
			transport++
			if r.Action != wfp.Permit {
				t.Fatal("missing UDP transport")
			}
			continue
		default:
			t.Fatal("unexpected datagram layer")
		}
		if r.Action != wfp.Permit || r.Priority != 400 || r.Application != c.Network.Application || r.User != wfp.SystemOrAdministrator {
			t.Fatal("UDP app/user scope absent")
		}
	}
	if connect != 1 || accept != 1 || deny != 1 || transport != 1 {
		t.Fatal("incomplete UDP policy", connect, accept, deny, transport)
	}
	cloned := cloneWindowsNetwork(c.Network)
	cloned.DatagramSockets[0].Interface = 200
	if c.Network.DatagramSockets[0] != e {
		t.Fatal("datagram scope aliased")
	}
	for _, bad := range []WindowsDatagramSocket{{Local: e.Local}, {Interface: 99, Local: e.Local}, {Interface: 10, Local: netip.MustParseAddrPort("0.0.0.0:51820")}, {Interface: 10, Local: netip.MustParseAddrPort("192.0.2.2:0")}, {Interface: 10, Local: netip.MustParseAddrPort("127.0.0.1:51820")}} {
		c.Network.DatagramSockets = []WindowsDatagramSocket{bad}
		if _, err := compileWindowsPolicy(c.Owner, c.Network); err == nil {
			t.Fatal("invalid socket grant accepted", bad)
		}
	}
	c.Network.DatagramSockets = []WindowsDatagramSocket{e, e}
	if _, err := compileWindowsPolicy(c.Owner, c.Network); err == nil {
		t.Fatal("duplicate socket accepted")
	}
}

func TestWindowsDatagramOwnerLifecycle(t *testing.T) {
	b, s, _, updater, owner, tcpScope := underlayFixture(t)
	ctx := context.Background()
	tcp, err := owner.Acquire(ctx, 1, tcpScope)
	if err != nil {
		t.Fatal(err)
	}
	e := WindowsDatagramSocket{Interface: 10, Local: netip.MustParseAddrPort("192.0.2.2:51820")}
	first, err := owner.AcquireDatagramSocket(ctx, 1, e)
	if err != nil {
		t.Fatal(err)
	}
	second, err := owner.AcquireDatagramSocket(ctx, 1, e)
	if err != nil || len(b.network.DatagramSockets) != 1 || len(owner.datagrams) != 2 {
		t.Fatal("duplicate ownership", err)
	}
	if err := first.Close(); err != nil || len(b.network.DatagramSockets) != 1 {
		t.Fatal("premature revoke", err)
	}
	if err := updater.UpdateTunnel(ctx, 101, nil); err != nil || len(b.network.DatagramSockets) != 1 {
		t.Fatal("lost UDP scope updating tunnel", err)
	}
	if err := updater.UpdateTunnel(ctx, e.Interface, nil); err == nil {
		t.Fatal("physical socket rebound to tunnel")
	}
	s.committedError = true
	if err := second.Close(); err == nil || len(owner.datagrams) != 1 {
		t.Fatal("uncertain revoke lost ownership")
	}
	if _, err := owner.AcquireDatagramSocket(ctx, 1, e); !errors.Is(err, ErrWindowsUnderlayBusy) {
		t.Fatal("uncertain policy accepted acquisition", err)
	}
	s.committedError = false
	if err := second.Close(); err != nil || len(owner.datagrams) != 0 || len(b.network.DatagramSockets) != 0 || len(b.network.Underlay) != 1 {
		t.Fatal("revoke retry", err)
	}
	stale, err := owner.AcquireDatagramSocket(ctx, 1, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Advance(ctx, 2); err != nil || len(b.network.DatagramSockets) != 0 || len(b.network.Underlay) != 0 {
		t.Fatal("generation retained grants", err)
	}
	if _, err := owner.AcquireDatagramSocket(ctx, 1, e); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("stale socket accepted", err)
	}
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tcp.Close(); err != nil {
		t.Fatal(err)
	}
	for index := range 4 {
		e.Local = netip.AddrPortFrom(e.Local.Addr(), uint16(51820+index))
		if _, err := owner.AcquireDatagramSocket(ctx, 2, e); err != nil {
			t.Fatal(err)
		}
	}
	e.Local = netip.AddrPortFrom(e.Local.Addr(), 51830)
	if _, err := owner.AcquireDatagramSocket(ctx, 2, e); err == nil || len(owner.datagrams) != 4 {
		t.Fatal("unbounded UDP socket reservations", err)
	}
	if err := owner.Close(); err != nil || len(b.network.DatagramSockets) != 0 || !s.state.Installed {
		t.Fatal("owner close removed protection or retained grants", err)
	}
}
