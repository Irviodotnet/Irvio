// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"net/netip"
	"strings"
	"testing"
)

func TestControlledNATRulesCoverEveryPolicyWithoutFormattingErrors(t *testing.T) {
	topology := nat44Topology{
		ClientIPv4: netip.MustParseAddr("10.241.10.2"), RouterOutside: netip.MustParseAddr("198.19.10.1"),
		ServerA1: netip.MustParseAddr("198.19.10.2"), ServerA2: netip.MustParseAddr("198.19.10.3"),
		InsideInterface: "hyi0001", OutsideInterface: "hyo0001",
	}
	for _, mapping := range []mappingPolicy{mappingEIM, mappingADM, mappingAPDM} {
		for _, filtering := range []filteringPolicy{filteringEIF, filteringADF, filteringAPDF} {
			rules := controlledNATRules(topology, mapping, filtering, clientMuxPort)
			if strings.Contains(rules, "%!") {
				t.Fatalf("formatting error in %s rules:\n%s", policyName(mapping, filtering), rules)
			}
			for _, required := range []string{
				"table ip hydra_m4b_raw", "notrack", "alternate-stateless-forward",
				"internal-mux-port", "internal-bad-port", "snat-a1p1", "snat-a2p1", "snat-a2p2",
			} {
				if !strings.Contains(rules, required) {
					t.Fatalf("%s rules omit %q", policyName(mapping, filtering), required)
				}
			}
		}
	}
}

func TestCounterForComment(t *testing.T) {
	ruleset := `{"nftables":[{"rule":{"comment":"internal-mux-port","expr":[{"counter":{"packets":7,"bytes":140}}]}}]}`
	packets, ok := counterForComment(ruleset, "internal-mux-port")
	if !ok || packets != 7 {
		t.Fatalf("counter = %d, %v", packets, ok)
	}
	if _, ok := counterForComment(ruleset, "missing"); ok {
		t.Fatal("missing counter was reported present")
	}
}

func TestUniquePortSet(t *testing.T) {
	if got := uniquePortSet(51001, 51001, 51001); got != "51001" {
		t.Fatalf("single port set = %q", got)
	}
	if got := uniquePortSet(51001, 51002, 51002); got != "{ 51001, 51002 }" {
		t.Fatalf("deduplicated port set = %q", got)
	}
}
