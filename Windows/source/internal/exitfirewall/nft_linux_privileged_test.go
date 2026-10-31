// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package exitfirewall

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os"
	"slices"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
)

// Run this test only inside a disposable network namespace with CAP_NET_ADMIN.
func TestPrivilegedWANPathReplacementPreservesSafetyRuleOrder(t *testing.T) {
	if os.Getenv("HYDRA_EXITFIREWALL_PRIVILEGED") != "1" || os.Geteuid() != 0 {
		t.Skip("requires an isolated privileged network namespace")
	}
	ctx := context.Background()
	installation, err := (NFTBackend{}).Apply(ctx, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer installation.Close()
	reconciler, ok := installation.(WANPathReconciler)
	if !ok {
		t.Fatal("Linux nft installation cannot reconcile its WAN path")
	}
	before := forwardRuleLabels(t)
	if err := reconciler.SetWANPath(ctx, "wan1", netip.MustParseAddr("192.0.2.9"), netip.Addr{}); err != nil {
		t.Fatal(err)
	}
	after := forwardRuleLabels(t)
	if !slices.Equal(before, after) {
		t.Fatalf("WAN migration changed forwarding rule order: before=%v after=%v", before, after)
	}
	assertWANRuleMatches(t, "wan1")
	for _, label := range []string{"drop-hydra-multihop", "drop-safety-v0-tcp", "drop-safety-v0-udp", "drop-hydra-other"} {
		if !slices.Contains(after, label) {
			t.Fatalf("WAN migration lost %s", label)
		}
	}
	if err := reconciler.SetWANPath(ctx, "wan0", netip.Addr{}, netip.Addr{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, forwardRuleLabels(t)) {
		t.Fatal("rollback did not restore forwarding rule order")
	}
	assertWANRuleMatches(t, "wan0")
}

func TestPrivilegedCleanupCrashResidueRequiresOwnerMarker(t *testing.T) {
	if os.Getenv("HYDRA_EXITFIREWALL_PRIVILEGED") != "1" || os.Geteuid() != 0 {
		t.Skip("requires an isolated privileged network namespace")
	}
	ctx := context.Background()
	policy := testPolicy()
	policy.TableName = DefaultTableName
	installation, err := (NFTBackend{}).Apply(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer installation.Close()
	if err := CleanupCrashResidue(ctx); err != nil {
		t.Fatal(err)
	}
	if table, err := findTable(DefaultTableName); err != nil || table != nil {
		t.Fatalf("Exit crash table remains: table=%v err=%v", table, err)
	}
	if err := CleanupCrashResidue(ctx); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
	}

	conn := &nftables.Conn{}
	table := conn.CreateTable(&nftables.Table{Name: DefaultTableName, Family: nftables.TableFamilyINet})
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup := &nftables.Conn{}
		cleanup.DelTable(table)
		_ = cleanup.Flush()
	}()
	if err := CleanupCrashResidue(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign table cleanup error = %v", err)
	}
}

func assertWANRuleMatches(t *testing.T, name string) {
	t.Helper()
	conn := &nftables.Conn{}
	table := &nftables.Table{Name: testPolicy().TableName, Family: nftables.TableFamilyINet}
	for _, item := range []struct {
		chain string
		label string
	}{
		{forwardChainName, "accept-hydra-wan"},
		{forwardChainName, "accept-wan-reply"},
		{natChainName, "masquerade-ipv4"},
		{natChainName, "masquerade-ipv6"},
	} {
		rules, err := conn.GetRules(table, &nftables.Chain{Name: item.chain, Table: table})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, rule := range rules {
			label, _ := userdata.GetString(rule.UserData, userdata.TypeComment)
			if label != item.label {
				continue
			}
			for _, expression := range rule.Exprs {
				match, ok := expression.(*expr.Cmp)
				if ok && string(bytes.TrimRight(match.Data, "\x00")) == name {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("%s/%s does not match WAN %q", item.chain, item.label, name)
		}
	}
}

func forwardRuleLabels(t *testing.T) []string {
	t.Helper()
	conn := &nftables.Conn{}
	table := &nftables.Table{Name: testPolicy().TableName, Family: nftables.TableFamilyINet}
	chain := &nftables.Chain{Name: forwardChainName, Table: table}
	rules, err := conn.GetRules(table, chain)
	if err != nil {
		t.Fatal(err)
	}
	labels := make([]string, 0, len(rules))
	for _, rule := range rules {
		label, _ := userdata.GetString(rule.UserData, userdata.TypeComment)
		labels = append(labels, label)
		if label == "accept-hydra-wan" || label == "accept-wan-reply" {
			var counter bool
			for _, item := range rule.Exprs {
				_, counter = item.(*expr.Counter)
				if counter {
					break
				}
			}
			if !counter {
				t.Fatalf("WAN rule %s lost its counter", label)
			}
		}
	}
	return labels
}
