// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"

	"github.com/Irviodotnet/Irvio/internal/socketpolicy"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

const exclusiveAddressUse = -5

var errAddressInUse = windows.WSAEADDRINUSE

func isAddressInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, windows.WSAEACCES)
}

func datagramTruncated(flags int, err error) bool {
	return errors.Is(err, windows.WSAEMSGSIZE) || (err == nil && flags&windows.MSG_TRUNC != 0)
}

func listenPhysical(ctx context.Context, family AddressFamily, bind netip.Addr, port uint16) (*net.UDPConn, error) {
	network := "udp4"
	if family == IPv6 {
		network = "udp6"
	}
	lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var optionErr error
		if err := raw.Control(func(fd uintptr) {
			handle := windows.Handle(fd)
			optionErr = windows.SetsockoptInt(handle, windows.SOL_SOCKET, exclusiveAddressUse, 1)
			if optionErr == nil && family == IPv6 {
				optionErr = windows.SetsockoptInt(handle, windows.IPPROTO_IPV6, windows.IPV6_V6ONLY, 1)
			}
		}); err != nil {
			return err
		}
		return optionErr
	}}
	packet, err := lc.ListenPacket(ctx, network, netip.AddrPortFrom(bind, port).String())
	if err != nil {
		return nil, err
	}
	conn, ok := packet.(*net.UDPConn)
	if !ok {
		packet.Close()
		return nil, fmt.Errorf("listen %s returned %T, want *net.UDPConn", network, packet)
	}
	return conn, nil
}

func socketMarkSetter(conn *net.UDPConn) (func(uint32) error, error) {
	local := conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr()
	if local.IsUnspecified() {
		return func(mark uint32) error {
			if mark != 0 {
				return fmt.Errorf("Windows underlay policy requires a concrete local source address")
			}
			return nil
		}, nil
	}
	binding, err := winroute.ForSource(local)
	if err != nil {
		return nil, err
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	network := "udp6"
	if local.Is4() {
		network = "udp4"
	}
	set := func(mark uint32) error {
		if mark != 0 && mark != socketpolicy.Underlay {
			return fmt.Errorf("unsupported Windows physical UDP policy %#x", mark)
		}
		// Zero never removes the captured interface fence; WFP authorization is a separate layer.
		return binding.Control(network, "", raw)
	}
	if err := set(0); err != nil {
		return nil, err
	}
	return set, nil
}
