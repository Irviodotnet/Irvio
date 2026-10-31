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

func TestWindowsControlListenerPolicy(t *testing.T) {
	c := windowsFixture()
	e := WindowsControlListener{Interface: 10, Local: netip.MustParseAddrPort("192.0.2.2:4000")}
	c.Network.ControlListeners = []WindowsControlListener{e}
	rules, err := compileWindowsPolicy(c.Owner, c.Network)
	if err != nil {
		t.Fatal(err)
	}
	accept, deny, reply := 0, 0, 0
	for _, r := range rules {
		if r.LocalPort != 4000 {
			continue
		}
		if r.LocalInterface != e.Interface || r.LocalAddress != e.Local.Addr() || r.Protocol != 6 {
			t.Fatal("broad listener rule")
		}
		switch r.Layer {
		case wfp.ConnectV4, wfp.ConnectV6:
			t.Fatal("listener allowed initiating WAN connections")
		case wfp.AcceptV4:
			if r.Action == wfp.Block {
				deny++
				if r.Priority != 390 || r.Application != "" || r.User != wfp.AnyUser {
					t.Fatal("foreign listener not denied")
				}
			} else {
				accept++
				if r.Priority != 400 || r.Application != c.Network.Application || r.User != wfp.SystemOrAdministrator {
					t.Fatal("listener owner authorization missing")
				}
			}
		case wfp.TransportV4:
			reply++
			if r.Action != wfp.Permit {
				t.Fatal("missing replies")
			}
		default:
			t.Fatal("unexpected listener layer")
		}
	}
	if accept != 1 || deny != 1 || reply != 1 {
		t.Fatal("listener coverage", accept, deny, reply)
	}
	for _, bad := range []WindowsControlListener{{Interface: 0, Local: e.Local}, {Interface: 99, Local: e.Local}, {Interface: 10, Local: netip.MustParseAddrPort("0.0.0.0:4000")}, {Interface: 10, Local: netip.MustParseAddrPort("192.0.2.2:0")}} {
		c.Network.ControlListeners = []WindowsControlListener{bad}
		if _, err := compileWindowsPolicy(c.Owner, c.Network); err == nil {
			t.Fatal("unsafe listener grant")
		}
	}
}

func TestWindowsControlListenerGenerationAndUncertainClose(t *testing.T) {
	b, s, _, _, owner, dialGrant := underlayFixture(t)
	ctx := context.Background()
	dial, err := owner.Acquire(ctx, 1, dialGrant)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := owner.AcquireListener(ctx, 1, WindowsControlListener{Interface: 10, Local: netip.MustParseAddrPort("192.0.2.2:4000")})
	if err != nil {
		t.Fatal(err)
	}
	s.committedError = true
	if err := listener.Close(); err == nil || len(owner.listeners) != 1 {
		t.Fatal("failed revoke lost listener")
	}
	s.committedError = false
	if err := listener.Close(); err != nil || len(b.network.ControlListeners) != 0 || len(b.network.Underlay) != 1 {
		t.Fatal("listener retry changed dial lane", err)
	}
	listener, err = owner.AcquireListener(ctx, 1, WindowsControlListener{Interface: 10, Local: netip.MustParseAddrPort("192.0.2.2:4000")})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Advance(ctx, 2); err != nil || len(b.network.ControlListeners) != 0 || len(b.network.Underlay) != 0 {
		t.Fatal("generation retained listeners", err)
	}
	if _, err := owner.AcquireListener(ctx, 1, WindowsControlListener{Interface: 10, Local: netip.MustParseAddrPort("192.0.2.2:4000")}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dial.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}
