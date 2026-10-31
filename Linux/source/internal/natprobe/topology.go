// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import (
	"fmt"
	"net/netip"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

// Topology is the validated two-address, two-port RFC 5780 server layout.
type Topology struct {
	Primary                  netip.AddrPort
	Other                    netip.AddrPort
	AlternateIPPrimaryPort   netip.AddrPort
	PrimaryIPAlternatePort   netip.AddrPort
	AlternateIPAlternatePort netip.AddrPort
}

func topologyFromBaseline(primary netip.AddrPort, response stunprobe.BindingResponse) (Topology, error) {
	if err := validUnicastEndpoint(primary); err != nil {
		return Topology{}, fmt.Errorf("%w: invalid primary endpoint", ErrInvalidTopology)
	}
	if response.Destination != primary || response.ResponseSource != primary {
		return Topology{}, fmt.Errorf("%w: baseline destination or source mismatch", ErrInvalidTopology)
	}
	if !response.ResponseOrigin.Present {
		return Topology{}, fmt.Errorf("%w: missing RESPONSE-ORIGIN", ErrServerUnsupported)
	}
	if response.ResponseOrigin.AddrPort != response.ResponseSource || response.ResponseOrigin.AddrPort != primary {
		return Topology{}, fmt.Errorf("%w: invalid RESPONSE-ORIGIN", ErrInvalidTopology)
	}
	if !response.OtherAddress.Present {
		return Topology{}, fmt.Errorf("%w: missing OTHER-ADDRESS", ErrServerUnsupported)
	}
	other := response.OtherAddress.AddrPort
	if err := validUnicastEndpoint(other); err != nil {
		return Topology{}, fmt.Errorf("%w: invalid OTHER-ADDRESS", ErrInvalidTopology)
	}
	if endpointFamily(primary) != endpointFamily(other) || response.Family != endpointFamily(primary) {
		return Topology{}, fmt.Errorf("%w: mixed address family", ErrInvalidTopology)
	}
	if primary.Addr() == other.Addr() || primary.Port() == other.Port() || primary == other {
		return Topology{}, fmt.Errorf("%w: topology requires distinct addresses and ports", ErrInvalidTopology)
	}
	return Topology{
		Primary:                  primary,
		Other:                    other,
		AlternateIPPrimaryPort:   netip.AddrPortFrom(other.Addr(), primary.Port()),
		PrimaryIPAlternatePort:   netip.AddrPortFrom(primary.Addr(), other.Port()),
		AlternateIPAlternatePort: other,
	}, nil
}

func endpointFamily(endpoint netip.AddrPort) socketmux.AddressFamily {
	if endpoint.Addr().Is4() {
		return socketmux.IPv4
	}
	if endpoint.Addr().Is6() && !endpoint.Addr().Is4In6() {
		return socketmux.IPv6
	}
	return 0
}

func validUnicastEndpoint(endpoint netip.AddrPort) error {
	if !endpoint.IsValid() || endpoint.Port() == 0 || endpoint.Addr().IsUnspecified() || endpoint.Addr().IsMulticast() || endpoint.Addr().Is4In6() {
		return ErrInvalidServer
	}
	if endpoint.Addr().Is4() && endpoint.Addr().As4() == [4]byte{255, 255, 255, 255} {
		return ErrInvalidServer
	}
	if endpointFamily(endpoint) == 0 {
		return ErrInvalidServer
	}
	return nil
}
