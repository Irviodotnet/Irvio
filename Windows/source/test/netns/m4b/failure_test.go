// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/natprobe"
)

func TestM4B_BasicBindingThroughTranslatedPort(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "basic-nat-port-translation")
	topology := lab.buildNAT44()
	server := lab.startResponder("server-v4", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
	result, err := lab.runProbe("basic-nat", topology.ClientNamespace, probeOptions{
		Mode: "basic", BindIPv4: topology.ClientIPv4, Port: clientMuxPort,
		ServerIPv4: endpointValue(topology.ServerA1, stunPrimaryPort),
		InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2,
	}, func(ready probeReady) {
		lab.installNFT(topology.RouterNamespace, "basic-nat", controlledNATRules(topology, mappingEIM, filteringEIF, ready.Port))
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.BasicIPv4 == nil {
		t.Fatal("missing Basic Binding result")
	}
	wantMapped := endpointValue(topology.RouterOutside, externalPortOne)
	if result.BasicIPv4.Local.Port() != clientMuxPort || result.BasicIPv4.Mapped != wantMapped {
		t.Fatalf("Basic Binding local=%s mapped=%s, want internal port %d and external %s", result.BasicIPv4.Local, result.BasicIPv4.Mapped, clientMuxPort, wantMapped)
	}
	observations := mustObservations(t, server.observationPath)
	if len(observations) == 0 || observations[0].Source != wantMapped {
		t.Fatalf("server oracle = %+v, want source %s", observations, wantMapped)
	}
}

func TestM4B_DoubleNATSmoke(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "double-nat")
	topology := lab.buildDoubleNAT44()
	server := lab.startResponder("server-v4", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
	result, err := lab.runProbe("double-nat-profile", topology.ClientNamespace, probeOptions{
		Mode: "profile", BindIPv4: topology.ClientIPv4, Port: clientMuxPort,
		ServerIPv4: endpointValue(topology.ServerA1, stunPrimaryPort),
		InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2, Pacing: 20 * time.Millisecond,
	}, func(ready probeReady) {
		routerOne, routerTwo := doubleNATRules(topology, ready.Port)
		lab.installNFT(topology.RouterOne, "router-one-nat", routerOne)
		lab.installNFT(topology.RouterTwo, "router-two-nat", routerTwo)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProfile(t, result.ProfileIPv4, natprobe.MappingEndpointIndependent, natprobe.FilteringEndpointIndependent, natprobe.TranslationPresent)
	observations := mustObservations(t, server.observationPath)
	assertServerOracle(t, result.ProfileIPv4, observations, clientMuxPort, externalPortOne)
	if result.ProfileIPv4.InitialMappedEndpoint.Addr() != topology.RouterTwoOutside {
		t.Fatalf("double NAT mapped address = %s, want outer router %s", result.ProfileIPv4.InitialMappedEndpoint, topology.RouterTwoOutside)
	}
	lab.captureNamespace(topology.RouterOne, "router-one", true)
	lab.captureNamespace(topology.RouterTwo, "router-two", true)
}

func TestM4B_PrimaryOutageAfterBaselineIsInconclusive(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "primary-outage")
	topology := lab.buildDirectDualStack()
	baselineReady := filepath.Join(lab.artifactDir, "baseline-ready")
	baselineContinue := filepath.Join(lab.artifactDir, "baseline-continue")
	server := lab.startResponder("server-v4", topology.ServerNamespace, topology.ServerIPv4A1, topology.ServerIPv4A2,
		"--baseline-ready-file", baselineReady, "--baseline-continue-file", baselineContinue)
	injection := make(chan error, 1)
	result, err := lab.runProbe("outage-profile", topology.ClientNamespace, probeOptions{
		Mode: "profile", BindIPv4: topology.ClientIPv4, Port: clientMuxPort,
		ServerIPv4: endpointValue(topology.ServerIPv4A1, stunPrimaryPort),
		InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2, Pacing: 20 * time.Millisecond,
	}, func(probeReady) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if err := waitForFile(ctx, baselineReady); err != nil {
				injection <- err
				return
			}
			injection <- server.child.stop()
		}()
	})
	if err != nil {
		t.Fatal(err)
	}
	if injectErr := <-injection; injectErr != nil {
		t.Fatalf("inject responder outage: %v", injectErr)
	}
	if result.ProfileIPv4 == nil || result.ProfileIPv4.Status == natprobe.ProfileComplete ||
		result.ProfileIPv4.Filtering != natprobe.FilteringUnknown || result.ProfileIPv4.Reason != natprobe.ReasonLivenessFailed {
		t.Fatalf("server outage produced a false filtering conclusion: %+v", result.ProfileIPv4)
	}
}

func TestM4B_RouteRemovalTerminatesProfile(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "route-removal")
	topology := lab.buildNAT44()
	baselineReady := filepath.Join(lab.artifactDir, "baseline-ready")
	baselineContinue := filepath.Join(lab.artifactDir, "baseline-continue")
	lab.startResponder("server-v4", topology.ServerNamespace, topology.ServerA1, topology.ServerA2,
		"--baseline-ready-file", baselineReady, "--baseline-continue-file", baselineContinue)
	injection := make(chan error, 1)
	result, err := lab.runProbe("route-profile", topology.ClientNamespace, probeOptions{
		Mode: "profile", BindIPv4: topology.ClientIPv4, Port: clientMuxPort,
		ServerIPv4: endpointValue(topology.ServerA1, stunPrimaryPort), Timeout: 4 * time.Second,
		InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2, Pacing: 20 * time.Millisecond,
	}, func(ready probeReady) {
		lab.installNFT(topology.RouterNamespace, "controlled-nat", controlledNATRules(topology, mappingEIM, filteringEIF, ready.Port))
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if err := waitForFile(ctx, baselineReady); err != nil {
				injection <- err
				return
			}
			if _, err := suite.command(ctx, "ip", "netns", "exec", topology.ClientNamespace,
				"ip", "route", "delete", "default"); err != nil {
				injection <- err
				return
			}
			injection <- touch(baselineContinue)
		}()
	})
	if injectErr := <-injection; injectErr != nil {
		t.Fatalf("inject route removal: %v", injectErr)
	}
	if err != nil && result.ProfileIPv4 == nil {
		t.Fatalf("route failure returned no bounded diagnostic result: %v", err)
	}
	if result.ProfileIPv4 != nil && result.ProfileIPv4.Status == natprobe.ProfileComplete {
		t.Fatalf("route removal produced false complete profile: %+v", *result.ProfileIPv4)
	}
}

