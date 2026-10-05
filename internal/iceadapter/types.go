// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"net/netip"
	"time"

	"github.com/pion/stun/v3"

	"hydra.local/hydra/internal/socketmux"
)

type transactionID [stun.TransactionIDSize]byte

type connKey struct {
	ufrag  string
	family socketmux.AddressFamily
}

type inboundPacket struct {
	payload []byte
	source  netip.AddrPort
}

type transaction struct {
	id        transactionID
	ufrag     string
	remote    netip.AddrPort
	family    socketmux.AddressFamily
	createdAt time.Time
	expiresAt time.Time
}

type retiredTransaction struct {
	id        transactionID
	remote    netip.AddrPort
	family    socketmux.AddressFamily
	expiresAt time.Time
}

// TransactionSnapshot is a copied diagnostic view of a registered outbound request.
type TransactionSnapshot struct {
	LocalUfrag string
	Remote     netip.AddrPort
	Family     socketmux.AddressFamily
	CreatedAt  time.Time
	ExpiresAt  time.Time
}
