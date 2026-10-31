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
	"syscall"
	"time"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"golang.org/x/sys/windows"
)

// ListenerAuthorizer grants replies to an already exclusive source/interface/port.
type ListenerAuthorizer func(context.Context, winroute.Binding, netip.AddrPort) (io.Closer, error)

// Factory substitutes only wildcard requests with the runtime-selected interface source.
func (o *WindowsListeners) Factory(ctx context.Context, resolve func(context.Context, bool) (winroute.Binding, error), authorize ListenerAuthorizer) (ListenerFactory, error) {
	if o == nil || ctx == nil || resolve == nil || authorize == nil {
		return nil, fmt.Errorf("listener dependencies required")
	}
	return func(addr multiaddr.Multiaddr) (manet.Listener, error) {
		if len(addr) != 2 || (addr[0].Protocol().Code != multiaddr.P_IP4 && addr[0].Protocol().Code != multiaddr.P_IP6) || addr[1].Protocol().Code != multiaddr.P_TCP {
			return nil, fmt.Errorf("control listener requires direct IP/TCP")
		}
		_, target, err := manet.DialArgs(addr)
		if err != nil {
			return nil, err
		}
		endpoint, err := netip.ParseAddrPort(target)
		if err != nil || endpoint.Addr().Is4In6() || endpoint.Addr().IsMulticast() || endpoint.Addr().Zone() != "" {
			return nil, fmt.Errorf("invalid control listen endpoint")
		}
		binding, err := resolve(ctx, endpoint.Addr().Is4())
		if err != nil {
			return nil, err
		}
		if binding.Source.Is4() != endpoint.Addr().Is4() || !endpoint.Addr().IsUnspecified() && endpoint.Addr() != binding.Source {
			return nil, fmt.Errorf("control listen source differs from resolved interface")
		}
		listener, err := o.Listen(ctx, binding, endpoint.Port(), authorize)
		if err != nil {
			return nil, err
		}
		wrapped, err := manet.WrapNetListener(listener)
		if err != nil {
			return nil, errors.Join(err, listener.Close())
		}
		return wrapped, nil
	}, nil
}

// WindowsListeners retains reservations when a partial constructor or cleanup fails.
type WindowsListeners struct {
	mu        sync.Mutex
	closing   bool
	listeners map[*WindowsListener]struct{}
}

type WindowsListener struct {
	parent          *WindowsListeners
	socket          *net.TCPListener
	binding         winroute.Binding
	mu              sync.Mutex
	grant           io.Closer
	closing, closed bool
}

func (o *WindowsListeners) Listen(ctx context.Context, binding winroute.Binding, port uint16, authorize ListenerAuthorizer) (*WindowsListener, error) {
	if o == nil || ctx == nil || authorize == nil {
		return nil, fmt.Errorf("listener owner, context and authorization required")
	}
	o.mu.Lock()
	if o.closing {
		o.mu.Unlock()
		return nil, net.ErrClosed
	}
	if len(o.listeners) >= 8 {
		o.mu.Unlock()
		return nil, fmt.Errorf("control listener capacity exhausted")
	}
	network := "tcp6"
	if binding.Source.Is4() {
		network = "tcp4"
	}
	lc := net.ListenConfig{Control: func(network, address string, raw syscall.RawConn) error {
		if err := binding.Control(network, address, raw); err != nil {
			return err
		}
		var optionErr error
		if err := raw.Control(func(fd uintptr) { optionErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, -5, 1) }); err != nil {
			return err
		}
		return optionErr
	}}
	socket, err := lc.Listen(ctx, network, netip.AddrPortFrom(binding.Source, port).String())
	if err != nil {
		o.mu.Unlock()
		return nil, err
	}
	l := &WindowsListener{parent: o, socket: socket.(*net.TCPListener), binding: binding}
	if o.listeners == nil {
		o.listeners = make(map[*WindowsListener]struct{})
	}
	o.listeners[l] = struct{}{}
	l.grant, err = authorize(ctx, binding, l.socket.Addr().(*net.TCPAddr).AddrPort())
	if err == nil && l.grant == nil {
		err = fmt.Errorf("control listener authorization owner missing")
	}
	o.mu.Unlock()
	if err != nil {
		return nil, errors.Join(err, l.Close())
	}
	return l, nil
}

func (l *WindowsListener) Addr() net.Addr { return l.socket.Addr() }

func (l *WindowsListener) Accept() (net.Conn, error) {
	conn, err := l.socket.AcceptTCP()
	if err != nil {
		l.mu.Lock()
		closing := l.closing
		l.mu.Unlock()
		if closing {
			return nil, net.ErrClosed
		}
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closing {
		conn.Close()
		return nil, net.ErrClosed
	}
	local := conn.LocalAddr().(*net.TCPAddr).AddrPort()
	if local != l.socket.Addr().(*net.TCPAddr).AddrPort() {
		conn.Close()
		return nil, fmt.Errorf("accepted socket local endpoint changed")
	}
	raw, err := conn.SyscallConn()
	if err == nil {
		network := "tcp6"
		if local.Addr().Is4() {
			network = "tcp4"
		}
		err = l.binding.Control(network, conn.RemoteAddr().String(), raw)
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err = conn.SetKeepAliveConfig(net.KeepAliveConfig{Enable: true, Idle: 30 * time.Second}); err != nil {
		conn.Close()
		return nil, err
	}
	if err = conn.SetLinger(0); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// Close stops admissions and revokes replies before releasing the exclusive port reservation.
func (l *WindowsListener) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closing = true
	if err := l.socket.SetDeadline(time.Now()); err != nil && !errors.Is(err, net.ErrClosed) {
		l.mu.Unlock()
		return err
	}
	if l.grant != nil {
		if err := l.grant.Close(); err != nil {
			l.mu.Unlock()
			return err
		}
		l.grant = nil
	}
	if err := l.socket.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		l.mu.Unlock()
		return err
	}
	l.closed = true
	l.mu.Unlock()
	l.parent.mu.Lock()
	delete(l.parent.listeners, l)
	l.parent.mu.Unlock()
	return nil
}

func (o *WindowsListeners) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	o.closing = true
	o.mu.Unlock()
	return o.Drain()
}

// Drain retries retiring listeners while the runtime fences admissions for a network migration.
func (o *WindowsListeners) Drain() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	var listeners []*WindowsListener
	for l := range o.listeners {
		listeners = append(listeners, l)
	}
	o.mu.Unlock()
	var result error
	for _, l := range listeners {
		result = errors.Join(result, l.Close())
	}
	return result
}
