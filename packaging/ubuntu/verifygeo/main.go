// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// verifygeo is a build-time read-only check. It is not shipped or run by hydrad.
package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"hydra.local/hydra/internal/rendezvous"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: verifygeo COUNTRY_MMDB")
		os.Exit(2)
	}
	r, err := rendezvous.OpenMMDB(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer r.Close()
	snapshot := r.Snapshot()
	if !strings.Contains(strings.ToLower(snapshot.DatabaseType), "dbip") || snapshot.Degraded {
		panic("expected a valid DB-IP database")
	}
	observation, err := r.Resolve(context.Background(), netip.MustParseAddr("8.8.8.8"))
	if err != nil || len(observation.Country) != 2 {
		panic("GeoIP lookup through the existing CountryResolver failed")
	}
	fmt.Printf("GeoIP check PASS: type=%s build=%s\n", snapshot.DatabaseType, snapshot.DatabaseBuild)
}
