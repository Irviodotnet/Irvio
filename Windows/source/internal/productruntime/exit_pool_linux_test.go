// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package productruntime

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestExitPoolSkipsHostRouteCollisionsFromProfileSeed(t *testing.T) {
	occupied := []netip.Prefix{
		netip.MustParsePrefix("10.201.0.0/16"),
		netip.MustParsePrefix("10.202.1.0/24"),
	}
	pool, err := selectExitPoolFrom(occupied, 201*256+1)
	if err != nil {
		t.Fatal(err)
	}
	if want := netip.MustParsePrefix("10.202.2.0/24"); pool != want {
		t.Fatalf("pool=%s, want %s", pool, want)
	}
	if want := netip.MustParsePrefix("10.202.2.1/24"); exitTUNAddress(pool) != want {
		t.Fatalf("TUN address=%s, want %s", exitTUNAddress(pool), want)
	}
	if !allowedExitPool(pool) || allowedExitPool(netip.MustParsePrefix("8.8.8.0/24")) {
		t.Fatal("persisted pool validation changed")
	}
}

func TestExitPoolDifferentProfileSeedsDoNotStartAtSamePool(t *testing.T) {
	first, err := selectExitPoolFrom(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := selectExitPoolFrom(nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first != netip.MustParsePrefix("10.1.0.0/24") || second != netip.MustParsePrefix("10.1.1.0/24") {
		t.Fatalf("distinct seeds selected %s and %s", first, second)
	}
}

func TestExitPoolPersistsAcrossResolution(t *testing.T) {
	directory := t.TempDir()
	first, tun, err := ResolveExitPool(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, secondTUN, err := ResolveExitPool(directory)
	if err != nil || first != second || tun != secondTUN {
		t.Fatalf("persistent pool changed: %s/%s → %s/%s, %v", first, tun, second, secondTUN, err)
	}
	info, err := os.Stat(filepath.Join(directory, ExitPoolFilename))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("persisted pool mode=%v", info.Mode())
	}
}
