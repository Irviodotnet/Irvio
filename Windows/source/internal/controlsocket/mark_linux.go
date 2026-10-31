// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package controlsocket

import (
	"fmt"
	"net"
	"syscall"

	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"golang.org/x/sys/unix"
)

// DialerForMark marks TCP sockets before connect, without host-wide bypasses.
func DialerForMark(mark uint32) (tcptransport.DialerForAddr, error) {
	if mark == 0 {
		return nil, ErrInvalidMark
	}
	control := Control(mark)
	return func(_ multiaddr.Multiaddr) (tcptransport.ContextDialer, error) {
		return &net.Dialer{Control: control}, nil
	}, nil
}

// Control marks a socket before connect or exposure to libp2p.
func Control(mark uint32) func(string, string, syscall.RawConn) error {
	return control(mark, "", false)
}

// ControlOnDevice marks and binds sockets so lost tunnel routes cannot fall back to WAN.
func ControlOnDevice(mark uint32, device string) func(string, string, syscall.RawConn) error {
	return control(mark, device, true)
}

func control(mark uint32, device string, requireDevice bool) func(string, string, syscall.RawConn) error {
	return func(_, _ string, raw syscall.RawConn) error {
		if mark == 0 {
			return ErrInvalidMark
		}
		if requireDevice && device == "" {
			return ErrInvalidDevice
		}
		var setErr error
		if err := raw.Control(func(fd uintptr) {
			setErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(mark))
			if setErr == nil && device != "" {
				setErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, device)
			}
		}); err != nil {
			return fmt.Errorf("access Irvio control socket: %w", err)
		}
		if setErr != nil {
			return fmt.Errorf("configure Irvio control socket: %w", setErr)
		}
		return nil
	}
}
