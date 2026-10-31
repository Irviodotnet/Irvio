// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package controlsocket

import (
	"fmt"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/transport"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

type ListenerFactory func(multiaddr.Multiaddr) (manet.Listener, error)

type listenerTransport struct {
	*tcptransport.TcpTransport
	upgrader transport.Upgrader
	listen   ListenerFactory
}

// TransportForListener preserves libp2p security, muxing and resource gates around owned sockets.
func TransportForListener(listen ListenerFactory, dial tcptransport.DialerForAddr) func(transport.Upgrader, network.ResourceManager) (transport.Transport, error) {
	return func(upgrader transport.Upgrader, manager network.ResourceManager) (transport.Transport, error) {
		if listen == nil {
			return nil, fmt.Errorf("control listener factory required")
		}
		opts := []tcptransport.Option{tcptransport.DisableReuseport()}
		if dial != nil {
			opts = append(opts, tcptransport.WithDialerForAddr(dial))
		}
		base, err := tcptransport.NewTCPTransport(upgrader, manager, nil, opts...)
		if err != nil {
			return nil, err
		}
		return &listenerTransport{TcpTransport: base, upgrader: upgrader, listen: listen}, nil
	}
}

func (t *listenerTransport) Listen(addr multiaddr.Multiaddr) (transport.Listener, error) {
	listener, err := t.listen(addr)
	if err != nil {
		return nil, err
	}
	if listener == nil {
		return nil, fmt.Errorf("control listener missing")
	}
	return t.upgrader.UpgradeGatedMaListener(t, t.upgrader.GateMaListener(listener)), nil
}
