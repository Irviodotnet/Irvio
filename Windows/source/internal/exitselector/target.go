// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import "github.com/libp2p/go-libp2p/core/peer"

func resolveTarget(target Target, legacyCountry CountryIntent) (Target, error) {
	switch target.Kind {
	case TargetPublicCountry:
		if target.PeerID != "" {
			return Target{}, ErrInvalidConfig
		}
		if target.Country == "" {
			target.Country = legacyCountry
		}
		country, err := normalizeCountry(target.Country)
		if err != nil {
			return Target{}, err
		}
		if legacyCountry != "" {
			legacy, err := normalizeCountry(legacyCountry)
			if err != nil || legacy != country {
				return Target{}, ErrInvalidConfig
			}
		}
		target.Country = country
	case TargetPrivatePeer:
		_, err := peer.IDFromBytes([]byte(target.PeerID))
		if target.Country != "" || legacyCountry != "" || err != nil {
			return Target{}, ErrInvalidConfig
		}
	default:
		return Target{}, ErrInvalidConfig
	}
	return target, nil
}
