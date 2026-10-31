// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package controlsocket

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	manet "github.com/multiformats/go-multiaddr/net"
)

// DialEndpoint uses the supplied socket policy with no DNS or default-dialer fallback.
func DialEndpoint(ctx context.Context, factory tcptransport.DialerForAddr, endpoint netip.AddrPort) (net.Conn, error) {
	if ctx == nil || factory == nil || !endpoint.IsValid() || endpoint.Port() == 0 ||
		endpoint.Addr().IsUnspecified() || endpoint.Addr().IsMulticast() || endpoint.Addr().Is4In6() || endpoint.Addr().Zone() != "" {
		return nil, fmt.Errorf("invalid control TCP endpoint or dialer")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	address, err := manet.FromNetAddr(net.TCPAddrFromAddrPort(endpoint))
	if err != nil {
		return nil, err
	}
	dialer, err := factory(address)
	if err != nil {
		return nil, err
	}
	if dialer == nil {
		return nil, fmt.Errorf("control TCP dialer missing")
	}
	network := "tcp6"
	if endpoint.Addr().Is4() {
		network = "tcp4"
	}
	return dialer.DialContext(ctx, network, endpoint.String())
}
