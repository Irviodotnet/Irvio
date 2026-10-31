// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

var errAddressInUse = unix.EADDRINUSE

func isAddressInUse(err error) bool { return errors.Is(err, unix.EADDRINUSE) }

func datagramTruncated(flags int, err error) bool {
	return err == nil && flags&unix.MSG_TRUNC != 0
}

func socketMarkSetter(conn *net.UDPConn) (func(uint32) error, error) {
	return func(mark uint32) error {
		raw, err := conn.SyscallConn()
		if err != nil {
			return err
		}
		var socketErr error
		if err := raw.Control(func(fd uintptr) {
			socketErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(mark))
		}); err != nil {
			return err
		}
		return socketErr
	}, nil
}
