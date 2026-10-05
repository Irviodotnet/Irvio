// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesession

import (
	"fmt"
	"net/netip"

	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

// CandidateFromPion converts a supported detached Pion candidate.
func CandidateFromPion(candidate ice.Candidate, config ValidationConfig) (Candidate, error) {
	if candidate == nil {
		return Candidate{}, fmt.Errorf("%w: nil Pion candidate", ErrInvalidDescription)
	}
	result := Candidate{
		Foundation: candidate.Foundation(), Priority: candidate.Priority(),
		Component: candidate.Component(), Port: uint16(candidate.Port()),
	}
	switch candidate.Type() {
	case ice.CandidateTypeHost:
		result.Type = CandidateTypeHost
	case ice.CandidateTypeServerReflexive:
		result.Type = CandidateTypeServerReflexive
	default:
		return Candidate{}, fmt.Errorf("%w: unsupported Pion candidate type %s", ErrInvalidDescription, candidate.Type())
	}
	switch candidate.NetworkType() {
	case ice.NetworkTypeUDP4:
		result.Network = NetworkUDP4
	case ice.NetworkTypeUDP6:
		result.Network = NetworkUDP6
	default:
		return Candidate{}, fmt.Errorf("%w: unsupported Pion candidate network %s", ErrInvalidDescription, candidate.NetworkType())
	}
	address, err := netip.ParseAddr(candidate.Address())
	if err != nil {
		return Candidate{}, fmt.Errorf("%w: parse Pion candidate address", ErrInvalidDescription)
	}
	result.Address = address
	if related := candidate.RelatedAddress(); related != nil {
		result.RelatedAddress, err = netip.ParseAddr(related.Address)
		if err != nil || related.Port <= 0 || related.Port > 65535 {
			return Candidate{}, fmt.Errorf("%w: invalid Pion related endpoint", ErrInvalidDescription)
		}
		result.RelatedPort = uint16(related.Port)
	}
	if candidate.Port() <= 0 || candidate.Port() > 65535 {
		return Candidate{}, fmt.Errorf("%w: invalid Pion candidate port", ErrInvalidDescription)
	}
	if err := validateCandidate(result, config.AllowLoopback); err != nil {
		return Candidate{}, err
	}
	return result, nil
}

// CandidateToPion reconstructs a detached candidate using official constructors.
func CandidateToPion(candidate Candidate, config ValidationConfig) (ice.Candidate, error) {
	if err := validateCandidate(candidate, config.AllowLoopback); err != nil {
		return nil, err
	}
	network := "udp4"
	if candidate.Network == NetworkUDP6 {
		network = "udp6"
	}
	switch candidate.Type {
	case CandidateTypeHost:
		return ice.NewCandidateHost(&ice.CandidateHostConfig{
			Network: network, Address: candidate.Address.String(), Port: int(candidate.Port),
			Component: candidate.Component, Priority: candidate.Priority, Foundation: candidate.Foundation,
		})
	case CandidateTypeServerReflexive:
		return ice.NewCandidateServerReflexive(&ice.CandidateServerReflexiveConfig{
			Network: network, Address: candidate.Address.String(), Port: int(candidate.Port),
			Component: candidate.Component, Priority: candidate.Priority, Foundation: candidate.Foundation,
			RelAddr: candidate.RelatedAddress.String(), RelPort: int(candidate.RelatedPort),
		})
	default:
		return nil, fmt.Errorf("%w: unsupported candidate type", ErrInvalidDescription)
	}
}

func candidateFromObservation(observation SrflxObservation, hosts []Candidate, config ValidationConfig) (Candidate, bool, error) {
	if !observation.LocalEndpoint.IsValid() || !observation.MappedEndpoint.IsValid() ||
		observation.LocalEndpoint.Port() == 0 || observation.MappedEndpoint.Port() == 0 {
		return Candidate{}, false, fmt.Errorf("%w: invalid srflx observation", ErrInvalidDescription)
	}
	wantNetwork := networkForFamily(observation.Family)
	if observation.Family != socketmux.IPv4 && observation.Family != socketmux.IPv6 ||
		(observation.LocalEndpoint.Addr().Is4() != (wantNetwork == NetworkUDP4)) ||
		(observation.MappedEndpoint.Addr().Is4() != (wantNetwork == NetworkUDP4)) ||
		observation.LocalEndpoint.Addr().Is4In6() || observation.MappedEndpoint.Addr().Is4In6() {
		return Candidate{}, false, fmt.Errorf("%w: srflx observation family mismatch", ErrInvalidDescription)
	}
	if observation.LocalEndpoint == observation.MappedEndpoint {
		return Candidate{}, true, nil
	}
	var host *Candidate
	for index := range hosts {
		if hosts[index].Type == CandidateTypeHost && hosts[index].Network == wantNetwork &&
			hosts[index].Address == observation.LocalEndpoint.Addr() && hosts[index].Port == observation.LocalEndpoint.Port() {
			host = &hosts[index]
			break
		}
	}
	if host == nil {
		return Candidate{}, false, fmt.Errorf("%w: srflx observation has no matching host candidate", ErrInvalidDescription)
	}
	network := "udp4"
	if wantNetwork == NetworkUDP6 {
		network = "udp6"
	}
	pionCandidate, err := ice.NewCandidateServerReflexive(&ice.CandidateServerReflexiveConfig{
		Network: network, Address: observation.MappedEndpoint.Addr().String(), Port: int(observation.MappedEndpoint.Port()),
		Component: 1, RelAddr: host.Address.String(), RelPort: int(host.Port),
	})
	if err != nil {
		return Candidate{}, false, fmt.Errorf("construct Pion srflx candidate: %w", err)
	}
	result, err := CandidateFromPion(pionCandidate, config)
	return result, false, err
}
