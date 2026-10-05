// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package localapi

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

var ErrUnauthorized = errors.New("local API peer is not authorized")

type PeerCredentials struct {
	PID int32
	UID uint32
	GID uint32
}

type Authorizer interface {
	Authorize(*net.UnixConn) (PeerCredentials, error)
}

type CredentialPolicy struct {
	AllowedUIDs map[uint32]struct{}
	AllowedGIDs map[uint32]struct{}
}

func (policy CredentialPolicy) Authorize(conn *net.UnixConn) (PeerCredentials, error) {
	if conn == nil {
		return PeerCredentials{}, ErrUnauthorized
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return PeerCredentials{}, err
	}
	var credentials *unix.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return PeerCredentials{}, err
	}
	if socketErr != nil {
		return PeerCredentials{}, socketErr
	}
	peer := PeerCredentials{PID: credentials.Pid, UID: credentials.Uid, GID: credentials.Gid}
	if _, ok := policy.AllowedUIDs[peer.UID]; ok {
		return peer, nil
	}
	if _, ok := policy.AllowedGIDs[peer.GID]; ok {
		return peer, nil
	}
	if supplementaryGroupAllowed(peer, policy.AllowedGIDs) {
		return peer, nil
	}
	return peer, ErrUnauthorized
}

func supplementaryGroupAllowed(peer PeerCredentials, allowed map[uint32]struct{}) bool {
	if peer.PID <= 0 || len(allowed) == 0 {
		return false
	}
	file, err := os.Open(fmt.Sprintf("/proc/%d/status", peer.PID))
	if err != nil {
		return false
	}
	defer file.Close()
	const maximumStatusBytes = 64 << 10
	payload, err := io.ReadAll(io.LimitReader(file, maximumStatusBytes+1))
	if err != nil || len(payload) > maximumStatusBytes {
		return false
	}
	var uidMatches bool
	var groups []string
	for _, line := range strings.Split(string(payload), "\n") {
		switch {
		case strings.HasPrefix(line, "Uid:"):
			fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
			uidMatches = len(fields) >= 2 && fields[0] == strconv.FormatUint(uint64(peer.UID), 10) && fields[1] == fields[0]
		case strings.HasPrefix(line, "Groups:"):
			groups = strings.Fields(strings.TrimPrefix(line, "Groups:"))
		}
	}
	if !uidMatches {
		return false
	}
	for _, value := range groups {
		group, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			continue
		}
		if _, ok := allowed[uint32(group)]; ok {
			return true
		}
	}
	return false
}
