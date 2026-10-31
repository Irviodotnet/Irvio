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
	"testing"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

type datagramCloseFunc func() error

func (f datagramCloseFunc) Close() error { return f() }

func TestWindowsTransportSocketRetainsFailedReservation(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "constructor"}[partial], func(t *testing.T) {
			ctx := context.Background()
			owner := &WindowsTransportSockets{}
			failing := true
			t.Cleanup(func() {
				failing = false
				if err := owner.Close(); err != nil {
					t.Error("socket cleanup", err)
				}
			})
			var local netip.AddrPort
			factory, err := owner.Factory(func(_ context.Context, g networkstate.Generation, b winroute.Binding, e netip.AddrPort) (io.Closer, error) {
				local = e
				if g != 7 || b.Source != e.Addr() || b.LUID == 0 {
					t.Fatal("authorization not tied to generation/socket")
				}
				grant := datagramCloseFunc(func() error {
					if failing {
						return errors.New("injected revoke failure")
					}
					return nil
				})
				if partial {
					return grant, errors.New("uncertain permission commit")
				}
				return grant, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			c := socketmux.DefaultConfig()
			c.IPv4Bind = netip.MustParseAddr("127.0.0.1")
			c.IPv6Policy = socketmux.FamilyDisabled
			binding, err := winroute.ForSource(c.IPv4Bind)
			if err != nil {
				t.Fatal(err)
			}
			state := networkstate.State{Generation: 7, Snapshot: networkstate.Snapshot{Links: []networkstate.Link{{Index: int(binding.Index), NativeID: binding.LUID, Up: true, Addresses: []netip.Prefix{netip.PrefixFrom(binding.Source, 32)}}}}}
			m, lease, err := factory(ctx, state, c)
			if partial {
				if err == nil || m != nil || lease != nil {
					t.Fatal("partial constructor accepted")
				}
			} else {
				if err != nil || m == nil || lease == nil {
					t.Fatal(err)
				}
				if err := lease.Close(); err == nil {
					t.Fatal("failed permission cleanup accepted")
				}
			}
			if len(owner.entries) != 1 {
				t.Fatal("uncertain socket owner lost")
			}
			c.Port = int(local.Port())
			other, err := socketmux.New(ctx, c)
			if err == nil {
				other.Close()
				t.Fatal("reserved port reused before permission withdrawal")
			}
			if err := owner.Close(); err == nil {
				t.Fatal("owner cleanup failure hidden")
			}
			if _, _, err := factory(ctx, state, c); err == nil {
				t.Fatal("closing owner created a generation")
			}
			failing = false
			if err := owner.Close(); err != nil || len(owner.entries) != 0 {
				t.Fatal("cleanup retry", err)
			}
			other, err = socketmux.New(ctx, c)
			if err != nil {
				t.Fatal("reservation not released", err)
			}
			if err := other.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWindowsTransportSocketRejectsStaleInterface(t *testing.T) {
	owner := &WindowsTransportSockets{}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	authorized := false
	factory, err := owner.Factory(func(context.Context, networkstate.Generation, winroute.Binding, netip.AddrPort) (io.Closer, error) {
		authorized = true
		return datagramCloseFunc(func() error { return nil }), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c := socketmux.DefaultConfig()
	c.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	c.IPv6Policy = socketmux.FamilyDisabled
	binding, err := winroute.ForSource(c.IPv4Bind)
	if err != nil {
		t.Fatal(err)
	}
	state := networkstate.State{Generation: 7, Snapshot: networkstate.Snapshot{Links: []networkstate.Link{{Index: int(binding.Index), NativeID: binding.LUID + 1, Up: true, Addresses: []netip.Prefix{netip.PrefixFrom(binding.Source, 32)}}}}}
	m, lease, err := factory(context.Background(), state, c)
	if err == nil || m != nil || lease != nil || authorized || len(owner.entries) != 0 {
		t.Fatal("stale generation authorized or retained socket", err)
	}
}
