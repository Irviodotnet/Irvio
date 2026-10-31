// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package socketmux

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"syscall"

	"golang.org/x/sys/unix"
)

func listenPhysical(ctx context.Context, family AddressFamily, bind netip.Addr, port uint16) (*net.UDPConn, error) {
	network := "udp4"
	listenConfig := net.ListenConfig{}
	if family == IPv6 {
		network = "udp6"
		listenConfig.Control = func(_, _ string, raw syscall.RawConn) error {
			var optionErr error
			if err := raw.Control(func(fd uintptr) {
				optionErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 1)
			}); err != nil {
				return fmt.Errorf("access IPv6 socket: %w", err)
			}
			if optionErr != nil {
				return fmt.Errorf("set IPV6_V6ONLY: %w", optionErr)
			}
			return nil
		}
	}
	packetConn, err := listenConfig.ListenPacket(ctx, network, netip.AddrPortFrom(bind, port).String())
	if err != nil {
		return nil, err
	}
	conn, ok := packetConn.(*net.UDPConn)
	if !ok {
		packetConn.Close()
		return nil, fmt.Errorf("listen %s returned %T, want *net.UDPConn", network, packetConn)
	}
	return conn, nil
}
