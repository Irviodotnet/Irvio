// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package main

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

type productUnderlay struct {
	IfName string
	Source netip.Addr
}

func resolveProductDaemonWithHost(options productOptions, addresses []string, country exitselector.CountryIntent, pool []netip.AddrPort, path productUnderlay, exitPool, tunAddress netip.Prefix, bypass []netip.Prefix) (irvioruntime.DaemonConfig, error) {
	targets := make(irvioruntime.StaticTargets, 0, len(addresses))
	for _, address := range addresses {
		target, parseErr := parseUnderlayTarget(address)
		if parseErr == nil {
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return irvioruntime.DaemonConfig{}, fmt.Errorf("bootstrap bundle contains no usable underlay target")
	}
	production := &irvioruntime.ProductionConfig{
		STUNIPv4Pool: pool, TUNName: productTUNName(),
		TunnelDNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		Bypass:           bypass, Obfuscation: irvioruntime.DefaultProductionObfuscation(), ConnectCountry: country,
		ExitWANName: path.IfName, ExitTUNAddresses: []netip.Prefix{tunAddress}, ExitIPv4Pool: exitPool,
		ExitManagement: bypass, LeaseTTL: tunnellease.DefaultTTL, LeaseSweep: time.Second,
	}
	server := options.role == productruntime.Server
	proofTTL, reverifyLead, retryDelay := 5*time.Minute, time.Minute, time.Minute
	if options.publicProofTTL != 0 {
		proofTTL = options.publicProofTTL
	}
	if options.publicReverify != 0 {
		reverifyLead = options.publicReverify
	}
	if options.publicRetry != 0 {
		retryDelay = options.publicRetry
	}
	geoipDB, err := productGeoIPPath(options)
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	listenPort := 0
	if server {
		listenPort = 4000
		production.UDPPort = productruntime.ServerUDPPort
		production.RequireFixedUDPPort = true
	} else {
		listenPort, err = productruntime.ResolveListenPort(options.dataDir)
		if err != nil {
			return irvioruntime.DaemonConfig{}, err
		}
	}
	config := irvioruntime.DaemonConfig{
		Platform: productDaemonPlatform(options.platform),
		DataDir:  options.dataDir, ListenPort: listenPort, Bootstrap: addresses,
		Exit: true, DHTMode: "server", Logger: options.logger, ProtectedMode: !server, ProtectAtStart: !server,
		Production: production, UnderlayTargets: targets,
		AutoExitCountry: true, RequireExitCountry: !server,
		PublicSTUN: server, PublicSTUNBind: path.Source,
		PublicRole: irvioruntime.PublicRoleConfig{Requested: server, DiscoverVerifiers: server,
			ProofTTL: proofTTL, ReverifyLead: reverifyLead, RetryDelay: retryDelay,
			OperationTimeout: 10 * time.Second, Logger: options.logger},
		M12: irvioruntime.M12Config{Enabled: true, Serve: server,
			GeoIPDatabase: geoipDB, SessionControlPort: 51822,
			Policy: rendezvous.DefaultConfig()},
	}
	if options.allowTest {
		config.PublicRole.AllowTestAddresses = true
		config.M12.AllowTestAddresses = true
	}
	return config, nil
}
