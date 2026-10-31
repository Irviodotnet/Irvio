// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package killswitch

import (
	"slices"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"

	"github.com/Irviodotnet/Irvio/internal/safetypolicy"
	"github.com/Irviodotnet/Irvio/internal/trafficstats"
)

func TestPolicyBuildsInterfaceIndependentFailClosedRules(t *testing.T) {
	policy, err := normalizePolicy(Policy{TUNName: "hydra0", BypassMark: 0x48594452})
	if err != nil {
		t.Fatal(err)
	}
	table := &nftables.Table{Name: policy.TableName, Family: nftables.TableFamilyINet}
	drop := nftables.ChainPolicyDrop
	chain := &nftables.Chain{
		Name: outputChainName, Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityFilter, Policy: &drop,
	}
	if !validOutputChain(chain) {
		t.Fatal("canonical chain is not fail-closed")
	}
	accept := nftables.ChainPolicyAccept
	chain.Policy = &accept
	if validOutputChain(chain) {
		t.Fatal("an accept-policy chain was treated as canonical protection")
	}
	chain.Policy = &drop
	sets, err := safetypolicy.AddLinuxPortSets(&nftables.Conn{}, table)
	if err != nil {
		t.Fatal(err)
	}
	rules := buildRules(policy, table, chain, sets, false)
	accept = nftables.ChainPolicyAccept
	prerouting := &nftables.Chain{
		Name: preroutingChainName, Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookPrerouting, Priority: nftables.ChainPriorityMangle, Policy: &accept,
	}
	if !validPreroutingChain(prerouting) {
		t.Fatal("canonical prerouting chain is invalid")
	}
	ingress := buildPreroutingRules(policy, table, prerouting)
	labels := make([]string, 0, len(rules)+len(ingress))
	for _, rule := range ingress {
		label, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
		if !ok {
			t.Fatal("inbound rule lacks bounded ownership label")
		}
		labels = append(labels, label)
	}
	for _, rule := range rules {
		label, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
		if !ok {
			t.Fatal("rule lacks bounded ownership label")
		}
		labels = append(labels, label)
	}
	if !labelsComplete(labels) || labels[0] != "mark-inbound-bypass" ||
		labels[1] != "mark-inbound-tunnel-control" || labels[2] != OwnerMarker ||
		labels[len(labels)-1] != "drop-ordinary" {
		t.Fatalf("rule labels=%v", labels)
	}
	for _, inbound := range ingress {
		if _, ok := inbound.Exprs[1].(*expr.Socket); !ok {
			t.Fatalf("inbound ownership is not derived from socket mark: %#v", inbound.Exprs)
		}
		if meta, ok := inbound.Exprs[len(inbound.Exprs)-1].(*expr.Meta); !ok || !meta.SourceRegister {
			t.Fatalf("inbound socket mark is not applied before route validation: %#v", inbound.Exprs)
		}
	}
	last := rules[len(rules)-1]
	verdict, ok := last.Exprs[len(last.Exprs)-1].(*expr.Verdict)
	if !ok || verdict.Kind != expr.VerdictDrop {
		t.Fatalf("terminal rule=%#v", last.Exprs)
	}
	for _, forbidden := range []string{"accept-dns", "accept-lan", "accept-root", "accept-destination"} {
		for _, label := range labels {
			if label == forbidden {
				t.Fatalf("unsafe broad exception %q", label)
			}
		}
	}
}

func TestUserAccountingIsAfterSafetyWithoutVerdict(t *testing.T) {
	policy, err := normalizePolicy(Policy{TUNName: "hydra0", BypassMark: 0x48594452})
	if err != nil {
		t.Fatal(err)
	}
	table := &nftables.Table{Name: policy.TableName, Family: nftables.TableFamilyINet}
	sets, err := safetypolicy.AddLinuxPortSets(&nftables.Conn{}, table)
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{}
	for _, rule := range buildRules(policy, table, &nftables.Chain{Table: table}, sets, false) {
		label, _ := userdata.GetString(rule.UserData, userdata.TypeComment)
		labels = append(labels, label)
		if label != "account-user-upload" {
			continue
		}
		ref, ok := rule.Exprs[len(rule.Exprs)-1].(*expr.Objref)
		if !ok || ref.Name != trafficstats.CounterName {
			t.Fatal("counter must follow interface/mark conditions")
		}
		for _, e := range rule.Exprs {
			if _, verdict := e.(*expr.Verdict); verdict {
				t.Fatal("accounting must not change packet verdict")
			}
		}
	}
	i := slices.Index(labels, "account-user-upload")
	if i <= slices.Index(labels, "reject-safety-v0-udp") || i >= slices.Index(labels, "accept-tunnel") {
		t.Fatal(labels)
	}
}

