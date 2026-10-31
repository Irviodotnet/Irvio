// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/productruntime"
)

func TestWindowsProductServerPreset(t *testing.T) {
	paths, err := productruntime.WindowsDefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	options, err := resolveProductOptions(productOptions{role: productruntime.Server})
	if err != nil {
		t.Fatal(err)
	}
	pool := []netip.AddrPort{netip.MustParseAddrPort("8.8.8.8:3478"), netip.MustParseAddrPort("1.1.1.1:3478")}
	path := productUnderlay{IfName: "test-kernel-route", Source: netip.MustParseAddr("192.0.2.17")}
	addresses := []string{"/ip4/8.8.8.8/tcp/4000/p2p/12D3KooWMRgtTKXdcPTgVDrrevbP8EFLQXAeQEjrTiGkk6b3vn6k"}
	config, err := resolveProductDaemonWithHost(options, addresses, "", pool, path,
		netip.MustParsePrefix("10.203.0.0/24"), netip.MustParsePrefix("10.203.0.1/24"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if options.dataDir != paths.Profile || config.M12.GeoIPDatabase != paths.GeoIP || config.Platform.JournalDirectory != filepath.Join(options.platform.runtime, "network") {
		t.Fatal("server did not use canonical Windows paths")
	}
	if config.ListenPort != 4000 || config.DHTMode != "server" || !config.Exit || !config.PublicSTUN || config.PublicSTUNBind != path.Source || !config.M12.Enabled || !config.M12.Serve {
		t.Fatal("incomplete headless server preset")
	}
	if !config.PublicRole.Requested || !config.PublicRole.DiscoverVerifiers || config.PublicRole.AllowTestAddresses || config.M12.AllowTestAddresses || len(config.PublicRole.VerifierAddresses) != 0 {
		t.Fatal("server bypassed production verifier discovery")
	}
	if !config.AutoExitCountry || config.ProtectedMode || config.ProtectAtStart || config.Production.ConnectCountry != "" {
		t.Fatal("server got desktop client intent or self-declared country")
	}
	if config.Production.UDPPort != 51820 || !config.Production.RequireFixedUDPPort || config.Production.TUNName != "irvio0" || config.Production.ExitWANName != path.IfName || len(config.Production.STUNIPv4Pool) != len(pool) {
		t.Fatal("server ports, kernel underlay or redundant STUN pool lost")
	}
}
