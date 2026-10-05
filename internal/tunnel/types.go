// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnel

import (
	"net/netip"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

const (
	DefaultName = "hydra0"
	DefaultMTU  = 1280
)

// Config defines one long-lived kernel TUN owned by a logical TunnelSession.
type Config struct {
	Name      string
	MTU       int
	Addresses []netip.Prefix
}

// DeviceOwner is the platform-neutral lifetime used by TunnelSession.
type DeviceOwner interface {
	Name() string
	IfIndex() int
	Addresses() []netip.Prefix
	Borrow() (tun.Device, error)
	Close() error
}
