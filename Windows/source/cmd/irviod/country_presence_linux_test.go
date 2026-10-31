// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/rendezvous"
)

func TestCountryPresenceSurvivesReadySubsetAndExpires(t *testing.T) {
	start := time.Now()
	var view countryPresenceView
	sample := func(seconds int, codes ...string) rendezvous.AvailableCountries {
		now := start.Add(time.Duration(seconds) * time.Second)
		return view.observe(rendezvous.AvailableCountries{Status: rendezvous.AvailableCountriesReady, Codes: codes, GeneratedAt: now}, now)
	}
	sample(0, "CZ", "DE", "NL")
	got := sample(11, "CZ", "NL")
	if !reflect.DeepEqual(got.Codes, []string{"CZ", "DE", "NL"}) || got.Status != rendezvous.AvailableCountriesPartial {
		t.Fatalf("cold discovery subset flickered: %+v", got)
	}
	if got := sample(22, "CZ", "DE", "NL"); got.Status != rendezvous.AvailableCountriesReady {
		t.Fatalf("fresh confirmation did not restore READY: %+v", got)
	}
	sample(33, "CZ", "NL")
	sample(44, "CZ", "NL")
	if got := sample(52, "CZ", "NL"); !reflect.DeepEqual(got.Codes, []string{"CZ", "NL"}) {
		t.Fatalf("missing country retained beyond grace: %+v", got)
	}
}

func TestCountryPresenceCachedReadsNeverExtendLifetime(t *testing.T) {
	start := time.Now()
	var view countryPresenceView
	raw := rendezvous.AvailableCountries{Status: rendezvous.AvailableCountriesReady, Codes: []string{"DE"}, GeneratedAt: start}
	view.observe(raw, start)
	for seconds := 1; seconds <= 30; seconds++ {
		got := view.observe(raw, start.Add(time.Duration(seconds)*time.Second))
		if seconds == 30 && (len(got.Codes) != 0 || got.Status != rendezvous.AvailableCountriesUnavailable) {
			t.Fatalf("cached sample refreshed stale presence: %+v", got)
		}
	}
	if !reflect.DeepEqual(raw.Codes, []string{"DE"}) {
		t.Fatal("view mutated raw sample")
	}
}

func TestCountryPresenceOutageAndReset(t *testing.T) {
	start := time.Now()
	for _, status := range []rendezvous.AvailableCountriesStatus{rendezvous.AvailableCountriesEmpty, rendezvous.AvailableCountriesUnavailable, rendezvous.AvailableCountriesPartial} {
		var view countryPresenceView
		view.observe(rendezvous.AvailableCountries{Status: rendezvous.AvailableCountriesReady, Codes: []string{"DE"}, GeneratedAt: start}, start)
		for _, seconds := range []int{10, 20, 30} {
			now := start.Add(time.Duration(seconds) * time.Second)
			got := view.observe(rendezvous.AvailableCountries{Status: status, GeneratedAt: now}, now)
			if seconds < 30 && (len(got.Codes) != 1 || got.Status != rendezvous.AvailableCountriesPartial) {
				t.Fatalf("transient lost presence: %+v", got)
			}
			if seconds == 30 && len(got.Codes) != 0 {
				t.Fatalf("outage resurrected stale country: %+v", got)
			}
		}
		view = countryPresenceView{}
		if got := view.observe(rendezvous.AvailableCountries{Status: status, GeneratedAt: start}, start); len(got.Codes) != 0 {
			t.Fatal("profile reset retained country")
		}
	}
}
