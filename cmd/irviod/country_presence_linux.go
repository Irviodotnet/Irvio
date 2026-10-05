// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"sort"
	"time"

	"github.com/Irviodotnet/Irvio/internal/rendezvous"
)

const countryPresenceGrace = 30 * time.Second

// This display-only view survives bounded idle probes; it never supplies Exit candidates.
type countryPresenceView struct {
	lastSample time.Time
	seen       map[string]time.Time
}

func (v *countryPresenceView) observe(sample rendezvous.AvailableCountries, now time.Time) rendezvous.AvailableCountries {
	if v.seen == nil {
		v.seen = make(map[string]time.Time)
	}
	current := make(map[string]bool, len(sample.Codes))
	usable := sample.Status == rendezvous.AvailableCountriesReady || sample.Status == rendezvous.AvailableCountriesPartial
	fresh := !sample.GeneratedAt.IsZero() && sample.GeneratedAt.After(v.lastSample)
	if fresh {
		v.lastSample = sample.GeneratedAt
	}
	if usable {
		for _, code := range sample.Codes {
			current[code] = true
			if fresh {
				v.seen[code] = now
			}
		}
	}
	// READY describes responding servers, not proof that DHT found every distributor.
	codes := make([]string, 0, len(v.seen))
	retained := false
	for code, seen := range v.seen {
		if now.Sub(seen) >= countryPresenceGrace {
			delete(v.seen, code)
			continue
		}
		codes = append(codes, code)
		retained = retained || !current[code]
	}
	sort.Strings(codes)
	sample.Codes = codes
	if retained {
		sample.Status = rendezvous.AvailableCountriesPartial
	} else if len(codes) == 0 && usable {
		sample.Status = rendezvous.AvailableCountriesUnavailable
	}
	return sample
}
