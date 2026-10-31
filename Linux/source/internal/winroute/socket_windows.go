// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"encoding/binary"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const unicastInterfaceOption = 31

// Control pins socket egress; callers must also bind LocalAddr to Source and install their WFP policy.
func (b Binding) Control(network, _ string, raw syscall.RawConn) error {
	if network != "tcp4" && network != "udp4" && network != "tcp6" && network != "udp6" {
		return fmt.Errorf("interface binding requires an explicit TCP/UDP address family")
	}
	if strings.HasSuffix(network, "4") != b.Source.Is4() {
		return fmt.Errorf("socket binding address family mismatch")
	}
	if _, err := b.validate(); err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("socket handle is unavailable")
	}
	var optionErr error
	if err := raw.Control(func(fd uintptr) { optionErr = b.apply(windows.Handle(fd)) }); err != nil {
		return err
	}
	if optionErr != nil {
		return fmt.Errorf("pin socket interface: %w", optionErr)
	}
	return nil
}

func (b Binding) apply(fd windows.Handle) error {
	level := int32(windows.IPPROTO_IPV6)
	var index [4]byte
	if b.Source.Is4() {
		if b.Index > 0xffffff {
			return fmt.Errorf("IPv4 unicast interface index exceeds 24 bits")
		}
		level = windows.IPPROTO_IP
		binary.BigEndian.PutUint32(index[:], b.Index)
	} else {
		binary.NativeEndian.PutUint32(index[:], b.Index)
	}
	if err := windows.Setsockopt(fd, level, unicastInterfaceOption, &index[0], int32(len(index))); err != nil {
		return err
	}
	// Revalidate after native configuration so a replaced interface cannot be accepted silently.
	_, err := b.validate()
	return err
}

// SocketInterface reads the native egress constraint for diagnostics and tests, not policy authorization.
func SocketInterface(raw syscall.RawConn, ipv4 bool) (uint32, error) {
	if raw == nil {
		return 0, fmt.Errorf("socket handle is unavailable")
	}
	level := int32(windows.IPPROTO_IPV6)
	if ipv4 {
		level = windows.IPPROTO_IP
	}
	var value uint32
	length := int32(unsafe.Sizeof(value))
	var optionErr error
	if err := raw.Control(func(fd uintptr) {
		optionErr = windows.Getsockopt(windows.Handle(fd), level, unicastInterfaceOption, (*byte)(unsafe.Pointer(&value)), &length)
	}); err != nil {
		return 0, err
	}
	if optionErr != nil {
		return 0, optionErr
	}
	if length != int32(unsafe.Sizeof(value)) {
		return 0, fmt.Errorf("invalid interface option length %d", length)
	}
	return value, nil
}
