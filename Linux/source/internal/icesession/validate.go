// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesession

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
)

const (
	minUfragLength     = 4
	maxUfragLength     = 256
	minPasswordLength  = 22
	maxPasswordLength  = 256
	maxFoundationBytes = 64
	maxCandidateLimit  = 64
)

// ValidateDescription validates and de-duplicates a complete description.
func ValidateDescription(description Description, config ValidationConfig) (Description, error) {
	if err := config.Validate(); err != nil {
		return Description{}, err
	}
	if !validICEText(description.Credentials.Ufrag, minUfragLength, maxUfragLength) {
		return Description{}, fmt.Errorf("%w: invalid ufrag", ErrInvalidDescription)
	}
	if !validICEText(description.Credentials.Pwd, minPasswordLength, maxPasswordLength) {
		return Description{}, fmt.Errorf("%w: invalid password", ErrInvalidDescription)
	}
	if len(description.Candidates) == 0 || len(description.Candidates) > config.MaxCandidates {
		return Description{}, fmt.Errorf("%w: candidate count %d is outside 1..%d", ErrInvalidDescription, len(description.Candidates), config.MaxCandidates)
	}
	result := Description{Credentials: description.Credentials, Candidates: make([]Candidate, 0, len(description.Candidates))}
	seen := make(map[candidateKey]struct{}, len(description.Candidates))
	for _, candidate := range description.Candidates {
		if err := validateCandidate(candidate, config.AllowLoopback); err != nil {
			return Description{}, err
		}
		key := candidate.identity()
		if _, duplicate := seen[key]; duplicate {
			return Description{}, fmt.Errorf("%w: duplicate candidate", ErrInvalidDescription)
		}
		seen[key] = struct{}{}
		result.Candidates = append(result.Candidates, candidate)
	}
	sortCandidates(result.Candidates)
	return result, nil
}

type candidateKey struct {
	typeValue CandidateType
	network   NetworkType
	address   [16]byte
	isIPv4    bool
	port      uint16
	related   [16]byte
	related4  bool
	relPort   uint16
	component uint16
}

func (c Candidate) identity() candidateKey {
	key := candidateKey{
		typeValue: c.Type, network: c.Network, port: c.Port,
		relPort: c.RelatedPort, component: c.Component,
	}
	if c.Address.Is4() {
		v := c.Address.As4()
		copy(key.address[:4], v[:])
		key.isIPv4 = true
	} else if c.Address.IsValid() {
		key.address = c.Address.As16()
	}
	if c.RelatedAddress.Is4() {
		v := c.RelatedAddress.As4()
		copy(key.related[:4], v[:])
		key.related4 = true
	} else if c.RelatedAddress.IsValid() {
		key.related = c.RelatedAddress.As16()
	}
	return key
}

func validateCandidate(candidate Candidate, allowLoopback bool) error {
	if candidate.Type != CandidateTypeHost && candidate.Type != CandidateTypeServerReflexive {
		return fmt.Errorf("%w: unsupported candidate type %d", ErrInvalidDescription, candidate.Type)
	}
	if candidate.Network != NetworkUDP4 && candidate.Network != NetworkUDP6 {
		return fmt.Errorf("%w: unsupported candidate network %d", ErrInvalidDescription, candidate.Network)
	}
	if err := validateAddress(candidate.Address, allowLoopback); err != nil {
		return err
	}
	if (candidate.Network == NetworkUDP4) != candidate.Address.Is4() {
		return fmt.Errorf("%w: candidate address family mismatch", ErrInvalidDescription)
	}
	if candidate.Port == 0 || candidate.Component != 1 || candidate.Priority == 0 {
		return fmt.Errorf("%w: invalid port, component, or priority", ErrInvalidDescription)
	}
	if len(candidate.Foundation) == 0 || len(candidate.Foundation) > maxFoundationBytes || !printableASCII(candidate.Foundation) {
		return fmt.Errorf("%w: invalid foundation", ErrInvalidDescription)
	}
	if candidate.Type == CandidateTypeHost {
		if candidate.RelatedAddress.IsValid() || candidate.RelatedPort != 0 {
			return fmt.Errorf("%w: host candidate has related endpoint", ErrInvalidDescription)
		}
		return nil
	}
	if err := validateAddress(candidate.RelatedAddress, allowLoopback); err != nil {
		return fmt.Errorf("%w: invalid related address", err)
	}
	if candidate.RelatedPort == 0 || candidate.RelatedAddress.Is4() != candidate.Address.Is4() {
		return fmt.Errorf("%w: invalid related endpoint", ErrInvalidDescription)
	}
	return nil
}

func validateAddress(address netip.Addr, allowLoopback bool) error {
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.Is4In6() || address.Is6() && address.IsLinkLocalUnicast() {
		return fmt.Errorf("%w: invalid candidate address", ErrInvalidDescription)
	}
	if address.IsLoopback() && !allowLoopback {
		return fmt.Errorf("%w: loopback candidate is disabled", ErrInvalidDescription)
	}
	return nil
}

func validICEText(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '+' || character == '/' {
			continue
		}
		return false
	}
	return true
}

func printableASCII(value string) bool {
	for index := range len(value) {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func sortCandidates(candidates []Candidate) {
	slices.SortFunc(candidates, func(left, right Candidate) int {
		if left.Network != right.Network {
			return cmp.Compare(left.Network, right.Network)
		}
		if left.Type != right.Type {
			return cmp.Compare(left.Type, right.Type)
		}
		if comparison := left.Address.Compare(right.Address); comparison != 0 {
			return comparison
		}
		return cmp.Compare(left.Port, right.Port)
	})
}

// Validate verifies the configured resource bounds.
func (c ValidationConfig) Validate() error {
	if c.MaxCandidates <= 0 || c.MaxCandidates > maxCandidateLimit {
		return fmt.Errorf("%w: candidate limit must be within 1..%d", ErrInvalidDescription, maxCandidateLimit)
	}
	return nil
}
