// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package clientdplab defines disposable fixture coordination, never a product wire protocol.
package clientdplab

import (
	"net/netip"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

type Hello struct {
	Nonce     string
	Peer      string
	PublicKey awg.Key
}

type Ready struct {
	Peer             string
	PublicKey        awg.Key
	Lease            tunnellease.Lease
	UDP              netip.AddrPort
	SignedDescriptor []byte
}

type Mapped struct{ Endpoint netip.AddrPort }

type Result struct {
	Released  bool
	Remaining int
	Renewed   uint64
}
