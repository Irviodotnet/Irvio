// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package serviceindex maps canonical IRVIO services to synthetic provider CIDs.
package serviceindex

import (
	"fmt"
	"strings"

	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
)

const (
	ExitPrefix       = "hydra/v1/exit/"
	Rendezvous       = "hydra/v1/rendezvous"
	RendezvousPrefix = "hydra/v1/rendezvous/"
	STUNBasic        = "hydra/v1/stun/basic"
	STUNRFC5780      = "hydra/v1/stun/rfc5780"
	serviceMaxBytes  = 256
)

// ServiceCID deterministically hashes an exact versioned service string into a raw CIDv1.
func ServiceCID(service string) (cid.Cid, error) {
	if service == "" || len(service) > serviceMaxBytes {
		return cid.Undef, fmt.Errorf("service must contain 1..%d bytes", serviceMaxBytes)
	}
	if !strings.HasPrefix(service, "hydra/v1/") {
		return cid.Undef, fmt.Errorf("service %q is outside the hydra/v1 namespace", service)
	}
	hash, err := multihash.Sum([]byte(service), multihash.SHA2_256, -1)
	if err != nil {
		return cid.Undef, fmt.Errorf("hash service: %w", err)
	}
	return cid.NewCidV1(cid.Raw, hash), nil
}

// NormalizeCountry validates an alpha-2-like country and returns uppercase form.
func NormalizeCountry(country string) (string, error) {
	if len(country) != 2 {
		return "", fmt.Errorf("country must be exactly two ASCII letters")
	}
	normalized := strings.ToUpper(country)
	for _, character := range normalized {
		if character < 'A' || character > 'Z' {
			return "", fmt.Errorf("country must be exactly two ASCII letters")
		}
	}
	return normalized, nil
}

// ExitCID returns the synthetic provider CID for a normalized exit country.
func ExitCID(country string) (cid.Cid, string, error) {
	normalized, err := NormalizeCountry(country)
	if err != nil {
		return cid.Undef, "", err
	}
	service := ExitPrefix + normalized
	value, err := ServiceCID(service)
	if err != nil {
		return cid.Undef, "", err
	}
	return value, service, nil
}

// RendezvousCID returns the general or observed-country Rendezvous service.
func RendezvousCID(country string) (cid.Cid, string, error) {
	service := Rendezvous
	if country != "" {
		normalized, err := NormalizeCountry(country)
		if err != nil {
			return cid.Undef, "", err
		}
		service = RendezvousPrefix + normalized
	}
	value, err := ServiceCID(service)
	if err != nil {
		return cid.Undef, "", err
	}
	return value, service, nil
}
