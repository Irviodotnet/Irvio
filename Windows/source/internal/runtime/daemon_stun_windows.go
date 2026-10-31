// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"time"

	"github.com/Irviodotnet/Irvio/internal/stunserver"
	"golang.org/x/sys/windows"
)

func (d *Daemon) startWindowsSTUN(ctx context.Context) error {
	p := &d.platform
	if p.stunConn != nil {
		return ErrAlreadyStarted
	}
	binding, generation, err := p.binding(ctx, true)
	if err != nil {
		return err
	}
	if bind := d.config.PublicSTUNBind; bind.IsValid() && !bind.IsUnspecified() && bind != binding.Source {
		return ErrInvalidConfig
	}
	lc := net.ListenConfig{Control: func(network, address string, raw syscall.RawConn) error {
		if err := binding.Control(network, address, raw); err != nil {
			return err
		}
		var failure error
		if err := raw.Control(func(fd uintptr) { failure = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, -5, 1) }); err != nil {
			return err
		}
		return failure
	}}
	conn, err := lc.ListenPacket(ctx, "udp4", netip.AddrPortFrom(binding.Source, stunserver.DefaultPort).String())
	if err != nil {
		return err
	}
	p.stunConn = conn.(*net.UDPConn)
	p.stunGrant, err = p.permissions.Datagram(ctx, generation, binding, p.stunConn.LocalAddr().(*net.UDPAddr).AddrPort())
	if err != nil {
		return err
	}
	if p.stunGrant == nil {
		return ErrInvalidConfig
	}
	d.stunServer, err = stunserver.Serve(p.stunConn)
	if err == nil {
		d.config.Logger.Info("PUBLIC_STUN_READY", "bind", d.stunServer.Addr())
	}
	return err
}

func (d *Daemon) stopWindowsSTUN() error {
	p := &d.platform
	if p.stunConn == nil {
		return nil
	}
	if err := p.stunConn.SetReadDeadline(time.Now()); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	if p.stunGrant != nil {
		if err := p.stunGrant.Close(); err != nil {
			return err
		}
		p.stunGrant = nil
	}
	var err error
	if d.stunServer != nil {
		err = d.stunServer.Close()
	} else {
		err = p.stunConn.Close()
	}
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	p.stunConn, d.stunServer = nil, nil
	return nil
}
