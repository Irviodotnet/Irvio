// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

func writeCountryDatabase(t *testing.T, path, country, registered string, build int64) {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch: build, DatabaseType: "GeoIP2-Country-Irvio-Test", IPVersion: 6,
		RecordSize: 24, IncludeReservedNetworks: true,
		Description: map[string]string{"en": "Synthetic Irvio test fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, network, err := net.ParseCIDR("198.51.100.0/24")
	if err != nil {
		t.Fatal(err)
	}
	record := mmdbtype.Map{
		"country":            mmdbtype.Map{"iso_code": mmdbtype.String(country)},
		"registered_country": mmdbtype.Map{"iso_code": mmdbtype.String(registered)},
	}
	if err := tree.Insert(network, record); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.WriteTo(file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMMDBResolverUsesCountryAndHotReloadsAtomically(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.mmdb")
	second := filepath.Join(directory, "second.mmdb")
	writeCountryDatabase(t, first, "DE", "US", 100)
	writeCountryDatabase(t, second, "NL", "CA", 200)
	resolver, err := OpenMMDB(first)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()

	address := netip.MustParseAddr("198.51.100.42")
	observation, err := resolver.Resolve(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Country != "DE" || observation.State != CountryObserved {
		t.Fatalf("observation = %+v; registered_country must not replace country", observation)
	}
	if err := resolver.Reload(second); err != nil {
		t.Fatal(err)
	}
	observation, err = resolver.Resolve(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Country != "NL" || observation.DatabaseBuild.Unix() != 200 {
		t.Fatalf("reloaded observation = %+v", observation)
	}

	corrupt := filepath.Join(directory, "corrupt.mmdb")
	if err := os.WriteFile(corrupt, []byte("not an mmdb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := resolver.Reload(corrupt); err == nil {
		t.Fatal("corrupt reload succeeded")
	}
	if !resolver.Snapshot().Degraded {
		t.Fatal("failed reload did not report degraded state")
	}
	observation, err = resolver.Resolve(context.Background(), address)
	if err != nil {
		t.Fatalf("last valid snapshot was lost: %v", err)
	}
	if observation.Country != "NL" {
		t.Fatalf("fallback snapshot = %+v", observation)
	}
}
