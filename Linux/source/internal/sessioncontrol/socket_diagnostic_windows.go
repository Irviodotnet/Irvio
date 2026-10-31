// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const tcpDiagnosticMaxBytes = 4 << 20
const tcpDiagnosticMaxRows = 8

var sessionTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

type tcpPortOwner struct {
	AddressClass string
	State, PID   uint32
}

type tcpPortSnapshot struct {
	Code    uint32
	Matches uint32
	Owners  []tcpPortOwner
}

type tunnelListenDiagnostic struct {
	cause   error
	v4, v6  tcpPortSnapshot
	elapsed time.Duration
}

func (d *tunnelListenDiagnostic) Unwrap() error { return d.cause }

func (d *tunnelListenDiagnostic) Error() string {
	return fmt.Sprintf("%v; TCP port observation (not proof of bind ownership): ipv4=%+v ipv6=%+v elapsed_us=%d", d.cause, d.v4, d.v6, d.elapsed.Microseconds())
}

// Separate short records preserve observations through the existing sanitized journal field limit.
func (d *tunnelListenDiagnostic) logListenDiagnostics(logger *slog.Logger) {
	for i, snapshot := range []tcpPortSnapshot{d.v4, d.v6} {
		family := []string{"ipv4", "ipv6"}[i]
		logger.Warn("SESSION_CONTROL_BIND_OBSERVATION", "reason", fmt.Sprintf("family=%s code=%d matches=%d retained=%d elapsed_us=%d; observation only; bound-only sockets may be absent", family, snapshot.Code, snapshot.Matches, len(snapshot.Owners), d.elapsed.Microseconds()))
		for _, row := range snapshot.Owners {
			logger.Warn("SESSION_CONTROL_BIND_PORT_ROW", "reason", fmt.Sprintf("family=%s address_class=%s state=%d pid=%d", family, row.AddressClass, row.State, row.PID))
		}
	}
}

// diagnoseTunnelListen observes a failed bind without retrying it or exposing remote endpoints.
func diagnoseTunnelListen(cause error, endpoint netip.AddrPort) error {
	if !errors.Is(cause, windows.WSAEACCES) && !errors.Is(cause, windows.WSAEADDRINUSE) {
		return cause
	}
	start := time.Now()
	v4 := readTCPPortSnapshot(windows.AF_INET, endpoint)
	v6 := readTCPPortSnapshot(windows.AF_INET6, endpoint)
	return &tunnelListenDiagnostic{cause: cause, v4: v4, v6: v6, elapsed: time.Since(start)}
}

// readTCPPortSnapshot bounds memory and calls; Windows may omit bound-only or already closed sockets.
func readTCPPortSnapshot(family uint32, endpoint netip.AddrPort) tcpPortSnapshot {
	var size uint32
	code, _, _ := sessionTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), 5, 0)
	if code != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
		return tcpPortSnapshot{Code: uint32(code)}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if size < 4 || size > tcpDiagnosticMaxBytes {
			return tcpPortSnapshot{Code: uint32(windows.ERROR_INSUFFICIENT_BUFFER)}
		}
		buffer := make([]byte, int(size))
		code, _, _ = sessionTCPTable.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), 5, 0)
		runtime.KeepAlive(buffer)
		if code == uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
			continue
		}
		if code != 0 {
			return tcpPortSnapshot{Code: uint32(code)}
		}
		if size > uint32(len(buffer)) {
			return tcpPortSnapshot{Code: uint32(windows.ERROR_INVALID_DATA)}
		}
		return parseTCPPortSnapshot(buffer[:size], family, endpoint)
	}
	return tcpPortSnapshot{Code: uint32(windows.ERROR_INSUFFICIENT_BUFFER)}
}

func parseTCPPortSnapshot(buffer []byte, family uint32, endpoint netip.AddrPort) tcpPortSnapshot {
	invalid := tcpPortSnapshot{Code: uint32(windows.ERROR_INVALID_DATA)}
	rowSize, addrOffset, addrSize, portOffset, stateOffset, pidOffset := 24, 4, 4, 8, 0, 20
	if family == windows.AF_INET6 {
		rowSize, addrOffset, addrSize, portOffset, stateOffset, pidOffset = 56, 0, 16, 20, 48, 52
	} else if family != windows.AF_INET {
		return invalid
	}
	if len(buffer) < 4 || !endpoint.IsValid() {
		return invalid
	}
	count := binary.LittleEndian.Uint32(buffer)
	if uint64(count) > uint64((len(buffer)-4)/rowSize) {
		return invalid
	}
	var result tcpPortSnapshot
	for i := uint32(0); i < count; i++ {
		row := buffer[4+int(i)*rowSize : 4+(int(i)+1)*rowSize]
		if binary.BigEndian.Uint16(row[portOffset:]) != endpoint.Port() {
			continue
		}
		result.Matches++
		if len(result.Owners) >= tcpDiagnosticMaxRows {
			continue
		}
		address, _ := netip.AddrFromSlice(row[addrOffset : addrOffset+addrSize])
		kind := "other-local"
		if address.IsUnspecified() {
			kind = "wildcard"
		} else if address.Unmap() == endpoint.Addr().Unmap() {
			kind = "target"
		}
		result.Owners = append(result.Owners, tcpPortOwner{AddressClass: kind,
			State: binary.LittleEndian.Uint32(row[stateOffset:]), PID: binary.LittleEndian.Uint32(row[pidOffset:])})
	}
	return result
}
