// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package exitfirewall

import (
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"

	"github.com/Irviodotnet/Irvio/internal/safetypolicy"
)

func testPolicy() Policy {
	return Policy{TableName: "hydra_test", TUNName: "hydra0", WANName: "wan0", ClientIPv4: netip.MustParsePrefix("10.77.0.0/24"), ClientIPv6: netip.MustParsePrefix("fd77::/64"), BlockedDestinations: []netip.Prefix{netip.MustParsePrefix("198.18.20.0/24")}}
}

func TestPolicyDropsSafetyV0BeforeWANAccept(t *testing.T) {
	policy, err := normalizePolicy(testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	table := &nftables.Table{Name: policy.TableName, Family: nftables.TableFamilyINet}
	forward := &nftables.Chain{Name: forwardChainName, Table: table}
	sets, err := safetypolicy.AddLinuxPortSets(&nftables.Conn{}, table)
	if err != nil {
		t.Fatal(err)
	}
	rules := buildRules(policy, table, &nftables.Chain{}, forward, &nftables.Chain{}, sets)
	labels := make([]string, 0, len(rules))
	for _, rule := range rules {
		label, _ := userdata.GetString(rule.UserData, userdata.TypeComment)
		labels = append(labels, label)
	}
	tcp := slices.Index(labels, "drop-safety-v0-tcp")
	udp := slices.Index(labels, "drop-safety-v0-udp")
	wan := slices.Index(labels, "accept-hydra-wan")
	if tcp < 0 || udp < 0 || wan < 0 || tcp >= wan || udp >= wan {
		t.Fatalf("safety rules must precede WAN accept: %v", labels)
	}
	for _, index := range []int{tcp, udp} {
		var lookup, drop bool
		for _, expression := range rules[index].Exprs {
			switch value := expression.(type) {
			case *expr.Lookup:
				lookup = true
			case *expr.Verdict:
				drop = value.Kind == expr.VerdictDrop
			}
		}
		if !lookup || !drop {
			t.Fatalf("Exit safety rule %q lookup=%v drop=%v", labels[index], lookup, drop)
		}
	}
}

func TestPolicyBuildsNATIsolationAndAntiMultihop(t *testing.T) {
	policy, err := normalizePolicy(testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	table := &nftables.Table{Name: policy.TableName, Family: nftables.TableFamilyINet}
	input := &nftables.Chain{Name: inputChainName, Table: table}
	forward := &nftables.Chain{Name: forwardChainName, Table: table}
	nat := &nftables.Chain{Name: natChainName, Table: table}
	sets, err := safetypolicy.AddLinuxPortSets(&nftables.Conn{}, table)
	if err != nil {
		t.Fatal(err)
	}
	rules := buildRules(policy, table, input, forward, nat, sets)
	labels := make(map[string]bool)
	for _, rule := range rules {
		for _, expression := range rule.Exprs {
			if _, ok := expression.(*expr.Masq); ok {
				labels[ruleLabel(rule)] = true
			}
		}
	}
	if !labels["masquerade-ipv4"] || !labels["masquerade-ipv6"] {
		t.Fatalf("NAT labels=%v", labels)
	}
	if got := ruleLabel(rules[0]); got != OwnerMarker {
		t.Fatalf("first rule=%q", got)
	}
}

func TestPolicyPinsSNATToGenerationWANAddress(t *testing.T) {
	policy := testPolicy()
	policy.WANIPv4 = netip.MustParseAddr("192.0.2.3")
	policy.WANIPv6 = netip.MustParseAddr("2001:db8::3")
	policy, err := normalizePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	table := &nftables.Table{Name: policy.TableName, Family: nftables.TableFamilyINet}
	nat := &nftables.Chain{Name: natChainName, Table: table}
	rules := buildNATRules(policy, table, nat)
	if len(rules) != 2 {
		t.Fatalf("NAT rule count=%d", len(rules))
	}
	for _, rule := range rules {
		var immediate, sourceNAT bool
		for _, expression := range rule.Exprs {
			switch value := expression.(type) {
			case *expr.Immediate:
				immediate = len(value.Data) == 4 || len(value.Data) == 16
			case *expr.NAT:
				sourceNAT = value.Type == expr.NATTypeSourceNAT && value.RegAddrMin == 1
			case *expr.Masq:
				t.Fatal("generation-pinned NAT unexpectedly uses masquerade")
			}
		}
		if !immediate || !sourceNAT {
			t.Fatalf("SNAT expression incomplete: %+v", rule.Exprs)
		}
	}
}

func TestPolicyAllowsOnlyExactM12SessionControlBeforeExitInputDrop(t *testing.T) {
	policy := testPolicy()
	policy.SessionControl = []netip.AddrPort{
		netip.MustParseAddrPort("10.77.0.1:51822"),
		netip.MustParseAddrPort("[fd77::1]:51822"),
	}
	policy, err := normalizePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	table := &nftables.Table{Name: policy.TableName, Family: nftables.TableFamilyINet}
	input := &nftables.Chain{Name: inputChainName, Table: table}
	sets, err := safetypolicy.AddLinuxPortSets(&nftables.Conn{}, table)
	if err != nil {
		t.Fatal(err)
	}
	rules := buildRules(policy, table, input, &nftables.Chain{}, &nftables.Chain{}, sets)
	if got := ruleLabel(rules[0]); got != OwnerMarker {
		t.Fatalf("owner input rule=%q", got)
	}
	if got := ruleLabel(rules[1]); got != "accept-session-control-00" {
		t.Fatalf("first input rule=%q", got)
	}
	if got := ruleLabel(rules[2]); got != "accept-session-control-01" {
		t.Fatalf("second input rule=%q", got)
	}
	if got := ruleLabel(rules[3]); got != "drop-hydra-to-exit" {
		t.Fatalf("fallback input rule=%q", got)
	}
}

func TestPolicyRejectsUnsafeConfiguration(t *testing.T) {
	tests := []Policy{{}, {TableName: "bad name", TUNName: "hydra0", WANName: "wan0", ClientIPv4: netip.MustParsePrefix("10.0.0.0/24")}, {TUNName: "same", WANName: "same", ClientIPv4: netip.MustParsePrefix("10.0.0.0/24")}}
	for _, policy := range tests {
		if _, err := normalizePolicy(policy); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("policy=%+v error=%v", policy, err)
		}
	}
}

func TestLocalTunnelAddressNormalization(t *testing.T) {
	addresses, err := normalizeLocalTunnelAddresses([]netip.Addr{
		netip.MustParseAddr("fd77::2"), netip.MustParseAddr("10.77.0.2"), netip.MustParseAddr("10.77.0.2"),
	})
	if err != nil || len(addresses) != 2 || addresses[0].String() != "10.77.0.2" || addresses[1].String() != "fd77::2" {
		t.Fatalf("addresses=%v error=%v", addresses, err)
	}
	for _, invalid := range []netip.Addr{{}, netip.IPv4Unspecified(), netip.IPv6Loopback(), netip.MustParseAddr("fe80::1")} {
		if _, err := normalizeLocalTunnelAddresses([]netip.Addr{invalid}); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("address=%s error=%v", invalid, err)
		}
	}
}

func ruleLabel(rule *nftables.Rule) string {
	// Test rule behavior independently of Snapshot's userdata decoding.
	for _, candidate := range []string{
		OwnerMarker,
		"accept-session-control-00", "accept-session-control-01", "drop-hydra-to-exit",
		"drop-hydra-multihop", "drop-safety-v0-tcp", "drop-safety-v0-udp",
		"accept-hydra-wan", "masquerade-ipv4", "masquerade-ipv6",
	} {
		if string(rule.UserData) != "" && containsBytes(rule.UserData, []byte(candidate)) {
			return candidate
		}
	}
	return ""
}

func containsBytes(haystack, needle []byte) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		equal := true
		for offset := range needle {
			if haystack[index+offset] != needle[offset] {
				equal = false
				break
			}
		}
		if equal {
			return true
		}
	}
	return false
}
