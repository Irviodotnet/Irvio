// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package serviceindex

import "testing"

func TestPrivateOwnerCID(t *testing.T) {
	locator := make([]byte, 32)
	locator[0] = 1
	first, err := PrivateOwnerCID(locator)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrivateOwnerCID(locator)
	if err != nil || first != second {
		t.Fatal("private CID unstable")
	}
	locator[0] = 2
	other, err := PrivateOwnerCID(locator)
	if err != nil || first == other {
		t.Fatal("locators collide")
	}
	public, _, err := ExitCID("DE")
	if err != nil || first == public {
		t.Fatal("private/public CID collision")
	}
	for _, invalid := range [][]byte{nil, make([]byte, 31), make([]byte, 32), make([]byte, 33)} {
		if _, err := PrivateOwnerCID(invalid); err == nil {
			t.Fatal("invalid locator accepted")
		}
	}
}

func TestServiceCIDDeterministicAndDistinct(t *testing.T) {
	first, err := ServiceCID("hydra/v1/exit/DE")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ServiceCID("hydra/v1/exit/DE")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("CID changed: %s != %s", first, second)
	}
	if first.String() != "bafkreiajmr3ui46d6fauwzahoqkxctq2f2cvw6u5hq74jk4ski6ls7i2ny" {
		t.Fatalf("unexpected stable CID: %s", first)
	}
	for _, service := range []string{"hydra/v1/exit/de", "hydra/v1/exit/NL"} {
		other, err := ServiceCID(service)
		if err != nil {
			t.Fatal(err)
		}
		if other == first {
			t.Fatalf("%q collided with EXIT/DE", service)
		}
	}
}

func TestCountryNormalizationAndValidation(t *testing.T) {
	got, err := NormalizeCountry("de")
	if err != nil {
		t.Fatal(err)
	}
	if got != "DE" {
		t.Fatalf("normalized country = %q", got)
	}
	for _, invalid := range []string{"", "D", "DEU", "D1", "Dé"} {
		if _, err := NormalizeCountry(invalid); err == nil {
			t.Fatalf("invalid country %q accepted", invalid)
		}
	}
}

func TestExitCIDNormalizesCountry(t *testing.T) {
	upper, service, err := ExitCID("DE")
	if err != nil {
		t.Fatal(err)
	}
	lower, _, err := ExitCID("de")
	if err != nil {
		t.Fatal(err)
	}
	if upper != lower || service != "hydra/v1/exit/DE" {
		t.Fatalf("normalization mismatch: %s %s %q", upper, lower, service)
	}
}
