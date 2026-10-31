// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"syscall"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

type tunnelInterface struct {
	luid  uint64
	index uint32
	guid  windows.GUID
}

func captureTunnelInterface(device string) (tunnelInterface, error) {
	if device == "" {
		return tunnelInterface{}, fmt.Errorf("session control requires a tunnel interface")
	}
	iface, err := net.InterfaceByName(device)
	if err != nil {
		return tunnelInterface{}, err
	}
	row := windows.MibIfRow2{InterfaceIndex: uint32(iface.Index)}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		return tunnelInterface{}, err
	}
	owner := tunnelInterface{luid: row.InterfaceLuid, index: row.InterfaceIndex, guid: row.InterfaceGuid}
	return owner, owner.validate()
}

func (o tunnelInterface) validate() error {
	if o.luid == 0 || o.index == 0 || o.guid == (windows.GUID{}) {
		return fmt.Errorf("session control tunnel identity is missing")
	}
	row := windows.MibIfRow2{InterfaceLuid: o.luid}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		return err
	}
	if row.InterfaceLuid != o.luid || row.InterfaceIndex != o.index || row.InterfaceGuid != o.guid ||
		row.Type != 53 || row.OperStatus != windows.IfOperStatusUp {
		return fmt.Errorf("session control tunnel was replaced, is down, or is not virtual")
	}
	return nil
}

func tunnelEndpoint(network, address string, listen bool) (string, netip.AddrPort, error) {
	endpoint, err := netip.ParseAddrPort(address)
	if err != nil {
		return "", netip.AddrPort{}, fmt.Errorf("session control requires a literal IP endpoint: %w", err)
	}
	ip := endpoint.Addr()
	if !ip.IsGlobalUnicast() || ip.Is4In6() || ip.Zone() != "" || ip.IsLinkLocalUnicast() || !listen && endpoint.Port() == 0 {
		return "", netip.AddrPort{}, fmt.Errorf("invalid session control endpoint")
	}
	family := "tcp6"
	if ip.Is4() {
		family = "tcp4"
	}
	if network != "tcp" && network != family {
		return "", netip.AddrPort{}, fmt.Errorf("session control network/address family mismatch")
	}
	return family, endpoint, nil
}

func (o tunnelInterface) control(binding winroute.Binding) func(string, string, syscall.RawConn) error {
	return func(network, address string, raw syscall.RawConn) error {
		if err := o.validate(); err != nil {
			return err
		}
		if binding.LUID != o.luid || binding.Index != o.index {
			return fmt.Errorf("session control source does not belong to its tunnel")
		}
		if err := binding.Control(network, address, raw); err != nil {
			return err
		}
		return o.validate()
	}
}

// TunnelDialer captures interface identity once so reconnect cannot follow a reused name or index.
func TunnelDialer(device string) DialFunc {
	owner, captureErr := captureTunnelInterface(device)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if ctx == nil {
			return nil, ErrInvalid
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if captureErr != nil {
			return nil, captureErr
		}
		family, endpoint, err := tunnelEndpoint(network, address, false)
		if err != nil {
			return nil, err
		}
		if err := owner.validate(); err != nil {
			return nil, err
		}
		binding, err := winroute.ResolveTunnel(endpoint.Addr(), owner.luid)
		if err != nil {
			return nil, fmt.Errorf("resolve session control tunnel: %w", err)
		}
		dialer := net.Dialer{Control: owner.control(binding), LocalAddr: &net.TCPAddr{IP: net.IP(binding.Source.AsSlice())}}
		conn, err := dialer.DialContext(ctx, family, endpoint.String())
		if err != nil {
			return nil, err
		}
		if err := owner.validate(); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}
}

// ListenTunnel binds a concrete TUN address; WFP ingress policy and Noise authentication remain required.
func ListenTunnel(ctx context.Context, network, address, device string) (net.Listener, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	family, endpoint, err := tunnelEndpoint(network, address, true)
	if err != nil {
		return nil, err
	}
	owner, err := captureTunnelInterface(device)
	if err != nil {
		return nil, err
	}
	binding, err := winroute.ForSource(endpoint.Addr())
	if err != nil {
		return nil, err
	}
	listen := net.ListenConfig{Control: owner.control(binding)}
	listener, err := listen.Listen(ctx, family, endpoint.String())
	if err != nil {
		return nil, diagnoseTunnelListen(err, endpoint)
	}
	if err := owner.validate(); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}
