// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

const exclusiveAddressUse = ^windows.SO_REUSEADDR

type windowsReservation struct {
	mu      sync.Mutex
	socket  windows.Handle
	startup bool
	port    uint16
}

// ReserveWindows binds an exclusive, non-listening port; it never forwards application traffic.
func ReserveWindows(ctx context.Context, path Path, protocol uint8) (Reservation, error) {
	if ctx == nil || !validPath(path) || (protocol != TCP && protocol != UDP) {
		return nil, ErrConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binding, err := winroute.ForSource(path.Source)
	if err != nil {
		return nil, fmt.Errorf("reserve Exit port: %w", err)
	}
	if binding.LUID != path.Interface {
		return nil, ErrStale
	}
	r := &windowsReservation{socket: windows.InvalidHandle}
	fail := func(cause error) (Reservation, error) {
		if cleanup := r.Close(); cleanup != nil {
			return r, errors.Join(cause, cleanup)
		}
		return nil, cause
	}
	var data windows.WSAData
	if err := windows.WSAStartup(0x202, &data); err != nil {
		return nil, fmt.Errorf("start Exit port Winsock: %w", err)
	}
	r.startup = true
	socketType, network := int32(windows.SOCK_STREAM), "tcp4"
	if protocol == UDP {
		socketType, network = windows.SOCK_DGRAM, "udp4"
	}
	r.socket, err = windows.WSASocket(windows.AF_INET, socketType, int32(protocol), nil, 0, windows.WSA_FLAG_NO_HANDLE_INHERIT)
	if err != nil {
		return fail(fmt.Errorf("create Exit port socket: %w", err))
	}
	if err := windows.SetsockoptInt(r.socket, windows.SOL_SOCKET, exclusiveAddressUse, 1); err != nil {
		return fail(fmt.Errorf("make Exit port exclusive: %w", err))
	}
	if err := binding.Control(network, "", reservationRawConn{r.socket}); err != nil {
		return fail(err)
	}
	if err := windows.Bind(r.socket, &windows.SockaddrInet4{Addr: path.Source.As4()}); err != nil {
		return fail(fmt.Errorf("bind Exit port: %w", err))
	}
	address, err := windows.Getsockname(r.socket)
	if err != nil {
		return fail(err)
	}
	v4, ok := address.(*windows.SockaddrInet4)
	if !ok || v4.Addr != path.Source.As4() || v4.Port <= 0 || v4.Port > 65535 {
		return fail(ErrConfig)
	}
	r.port = uint16(v4.Port)
	if err := binding.Control(network, "", reservationRawConn{r.socket}); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return r, nil
}

func (r *windowsReservation) Port() uint16 { return r.port }

func (r *windowsReservation) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.socket != windows.InvalidHandle {
		if err := windows.Closesocket(r.socket); err != nil {
			return fmt.Errorf("close Exit port socket: %w", err)
		}
		r.socket = windows.InvalidHandle
	}
	if r.startup {
		if err := windows.WSACleanup(); err != nil {
			return fmt.Errorf("release Exit port Winsock: %w", err)
		}
		r.startup = false
	}
	return nil
}

// reservationRawConn is used only during construction, before any other owner can close the socket.
type reservationRawConn struct{ socket windows.Handle }

func (r reservationRawConn) Control(f func(uintptr)) error { f(uintptr(r.socket)); return nil }
func (reservationRawConn) Read(func(uintptr) bool) error   { return ErrUnsupported }
func (reservationRawConn) Write(func(uintptr) bool) error  { return ErrUnsupported }
