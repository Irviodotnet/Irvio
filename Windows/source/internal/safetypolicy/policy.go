// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package safetypolicy defines one platform-neutral client/Exit user-traffic deny policy.
package safetypolicy

// Version identifies local policy, not a negotiated capability.
type Version uint8

const (
	// V0 is the single blocked-service definition for both trust boundaries.
	V0 Version = iota
)

// Transport is an inner user-packet transport protocol.
type Transport uint8

const (
	TransportTCP Transport = iota + 1
	TransportUDP
)

// PortRange is one inclusive policy entry.
type PortRange struct {
	Transport Transport
	From      uint16
	To        uint16
	Service   string
}

var v0Rules = [...]PortRange{
	{Transport: TransportTCP, From: 19, To: 19, Service: "chargen"},
	{Transport: TransportTCP, From: 25, To: 25, Service: "smtp-server"},
	{Transport: TransportTCP, From: 135, To: 135, Service: "ms-rpc-endpoint-mapper"},
	{Transport: TransportTCP, From: 137, To: 139, Service: "netbios"},
	{Transport: TransportTCP, From: 445, To: 445, Service: "smb"},
	{Transport: TransportUDP, From: 19, To: 19, Service: "chargen"},
	{Transport: TransportUDP, From: 135, To: 135, Service: "ms-rpc-endpoint-mapper"},
	{Transport: TransportUDP, From: 137, To: 139, Service: "netbios"},
	{Transport: TransportUDP, From: 1900, To: 1900, Service: "ssdp"},
	{Transport: TransportUDP, From: 11211, To: 11211, Service: "memcached"},
}

// Rules returns a defensive copy of the canonical policy.
func Rules(version Version) []PortRange {
	if version != V0 {
		return nil
	}
	return append([]PortRange(nil), v0Rules[:]...)
}

// Ports expands the small canonical policy into sorted per-transport sets.
func Ports(version Version, transport Transport) []uint16 {
	var ports []uint16
	for _, rule := range Rules(version) {
		if rule.Transport != transport {
			continue
		}
		for port := uint32(rule.From); port <= uint32(rule.To); port++ {
			ports = append(ports, uint16(port))
		}
	}
	return ports
}

// Blocks reports whether one inner user transport tuple is denied.
func Blocks(version Version, transport Transport, port uint16) bool {
	for _, rule := range Rules(version) {
		if rule.Transport == transport && port >= rule.From && port <= rule.To {
			return true
		}
	}
	return false
}
