// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"net/netip"

	awgconn "github.com/amnezia-vpn/amneziawg-go/v3/conn"
)

// Endpoint is an AWG destination tied to one IrvioBind instance.
type Endpoint struct {
	destination netip.AddrPort
	owner       *IrvioBind
}

var _ awgconn.Endpoint = (*Endpoint)(nil)

func (e *Endpoint) ClearSrc() {}

func (e *Endpoint) SrcToString() string { return "" }

func (e *Endpoint) DstToString() string {
	if e == nil {
		return ""
	}
	return e.destination.String()
}

func (e *Endpoint) DstToBytes() []byte {
	if e == nil {
		return nil
	}
	encoded, _ := e.destination.MarshalBinary()
	return encoded
}

func (e *Endpoint) DstIP() netip.Addr {
	if e == nil {
		return netip.Addr{}
	}
	return e.destination.Addr()
}

func (*Endpoint) SrcIP() netip.Addr { return netip.Addr{} }

// AddrPort returns the copied public destination for bounded diagnostics.
func (e *Endpoint) AddrPort() netip.AddrPort {
	if e == nil {
		return netip.AddrPort{}
	}
	return e.destination
}
