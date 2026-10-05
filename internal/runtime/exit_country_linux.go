// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"fmt"

	"hydra.local/hydra/internal/rendezvous"
	"hydra.local/hydra/internal/transportgen"
)

func observedExitCountry(ctx context.Context, generation *transportgen.Generation, database string) (string, error) {
	if generation == nil || database == "" {
		return "", fmt.Errorf("GeoIP database or active transport generation is unavailable")
	}
	resolver, err := rendezvous.OpenMMDB(database)
	if err != nil {
		return "", err
	}
	defer resolver.Close()
	for _, observation := range generation.Observations() {
		if !observation.MappedEndpoint.Addr().Is4() {
			continue
		}
		country, err := resolver.Resolve(ctx, observation.MappedEndpoint.Addr())
		if err == nil && country.State == rendezvous.CountryObserved {
			return country.Country, nil
		}
	}
	return "", fmt.Errorf("no observed public IPv4 endpoint has a known GeoIP country")
}
