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

	"github.com/Irviodotnet/Irvio/internal/winroute"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// BindingResolver supplies the runtime-owned underlay generation, not an unconstrained current default route.
type BindingResolver func(context.Context, netip.Addr) (winroute.Binding, error)

// OwnedBindingResolver authorizes one generation and retains grants whose cleanup fails.
type OwnedBindingResolver func(context.Context, netip.AddrPort) (winroute.Binding, io.Closer, error)

// DialerForMark refuses mark-only configuration because it cannot identify a Windows underlay interface.
func DialerForMark(mark uint32) (tcptransport.DialerForAddr, error) {
	if mark == 0 {
		return nil, ErrInvalidMark
	}
	return nil, fmt.Errorf("Windows control sockets require explicit source/interface bindings, not a Linux mark")
}

// DialerForBindings resolves each new dial to an explicit source/LUID with no DNS or routing fallback.
func DialerForBindings(resolve BindingResolver) (tcptransport.DialerForAddr, error) {
	if resolve == nil {
		return nil, fmt.Errorf("Windows control socket binding resolver is required")
	}
	return windowsDialerFactory(resolve, nil), nil
}

// DialerForOwnedBindings releases authorization on failed dial or connection Close.
func DialerForOwnedBindings(resolve OwnedBindingResolver) (tcptransport.DialerForAddr, error) {
	if resolve == nil {
		return nil, fmt.Errorf("Windows control socket owned binding resolver is required")
	}
	return windowsDialerFactory(nil, resolve), nil
}

func windowsDialerFactory(resolve BindingResolver, owned OwnedBindingResolver) tcptransport.DialerForAddr {
	return func(address multiaddr.Multiaddr) (tcptransport.ContextDialer, error) {
		if len(address) != 2 || (address[0].Protocol().Code != multiaddr.P_IP4 && address[0].Protocol().Code != multiaddr.P_IP6) || address[1].Protocol().Code != multiaddr.P_TCP {
			return nil, fmt.Errorf("Windows control dial requires a resolved direct IP/TCP multiaddr")
		}
		network, target, err := manet.DialArgs(address)
		if err != nil {
			return nil, err
		}
		endpoint, err := netip.ParseAddrPort(target)
		if err != nil || endpoint.Port() == 0 || endpoint.Addr().IsUnspecified() || endpoint.Addr().IsMulticast() || endpoint.Addr().Is4In6() || endpoint.Addr().Zone() != "" {
			return nil, fmt.Errorf("invalid Windows control dial endpoint")
		}
		return &boundDialer{resolve: resolve, owned: owned, network: network, endpoint: endpoint}, nil
	}
}

type boundDialer struct {
	resolve  BindingResolver
	owned    OwnedBindingResolver
	network  string
	endpoint netip.AddrPort
}

func (d *boundDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if ctx == nil {
		return nil, fmt.Errorf("control dial requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := netip.ParseAddrPort(address)
	if err != nil || target != d.endpoint || (network != d.network && network != "tcp") {
		return nil, fmt.Errorf("control dial arguments differ from the authorized endpoint")
	}
	var binding winroute.Binding
	var grant io.Closer
	if d.owned != nil {
		binding, grant, err = d.owned(ctx, target)
		if err == nil && grant == nil {
			err = fmt.Errorf("control socket authorization owner is missing")
		}
	} else {
		binding, err = d.resolve(ctx, target.Addr())
	}
	fail := func(err error) (net.Conn, error) {
		if grant != nil {
			err = errors.Join(err, grant.Close())
		}
		return nil, err
	}
	if err != nil {
		return fail(fmt.Errorf("resolve control socket binding: %w", err))
	}
	if binding.Source.Is4() != target.Addr().Is4() {
		return fail(fmt.Errorf("control socket source family mismatch"))
	}
	dialer := net.Dialer{Control: binding.Control, LocalAddr: &net.TCPAddr{IP: net.IP(binding.Source.AsSlice())}}
	conn, err := dialer.DialContext(ctx, d.network, target.String())
	if err != nil {
		return fail(err)
	}
	if grant == nil {
		return conn, nil
	}
	return &ownedTCPConn{TCPConn: conn.(*net.TCPConn), grant: grant}, nil
}

type ownedTCPConn struct {
	*net.TCPConn
	mu       sync.Mutex
	grant    io.Closer
	closed   bool
	closeErr error
}

// Close stops traffic before revocation and retains the grant for retry on cleanup failure.
func (c *ownedTCPConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closeErr = c.TCPConn.Close()
		c.closed = true
	}
	if c.grant != nil {
		if err := c.grant.Close(); err != nil {
			return errors.Join(c.closeErr, err)
		}
		c.grant = nil
	}
	return c.closeErr
}
