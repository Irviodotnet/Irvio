// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package controlsocket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/multiformats/go-multiaddr"
)

type closeGrant struct {
	calls int
	err   error
}

func (g *closeGrant) Close() error { g.calls++; return g.err }

func TestWindowsOwnedTCPConnectionLifecycle(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	binding, err := winroute.ForSource(netip.MustParseAddr("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	grant := &closeGrant{}
	target := listener.Addr().(*net.TCPAddr).AddrPort()
	factory, err := DialerForOwnedBindings(func(_ context.Context, addr netip.AddrPort) (winroute.Binding, io.Closer, error) {
		if addr != target {
			t.Fatal("authorization endpoint changed")
		}
		return binding, grant, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dialer, err := factory(multiaddr.StringCast(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", target.Port())))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp4", target.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(3 * time.Second))
	owned, ok := conn.(*ownedTCPConn)
	if !ok || grant.calls != 0 {
		t.Fatal("live connection lost ownership")
	}
	raw, err := owned.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	index, err := winroute.SocketInterface(raw, true)
	if err != nil || index != binding.Index {
		t.Fatal("binding lost", err)
	}
	if err := owned.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	if n, err := peer.Read(data[:]); n != 0 || err != io.EOF {
		t.Fatal("half-close failed", err)
	}
	if grant.calls != 0 {
		t.Fatal("half-close revoked receive lane")
	}
	failure := errors.New("injected grant cleanup failure")
	grant.err = failure
	if err := conn.Close(); !errors.Is(err, failure) || grant.calls != 1 {
		t.Fatal("cleanup failure hidden", err)
	}
	if _, err := conn.Write([]byte{1}); err == nil {
		t.Fatal("socket remained open on revoke failure")
	}
	grant.err = nil
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if grant.calls != 2 {
		t.Fatal("cleanup retry not serialized", grant.calls)
	}
}

func TestWindowsOwnedTCPFailedAcquisitionAndDial(t *testing.T) {
	if _, err := DialerForOwnedBindings(nil); err == nil {
		t.Fatal("nil owner resolver")
	}
	for _, scenario := range []string{"resolver-error", "family-mismatch", "invalid-interface", "no-owner"} {
		t.Run(scenario, func(t *testing.T) {
			injected := errors.New("resolver failed after acquiring grant")
			cleanup := errors.New("cleanup failed")
			grant := &closeGrant{err: cleanup}
			factory, _ := DialerForOwnedBindings(func(context.Context, netip.AddrPort) (winroute.Binding, io.Closer, error) {
				switch scenario {
				case "resolver-error":
					return winroute.Binding{}, grant, injected
				case "family-mismatch":
					return winroute.Binding{Source: netip.MustParseAddr("::1")}, grant, nil
				case "invalid-interface":
					return winroute.Binding{Source: netip.MustParseAddr("127.0.0.1")}, grant, nil
				default:
					return winroute.Binding{}, nil, nil
				}
			})
			dialer, err := factory(multiaddr.StringCast("/ip4/127.0.0.1/tcp/1"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := dialer.DialContext(ctx, "tcp4", "127.0.0.1:1")
			if err == nil || conn != nil {
				t.Fatal("unauthorized dial succeeded")
			}
			if scenario != "no-owner" && (grant.calls != 1 || !errors.Is(err, cleanup)) {
				t.Fatal("grant cleanup lost", err)
			}
			if scenario == "resolver-error" && !errors.Is(err, injected) {
				t.Fatal("cause lost", err)
			}
		})
	}
}
