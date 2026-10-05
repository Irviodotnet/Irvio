// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"net/netip"
	"time"

	"github.com/pion/stun/v3"

	"hydra.local/hydra/internal/socketmux"
)

// TransactionID is the immutable STUN transaction identifier of an exchange.
type TransactionID [stun.TransactionIDSize]byte

// OptionalAddrPort represents an optional decoded STUN address attribute.
type OptionalAddrPort struct {
	AddrPort netip.AddrPort
	Present  bool
}

// BindingRequest describes one Binding exchange and its bounded response-source policy.
type BindingRequest struct {
	Destination           netip.AddrPort
	ExpectedSuccessSource netip.AddrPort
	AllowedErrorSources   []netip.AddrPort
	ChangeIP              bool
	ChangePort            bool
}

// BindingResponse is a copied, validated Binding success response.
type BindingResponse struct {
	Destination    netip.AddrPort
	ResponseSource netip.AddrPort
	LocalEndpoint  netip.AddrPort
	MappedEndpoint netip.AddrPort
	MappedAddress  OptionalAddrPort
	OtherAddress   OptionalAddrPort
	ResponseOrigin OptionalAddrPort
	Family         socketmux.AddressFamily
	RoundTrip      time.Duration
	Attempts       int
	CompletedAt    time.Time
	TransactionID  TransactionID
}

// Result describes one Basic STUN Binding observation.
type Result struct {
	Server      netip.AddrPort
	Local       netip.AddrPort
	Mapped      netip.AddrPort
	Family      socketmux.AddressFamily
	RoundTrip   time.Duration
	Attempts    int
	CompletedAt time.Time
}
