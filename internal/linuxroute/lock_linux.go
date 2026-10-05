// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package linuxroute

import (
	"errors"
	"sync"

	"golang.org/x/sys/unix"
)

const routeLockName = "\x00hydra-linuxroute-v1"

// routeLock is namespace-local and kernel-released on crash, distinguishing live ownership from residue.
type routeLock struct {
	fd        int
	closeOnce sync.Once
	closeErr  error
}

func acquireRouteLock() (*routeLock, error) {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: routeLockName}); err != nil {
		_ = unix.Close(fd)
		if errors.Is(err, unix.EADDRINUSE) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return &routeLock{fd: fd}, nil
}

func (l *routeLock) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.closeErr = unix.Close(l.fd)
		l.fd = -1
	})
	return l.closeErr
}
