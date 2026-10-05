// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build !linux

package sessioncontrol

import (
	"context"
	"net"
)

func TunnelDialer(string) DialFunc {
	return func(context.Context, string, string) (net.Conn, error) { return nil, ErrInvalid }
}

func ListenTunnel(context.Context, string, string, string) (net.Listener, error) {
	return nil, ErrInvalid
}
