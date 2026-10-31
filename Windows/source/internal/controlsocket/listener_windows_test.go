// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package controlsocket

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/multiformats/go-multiaddr"
	"golang.org/x/sys/windows"
)

func TestWindowsListenerExclusiveAndBoundAcceptedSocket(t *testing.T) {
	for _, source := range []string{"127.0.0.1", "::1"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			binding, err := winroute.ForSource(netip.MustParseAddr(source))
			if err != nil {
				t.Fatal(err)
			}
			owner := &WindowsListeners{}
			defer owner.Close()
			grant := &closeGrant{}
			listener, err := owner.Listen(ctx, binding, 0, func(_ context.Context, b winroute.Binding, e netip.AddrPort) (io.Closer, error) {
				if b != binding || e.Addr() != binding.Source || e.Port() == 0 {
					t.Fatal("bad listener authorization")
				}
				return grant, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			network := "tcp6"
			if binding.Source.Is4() {
				network = "tcp4"
			}
			lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
				var err error
				if e := raw.Control(func(fd uintptr) {
					err = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
				}); e != nil {
					return e
				}
				return err
			}}
			if stolen, err := lc.Listen(ctx, network, listener.Addr().String()); err == nil {
				stolen.Close()
				t.Fatal("exclusive listener port stolen")
			}
			client, err := net.DialTimeout(network, listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			accepted, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer accepted.Close()
			raw, err := accepted.(*net.TCPConn).SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			index, err := winroute.SocketInterface(raw, binding.Source.Is4())
			if err != nil || index != binding.Index {
				t.Fatal("accepted reply not pinned", index, err)
			}
			errClosed := make(chan error, 1)
			go func() { _, err := listener.Accept(); errClosed <- err }()
			grant.err = errors.New("revoke failed")
			if err := listener.Close(); err == nil {
				t.Fatal("failed revoke hidden")
			}
			select {
			case err := <-errClosed:
				if !errors.Is(err, net.ErrClosed) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("Accept not interrupted")
			}
			if stolen, err := lc.Listen(ctx, network, listener.Addr().String()); err == nil {
				stolen.Close()
				t.Fatal("port released before revoke")
			}
			grant.err = nil
			if err := owner.Close(); err != nil || len(owner.listeners) != 0 || grant.calls != 2 {
				t.Fatal("cleanup retry", err)
			}
		})
	}
}

func TestWindowsListenerConstructorKeepsFailedCleanup(t *testing.T) {
	ctx := context.Background()
	binding, err := winroute.ForSource(netip.MustParseAddr("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	owner := &WindowsListeners{}
	defer owner.Close()
	grant := &closeGrant{err: errors.New("revoke failed")}
	var endpoint netip.AddrPort
	listener, err := owner.Listen(ctx, binding, 0, func(_ context.Context, _ winroute.Binding, e netip.AddrPort) (io.Closer, error) {
		endpoint = e
		return grant, errors.New("partly installed authorization")
	})
	if err == nil || listener != nil || len(owner.listeners) != 1 {
		t.Fatal("failed constructor lost ownership")
	}
	if other, err := net.Listen("tcp4", endpoint.String()); err == nil {
		other.Close()
		t.Fatal("uncertain permission port reusable")
	}
	grant.err = nil
	if err := owner.Close(); err != nil || len(owner.listeners) != 0 {
		t.Fatal("failed constructor cleanup", err)
	}
}

func TestWindowsListenerFactoryRejectsShortcuts(t *testing.T) {
	owner := &WindowsListeners{}
	defer owner.Close()
	resolve := func(context.Context, bool) (winroute.Binding, error) {
		return winroute.Binding{Source: netip.MustParseAddr("127.0.0.1")}, nil
	}
	authorize := func(context.Context, winroute.Binding, netip.AddrPort) (io.Closer, error) {
		t.Fatal("invalid listener request reached authorization")
		return nil, nil
	}
	if _, err := owner.Factory(nil, resolve, authorize); err == nil {
		t.Fatal("missing context accepted")
	}
	factory, err := owner.Factory(context.Background(), resolve, authorize)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"/dns4/example.net/tcp/4000", "/ip4/127.0.0.1/udp/4000", "/ip4/127.0.0.1/tcp/4000/ws", "/ip4/127.0.0.2/tcp/4000", "/ip6/::/tcp/4000", "/ip6/::ffff:127.0.0.1/tcp/4000", "/ip4/224.0.0.1/tcp/4000"} {
		address, err := multiaddr.NewMultiaddr(text)
		if err != nil {
			t.Fatal(err)
		}
		if listener, err := factory(address); err == nil || listener != nil {
			t.Fatalf("invalid listener request accepted: %s", text)
		}
	}
	if len(owner.listeners) != 0 {
		t.Fatal("invalid request retained a listener")
	}
}
