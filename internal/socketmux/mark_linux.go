// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package socketmux

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// SetMark updates all mux sockets, rolling back earlier families if any update fails.
func (m *Mux) SetMark(mark uint32) error {
	if m == nil {
		return ErrClosed
	}
	m.markMu.Lock()
	defer m.markMu.Unlock()
	if m.closed.Load() {
		return ErrClosed
	}
	if m.mark == mark {
		return nil
	}
	previous := m.mark
	changed := make([]*physicalSocket, 0, 2)
	for _, socket := range []*physicalSocket{m.ipv4, m.ipv6} {
		if socket == nil {
			continue
		}
		if err := m.setMark(socket.conn, mark); err != nil {
			var rollbackErrors []error
			for index := len(changed) - 1; index >= 0; index-- {
				if rollbackErr := m.setMark(changed[index].conn, previous); rollbackErr != nil {
					rollbackErrors = append(rollbackErrors, rollbackErr)
				}
			}
			return errors.Join(fmt.Errorf("set %s UDP socket SO_MARK to %#x: %w", socket.family, mark, err), errors.Join(rollbackErrors...))
		}
		changed = append(changed, socket)
	}
	m.mark = mark
	return nil
}

func setUDPMark(conn *net.UDPConn, mark uint32) error {
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
}