func TestPolicyRejectsSafetyV0BeforeTunnelAccept(t *testing.T) {
	policy, err := normalizePolicy(Policy{TUNName: "hydra0", BypassMark: 0x48594452})
	if err != nil {
		t.Fatal(err)
	}
	table := &nftables.Table{Name: policy.TableName, Family: nftables.TableFamilyINet}
	chain := &nftables.Chain{Name: outputChainName, Table: table}
	sets, err := safetypolicy.AddLinuxPortSets(&nftables.Conn{}, table)
	if err != nil {
		t.Fatal(err)
	}
	rules := buildRules(policy, table, chain, sets, false)
	labels := make([]string, 0, len(rules))
	for _, rule := range rules {
		label, _ := userdata.GetString(rule.UserData, userdata.TypeComment)
		labels = append(labels, label)
	}
	tcp := slices.Index(labels, "reject-safety-v0-tcp")
	udp := slices.Index(labels, "reject-safety-v0-udp")
	tunnel := slices.Index(labels, "accept-tunnel")
	if tcp < 0 || udp < 0 || tunnel < 0 || tcp >= tunnel || udp >= tunnel {
		t.Fatalf("safety rules must precede tunnel accept: %v", labels)
	}
	for _, index := range []int{tcp, udp} {
		var lookup, reject bool
		for _, expression := range rules[index].Exprs {
			switch expression.(type) {
			case *expr.Lookup:
				lookup = true
			case *expr.Reject:
				reject = true
			}
		}
		if !lookup || !reject {
			t.Fatalf("client safety rule %q lookup=%v reject=%v", labels[index], lookup, reject)
		}
	}
}

func TestSourceValidationOwnershipIsUnambiguous(t *testing.T) {
	if labelsComplete(append(append([]string(nil), expectedRuleLabels...), sourceValidationPreviousZero,
		sourceValidationPreviousOne)) {
		t.Fatal("two prior sysctl values were accepted")
	}
	if !labelsComplete(append(append([]string(nil), expectedRuleLabels...), sourceValidationPreviousZero)) {
		t.Fatal("one persisted prior sysctl value was rejected")
	}
}

func TestTransportIntentMarksMustRemainDistinct(t *testing.T) {
	policy, err := normalizePolicy(Policy{TUNName: "hydra0", BypassMark: 0x48594452})
	if err != nil {
		t.Fatal(err)
	}
	if policy.TunnelControlMark == 0 || policy.TunnelControlMark == policy.BypassMark {
		t.Fatalf("transport intent marks are not distinct: %#v", policy)
	}
	if _, err := normalizePolicy(Policy{
		TUNName: "hydra0", BypassMark: 0x48594452, TunnelControlMark: 0x48594452,
	}); err != ErrInvalidConfig {
		t.Fatalf("same WAN and tunnel-control mark error=%v", err)
	}
}

func TestOwnershipRejectsUnknownAndDifferentVersion(t *testing.T) {
	if owned, err := labelOwnership([]string{"administrator-rule"}); owned || err != nil {
		t.Fatalf("unrelated ownership owned=%v err=%v", owned, err)
	}
	if owned, err := labelOwnership([]string{"hydra-protected-mode-v2"}); owned || err != ErrVersionMismatch {
		t.Fatalf("version ownership owned=%v err=%v", owned, err)
	}
	if owned, err := labelOwnership([]string{OwnerMarker}); !owned || err != nil {
		t.Fatalf("Irvio ownership owned=%v err=%v", owned, err)
	}
}
