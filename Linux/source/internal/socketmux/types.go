// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package socketmux owns one socket per family and bounded nonblocking queues of copied datagrams.
package socketmux

import "net/netip"

// AddressFamily identifies an IP address family without string parsing.
type AddressFamily uint8

const (
	IPv4 AddressFamily = 4
	IPv6 AddressFamily = 6
)

// String returns the conventional family name.
func (f AddressFamily) String() string {
	switch f {
	case IPv4:
		return "IPv4"
	case IPv6:
		return "IPv6"
	default:
		return "unknown"
	}
}

// PacketClass selects an inbound logical destination.
type PacketClass uint8

const (
	Control PacketClass = iota + 1
	Data
	Drop
)

// Datagram.Payload is a receiver-owned copy and may be modified after Read.
type Datagram struct {
	Payload []byte
	Source  netip.AddrPort
	Family  AddressFamily
}

// Classifier must be concurrent-safe, nonblocking, read-only, and never retain payloads.
type Classifier interface {
	Classify(payload []byte, source netip.AddrPort, family AddressFamily) PacketClass
}

// ClassifierFunc adapts a function to Classifier.
type ClassifierFunc func(payload []byte, source netip.AddrPort, family AddressFamily) PacketClass

// Classify invokes f.
func (f ClassifierFunc) Classify(payload []byte, source netip.AddrPort, family AddressFamily) PacketClass {
	return f(payload, source, family)
}
