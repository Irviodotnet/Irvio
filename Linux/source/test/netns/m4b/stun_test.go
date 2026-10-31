// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/natprobe"
	"github.com/Irviodotnet/Irvio/test/support/rfc5780"
)

func TestM4B_DirectDualStack(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "direct-dual-stack")
	topology := lab.buildDirectDualStack()
	server4 := lab.startResponder("server-v4", topology.ServerNamespace, topology.ServerIPv4A1, topology.ServerIPv4A2)
	server6 := lab.startResponder("server-v6", topology.ServerNamespace, topology.ServerIPv6A1, topology.ServerIPv6A2)

	result, err := lab.runProbe("profile-dual", topology.ClientNamespace, probeOptions{
		Mode: "profile-dual", BindIPv4: topology.ClientIPv4, BindIPv6: topology.ClientIPv6,
		Port:       clientMuxPort,
		ServerIPv4: endpointValue(topology.ServerIPv4A1, stunPrimaryPort),
		ServerIPv6: endpointValue(topology.ServerIPv6A1, stunPrimaryPort),
		InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2, Pacing: 20 * time.Millisecond,
	}, func(ready probeReady) {
		if ready.Port != clientMuxPort {
			t.Fatalf("mux port = %d, want %d", ready.Port, clientMuxPort)
		}
		output := lab.inNamespace(topology.ClientNamespace, "ss", "-H", "-l", "-u", "-n", "-p", fmt.Sprintf("sport = :%d", ready.Port))
		if err := os.WriteFile(filepath.Join(lab.artifactDir, "mux-sockets.txt"), []byte(output), 0o600); err != nil {
			t.Fatal(err)
		}
		lines := nonemptyLines(output)
		if len(lines) != 2 {
			t.Fatalf("physical mux socket count = %d, want one UDP4 and one UDP6: %q", len(lines), output)
		}
		if !strings.Contains(output, topology.ClientIPv4.String()) || !strings.Contains(output, topology.ClientIPv6.String()) {
			t.Fatalf("socket evidence lacks both concrete family addresses: %q", output)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProfile(t, result.ProfileIPv4, natprobe.MappingEndpointIndependent, natprobe.FilteringEndpointIndependent, natprobe.TranslationAbsent)
	assertProfile(t, result.ProfileIPv6, natprobe.MappingEndpointIndependent, natprobe.FilteringEndpointIndependent, natprobe.TranslationAbsent)
	if result.Port != clientMuxPort || result.ProfileIPv4.LocalEndpoint.Port() != result.ProfileIPv6.LocalEndpoint.Port() {
		t.Fatalf("dual-stack ports differ: mux=%d IPv4=%s IPv6=%s", result.Port, result.ProfileIPv4.LocalEndpoint, result.ProfileIPv6.LocalEndpoint)
	}
	observations4 := mustObservations(t, server4.observationPath)
	observations6 := mustObservations(t, server6.observationPath)
	assertServerOracle(t, result.ProfileIPv4, observations4, clientMuxPort, clientMuxPort)
	assertServerOracle(t, result.ProfileIPv6, observations6, clientMuxPort, clientMuxPort)
	for _, observation := range append(observations4, observations6...) {
		if observation.Source.Port() != clientMuxPort {
			t.Fatalf("direct server observed source %s, want mux port %d", observation.Source, clientMuxPort)
		}
	}

	if output := lab.inNamespace(topology.ClientNamespace, "ss", "-H", "-l", "-u", "-n", fmt.Sprintf("sport = :%d", clientMuxPort)); strings.TrimSpace(output) != "" {
		t.Fatalf("mux sockets remain after helper exit: %q", output)
	}
	second, err := lab.runProbe("basic-dual-rebind", topology.ClientNamespace, probeOptions{
		Mode: "basic-dual", BindIPv4: topology.ClientIPv4, BindIPv6: topology.ClientIPv6,
		Port: clientMuxPort, ServerIPv4: endpointValue(topology.ServerIPv4A1, stunPrimaryPort),
		ServerIPv6: endpointValue(topology.ServerIPv6A1, stunPrimaryPort),
		InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2,
	}, nil)
	if err != nil {
		t.Fatalf("immediate common-port reconstruction: %v", err)
	}
	if second.BasicIPv4 == nil || second.BasicIPv6 == nil || second.BasicIPv4.Local.Port() != clientMuxPort || second.BasicIPv6.Local.Port() != clientMuxPort {
		t.Fatalf("unexpected dual Basic Binding result after rebind: %+v", second)
	}
	lab.captureNamespace(topology.ClientNamespace, "client", false)
	lab.captureNamespace(topology.ServerNamespace, "server", false)
}

func TestM4B_SingleNATNativeObservation(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "single-native-nat")
	topology := lab.buildNAT44()
	server := lab.startResponder("server-v4", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
	result, err := lab.runProbe("native-profile", topology.ClientNamespace, probeOptions{
		Mode: "profile", BindIPv4: topology.ClientIPv4, Port: clientMuxPort,
		ServerIPv4: endpointValue(topology.ServerA1, stunPrimaryPort),
		InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2, Pacing: 20 * time.Millisecond,
	}, func(ready probeReady) {
		lab.installNFT(topology.RouterNamespace, "native-nat", nativeNATRules(topology, ready.Port))
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProfileIPv4 == nil || result.ProfileIPv4.Status != natprobe.ProfileComplete || result.ProfileIPv4.Translation != natprobe.TranslationPresent ||
		result.ProfileIPv4.Mapping == natprobe.MappingUnknown || result.ProfileIPv4.Filtering == natprobe.FilteringUnknown {
		t.Fatalf("native Linux NAT did not yield a complete measured profile: %+v", result.ProfileIPv4)
	}
	observations := mustObservations(t, server.observationPath)
	assertServerOracle(t, result.ProfileIPv4, observations, clientMuxPort, 0)
	rules := lab.inNamespace(topology.RouterNamespace, "nft", "-j", "list", "ruleset")
	requireCounter(t, rules, "internal-mux-port", 1)
	if count, ok := counterForComment(rules, "internal-bad-port"); !ok || count != 0 {
		t.Fatalf("non-mux Irvio source-port counter = %d, present=%v", count, ok)
	}
	lab.captureNamespace(topology.ClientNamespace, "client", false)
	lab.captureNamespace(topology.RouterNamespace, "router", true)
	lab.captureNamespace(topology.ServerNamespace, "server", false)
}

func TestM4B_ControlledNATMatrix(t *testing.T) {
	requirePrivileged(t)
	mappings := []struct {
		policy mappingPolicy
		want   natprobe.MappingBehavior
	}{
		{mappingEIM, natprobe.MappingEndpointIndependent},
		{mappingADM, natprobe.MappingAddressDependent},
		{mappingAPDM, natprobe.MappingAddressAndPortDependent},
	}
	filterings := []struct {
		policy filteringPolicy
		want   natprobe.FilteringBehavior
	}{
		{filteringEIF, natprobe.FilteringEndpointIndependent},
		{filteringADF, natprobe.FilteringAddressDependent},
		{filteringAPDF, natprobe.FilteringAddressAndPortDependent},
	}
	for _, mapping := range mappings {
		for _, filtering := range filterings {
			mapping, filtering := mapping, filtering
			t.Run(policyName(mapping.policy, filtering.policy), func(t *testing.T) {
				lab := newLab(t, policyName(mapping.policy, filtering.policy))
				topology := lab.buildNAT44()
				server := lab.startResponder("server-v4", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
				result, err := lab.runProbe("controlled-profile", topology.ClientNamespace, probeOptions{
					Mode: "profile", BindIPv4: topology.ClientIPv4, Port: clientMuxPort,
					ServerIPv4: endpointValue(topology.ServerA1, stunPrimaryPort),
					InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2, Pacing: 20 * time.Millisecond,
				}, func(ready probeReady) {
					lab.installNFT(topology.RouterNamespace, "controlled-nat", controlledNATRules(topology, mapping.policy, filtering.policy, ready.Port))
				})
				if err != nil {
					t.Fatal(err)
				}
				assertProfile(t, result.ProfileIPv4, mapping.want, filtering.want, natprobe.TranslationPresent)
				observations := mustObservations(t, server.observationPath)
				assertServerOracle(t, result.ProfileIPv4, observations, clientMuxPort, externalPortOne)
				if result.ProfileIPv4.InitialMappedEndpoint.Port() == result.ProfileIPv4.LocalEndpoint.Port() {
					t.Fatal("controlled port translation did not change the external port")
				}
				rules := lab.inNamespace(topology.RouterNamespace, "nft", "-j", "list", "ruleset")
				requireCounter(t, rules, "internal-mux-port", 1)
				if count, ok := counterForComment(rules, "internal-bad-port"); !ok || count != 0 {
					t.Fatalf("non-mux Irvio source-port counter = %d, present=%v", count, ok)
				}
				switch filtering.policy {
				case filteringEIF:
					requireCounter(t, rules, "filter-a2p2-accept", 1)
				case filteringADF:
					requireCounter(t, rules, "filter-a2p2-drop", 1)
					requireCounter(t, rules, "filter-a1p2-accept", 1)
				case filteringAPDF:
					requireCounter(t, rules, "filter-a2p2-drop", 1)
					requireCounter(t, rules, "filter-a1p2-drop", 1)
				}
				lab.captureNamespace(topology.RouterNamespace, "router", true)
			})
		}
	}
}

func TestM4B_IPv6FilteringMatrix(t *testing.T) {
	requirePrivileged(t)
	tests := []struct {
		name   string
		policy filteringPolicy
		want   natprobe.FilteringBehavior
	}{
		{"eif", filteringEIF, natprobe.FilteringEndpointIndependent},
		{"adf", filteringADF, natprobe.FilteringAddressDependent},
		{"apdf", filteringAPDF, natprobe.FilteringAddressAndPortDependent},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			lab := newLab(t, "ipv6-"+test.name)
			topology := lab.buildRoutedIPv6()
			server := lab.startResponder("server-v6", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
			result, err := lab.runProbe("ipv6-profile", topology.ClientNamespace, probeOptions{
				Mode: "profile", BindIPv6: topology.ClientIPv6, Port: clientMuxPort,
				ServerIPv6: endpointValue(topology.ServerA1, stunPrimaryPort),
				InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2, Pacing: 20 * time.Millisecond,
			}, func(ready probeReady) {
				lab.installNFT(topology.RouterNamespace, "ipv6-filter", ipv6FilterRules(topology, test.policy, ready.Port))
			})
			if err != nil {
				t.Fatal(err)
			}
			assertProfile(t, result.ProfileIPv6, natprobe.MappingEndpointIndependent, test.want, natprobe.TranslationAbsent)
			observations := mustObservations(t, server.observationPath)
			assertServerOracle(t, result.ProfileIPv6, observations, clientMuxPort, clientMuxPort)
			rules := lab.inNamespace(topology.RouterNamespace, "nft", "-j", "list", "ruleset")
			requireCounter(t, rules, "internal-mux-port", 1)
			if count, ok := counterForComment(rules, "internal-bad-port"); !ok || count != 0 {
				t.Fatalf("non-mux IPv6 source-port counter = %d, present=%v", count, ok)
			}
			lab.captureNamespace(topology.ClientNamespace, "client", false)
			lab.captureNamespace(topology.RouterNamespace, "router", true)
			lab.captureNamespace(topology.ServerNamespace, "server", false)
		})
	}
}

func TestM4B_DeterministicRetransmission(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "retransmission")
	topology := lab.buildDirectDualStack()
	server := lab.startResponder("server-v4", topology.ServerNamespace, topology.ServerIPv4A1, topology.ServerIPv4A2, "--drop-first", "1")
	result, err := lab.runProbe("retransmit-basic", topology.ClientNamespace, probeOptions{
		Mode: "basic", BindIPv4: topology.ClientIPv4, Port: clientMuxPort,
		ServerIPv4: endpointValue(topology.ServerIPv4A1, stunPrimaryPort),
		InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 3,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.BasicIPv4 == nil || result.BasicIPv4.Attempts != 2 || result.STUNStats.Retransmissions != 1 {
		t.Fatalf("retransmission evidence mismatch: %+v", result)
	}
	observations := mustObservations(t, server.observationPath)
	if len(observations) < 2 || observations[0].TransactionID != observations[1].TransactionID {
		t.Fatalf("retransmission did not preserve Transaction ID: %+v", observations)
	}
}

func mustObservations(t *testing.T, path string) []rfc5780.Observation {
	t.Helper()
	observations, err := readObservations(path)
	if err != nil {
		t.Fatal(err)
	}
	return observations
}

func endpointValue(address netip.Addr, port uint16) netip.AddrPort {
	return netip.AddrPortFrom(address, port)
}

func nonemptyLines(value string) []string {
	var result []string
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}

func waitForObservationCount(t *testing.T, path string, count int, maximum time.Duration) []rfc5780.Observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		observations, err := readObservations(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(observations) >= count {
			return observations
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %d observations: %v", count, ctx.Err())
		case <-ticker.C:
		}
	}
}
