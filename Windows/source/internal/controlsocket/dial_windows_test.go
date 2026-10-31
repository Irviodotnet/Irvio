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
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/multiformats/go-multiaddr"
)

func TestWindowsControlTCPBinding(t *testing.T) {
	for _, source := range []string{"127.0.0.1", "::1"} {
		t.Run(source, func(t *testing.T) {
			ip := netip.MustParseAddr(source)
			network, family := "tcp6", "ip6"
			if ip.Is4() {
				network, family = "tcp4", "ip4"
			}
			listener, err := net.Listen(network, net.JoinHostPort(source, "0"))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				for range 2 {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					conn.SetDeadline(time.Now().Add(3 * time.Second))
					io.Copy(conn, conn)
					conn.Close()
				}
			}()
			defer func() { listener.Close(); <-done }()
			binding, err := winroute.ForSource(ip)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			factory, err := DialerForBindings(func(_ context.Context, target netip.Addr) (winroute.Binding, error) {
				calls.Add(1)
				if target != ip {
					return winroute.Binding{}, errors.New("unexpected target")
				}
				return binding, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			address := multiaddr.StringCast(fmt.Sprintf("/%s/%s/tcp/%d", family, source, port))
			dialer, err := factory(address)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				conn, err := dialer.DialContext(ctx, network, listener.Addr().String())
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				raw, err := conn.(*net.TCPConn).SyscallConn()
				if err != nil {
					conn.Close()
					t.Fatal(err)
				}
				index, err := winroute.SocketInterface(raw, ip.Is4())
				if err != nil || index != binding.Index {
					conn.Close()
					t.Fatalf("native binding: %d %v", index, err)
				}
				if _, err := conn.Write([]byte("irvio")); err != nil {
					conn.Close()
					t.Fatal(err)
				}
				var reply [5]byte
				_, err = io.ReadFull(conn, reply[:])
				conn.Close()
				if err != nil || string(reply[:]) != "irvio" {
					t.Fatalf("echo failed: %q %v", reply, err)
				}
			}
			if calls.Load() != 2 {
				t.Fatal("binding was not resolved for every dial")
			}
		})
	}
}

func TestWindowsControlRejectsUnresolvedOrUnsupportedPolicy(t *testing.T) {
	if _, err := DialerForMark(0); !errors.Is(err, ErrInvalidMark) {
		t.Fatal(err)
	}
	if _, err := DialerForMark(0x48594452); err == nil {
		t.Fatal("mark silently ignored")
	}
	if _, err := DialerForBindings(nil); err == nil {
		t.Fatal("nil resolver accepted")
	}
	factory, _ := DialerForBindings(func(context.Context, netip.Addr) (winroute.Binding, error) {
		t.Fatal("resolver unexpectedly called")
		return winroute.Binding{}, nil
	})
	for _, value := range []string{"/dns4/localhost/tcp/80", "/ip4/127.0.0.1/udp/80", "/ip4/0.0.0.0/tcp/80", "/ip4/127.0.0.1/tcp/0", "/ip4/224.0.0.1/tcp/80", "/ip6/::ffff:127.0.0.1/tcp/80"} {
		if _, err := factory(multiaddr.StringCast(value)); err == nil {
			t.Fatalf("unsupported address accepted: %s", value)
		}
	}
	if _, err := factory(nil); err == nil {
		t.Fatal("empty address accepted")
	}
	dialer, err := factory(multiaddr.StringCast("/ip4/127.0.0.1/tcp/80"))
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"127.0.0.1:81", "localhost:80", "[::1]:80"} {
		if _, err := dialer.DialContext(context.Background(), "tcp4", address); err == nil {
			t.Fatal("endpoint substitution accepted")
		}
	}
	if _, err := dialer.DialContext(context.Background(), "udp4", "127.0.0.1:80"); err == nil {
		t.Fatal("protocol substitution accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dialer.DialContext(ctx, "tcp4", "127.0.0.1:80"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWindowsControlNeverFallsBackOnBindingFailure(t *testing.T) {
	injected := errors.New("generation unavailable")
	factory, _ := DialerForBindings(func(context.Context, netip.Addr) (winroute.Binding, error) { return winroute.Binding{}, injected })
	dialer, err := factory(multiaddr.StringCast("/ip4/127.0.0.1/tcp/80"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dialer.DialContext(context.Background(), "tcp4", "127.0.0.1:80"); !errors.Is(err, injected) {
		t.Fatalf("binding failure hidden: %v", err)
	}
}
