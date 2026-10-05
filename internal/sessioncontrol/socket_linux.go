// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package sessioncontrol

import (
	"context"
	"net"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
)

func TunnelDialer(device string) DialFunc {
	dialer := &net.Dialer{Control: controlsocket.ControlOnDevice(linuxroute.IrvioTunnelControlMark, device)}
	return dialer.DialContext
}

func ListenTunnel(ctx context.Context, network, address, device string) (net.Listener, error) {
	if device == "" {
		return nil, controlsocket.ErrInvalidDevice
	}
	listen := net.ListenConfig{Control: controlsocket.ControlOnDevice(linuxroute.IrvioTunnelControlMark, device)}
	return listen.Listen(ctx, network, address)
}