func TestM4B_IPv6RouteFailureDoesNotBreakIPv4(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "dual-route-failure")
	topology := lab.buildDirectDualStack()
	server4 := lab.startResponder("server-v4", topology.ServerNamespace, topology.ServerIPv4A1, topology.ServerIPv4A2)
	baselineReady := filepath.Join(lab.artifactDir, "v6-baseline-ready")
	baselineContinue := filepath.Join(lab.artifactDir, "v6-baseline-continue")
	lab.startResponder("server-v6", topology.ServerNamespace, topology.ServerIPv6A1, topology.ServerIPv6A2,
		"--baseline-ready-file", baselineReady, "--baseline-continue-file", baselineContinue)
	injection := make(chan error, 1)
	result, err := lab.runProbe("dual-route-profile", topology.ClientNamespace, probeOptions{
		Mode: "profile-dual", BindIPv4: topology.ClientIPv4, BindIPv6: topology.ClientIPv6,
		Port: clientMuxPort, ServerIPv4: endpointValue(topology.ServerIPv4A1, stunPrimaryPort),
		ServerIPv6: endpointValue(topology.ServerIPv6A1, stunPrimaryPort), Timeout: 5 * time.Second,
		InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2, Pacing: 20 * time.Millisecond,
	}, func(probeReady) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if err := waitForFile(ctx, baselineReady); err != nil {
				injection <- err
				return
			}
			prefix := netip.PrefixFrom(topology.ClientIPv6, 64).Masked().String()
			if _, err := suite.command(ctx, "ip", "netns", "exec", topology.ClientNamespace,
				"ip", "-6", "route", "delete", prefix, "dev", topology.ClientInterface); err != nil {
				injection <- err
				return
			}
			injection <- touch(baselineContinue)
		}()
	})
	if injectErr := <-injection; injectErr != nil {
		t.Fatalf("inject IPv6 route failure: %v", injectErr)
	}
	if result.ProfileIPv4 == nil || result.ProfileIPv4.Status != natprobe.ProfileComplete {
		t.Fatalf("concurrent IPv4 profile was affected: %+v, err=%v", result.ProfileIPv4, err)
	}
	if result.ProfileIPv6 != nil && result.ProfileIPv6.Status == natprobe.ProfileComplete {
		t.Fatalf("IPv6 route removal produced false complete profile: %+v", *result.ProfileIPv6)
	}
	observations := mustObservations(t, server4.observationPath)
	assertServerOracle(t, result.ProfileIPv4, observations, clientMuxPort, clientMuxPort)
}

func TestM4B_ArtifactDirectoryIsBoundedAndPrivate(t *testing.T) {
	requirePrivileged(t)
	info, err := os.Stat(suite.artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		t.Fatalf("artifact directory is group/world writable: %v", info.Mode())
	}
}
