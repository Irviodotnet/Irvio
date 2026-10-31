// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import (
	"fmt"
	"net/netip"
	"unicode/utf8"

	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/icesession"
)

const maxCandidateFoundationBytes = 64

// CandidateSnapshot is bounded diagnostics; M5b signaling rejects peer-reflexive candidates.
type CandidateSnapshot struct {
	Type           ice.CandidateType
	Network        icesession.NetworkType
	Address        netip.Addr
	Port           uint16
	RelatedAddress netip.Addr
	RelatedPort    uint16
	Foundation     string
	Priority       uint32
	Component      uint16
}

func candidateSnapshot(candidate ice.Candidate, allowLoopback bool) (CandidateSnapshot, error) {
	if candidate == nil {
		return CandidateSnapshot{}, fmt.Errorf("nil Pion candidate")
	}
	result := CandidateSnapshot{
		Type: candidate.Type(), Foundation: candidate.Foundation(), Priority: candidate.Priority(),
		Component: candidate.Component(),
	}
	switch result.Type {
	case ice.CandidateTypeHost, ice.CandidateTypeServerReflexive, ice.CandidateTypePeerReflexive:
	default:
		return CandidateSnapshot{}, fmt.Errorf("unsupported Pion candidate type %s", result.Type)
	}
	switch candidate.NetworkType() {
	case ice.NetworkTypeUDP4:
		result.Network = icesession.NetworkUDP4
	case ice.NetworkTypeUDP6:
		result.Network = icesession.NetworkUDP6
	default:
		return CandidateSnapshot{}, fmt.Errorf("unsupported Pion candidate network %s", candidate.NetworkType())
	}
	address, err := netip.ParseAddr(candidate.Address())
	if err != nil || !validCandidateAddress(address, result.Network, allowLoopback) {
		return CandidateSnapshot{}, fmt.Errorf("invalid Pion candidate address")
	}
	result.Address = address
	if candidate.Port() <= 0 || candidate.Port() > 65535 {
		return CandidateSnapshot{}, fmt.Errorf("invalid Pion candidate port")
	}
	result.Port = uint16(candidate.Port())
	if result.Component != 1 {
		return CandidateSnapshot{}, fmt.Errorf("invalid Pion candidate component")
	}
	if len(result.Foundation) == 0 || len(result.Foundation) > maxCandidateFoundationBytes ||
		!utf8.ValidString(result.Foundation) {
		return CandidateSnapshot{}, fmt.Errorf("invalid Pion candidate foundation")
	}
	for _, character := range result.Foundation {
		if character < 0x20 || character > 0x7e {
			return CandidateSnapshot{}, fmt.Errorf("invalid Pion candidate foundation")
		}
	}
	if related := candidate.RelatedAddress(); related != nil {
		if related.Address != "" || related.Port != 0 {
			result.RelatedAddress, err = netip.ParseAddr(related.Address)
			if err != nil || !validCandidateAddress(result.RelatedAddress, result.Network, allowLoopback) ||
				related.Port <= 0 || related.Port > 65535 {
				return CandidateSnapshot{}, fmt.Errorf("invalid Pion related endpoint")
			}
			result.RelatedPort = uint16(related.Port)
		}
	}
	if result.Type == ice.CandidateTypeServerReflexive && (!result.RelatedAddress.IsValid() || result.RelatedPort == 0) {
		return CandidateSnapshot{}, fmt.Errorf("server-reflexive candidate has no related endpoint")
	}
	return result, nil
}

func validCandidateAddress(address netip.Addr, network icesession.NetworkType, allowLoopback bool) bool {
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.Is4In6() ||
		address.IsLinkLocalUnicast() || (!allowLoopback && address.IsLoopback()) {
		return false
	}
	return address.Is4() == (network == icesession.NetworkUDP4)
}

func (candidate CandidateSnapshot) endpoint() netip.AddrPort {
	return netip.AddrPortFrom(candidate.Address, candidate.Port)
}

func (candidate CandidateSnapshot) signaled() (icesession.Candidate, bool) {
	result := icesession.Candidate{
		Network: candidate.Network, Address: candidate.Address, Port: candidate.Port,
		RelatedAddress: candidate.RelatedAddress, RelatedPort: candidate.RelatedPort,
		Foundation: candidate.Foundation, Priority: candidate.Priority, Component: candidate.Component,
	}
	switch candidate.Type {
	case ice.CandidateTypeHost:
		result.Type = icesession.CandidateTypeHost
	case ice.CandidateTypeServerReflexive:
		result.Type = icesession.CandidateTypeServerReflexive
	default:
		return icesession.Candidate{}, false
	}
	return result, true
}
