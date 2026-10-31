// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestM7_SafetyPolicyLifecycleStressAndExitCrashAdoption(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")
	lab := newLab(t, "m7-safety-life")
	namespace := lab.namespace("policy")
	lab.inNamespace(namespace, "ip", "link", "set", "lo", "up")

	for _, mode := range []string{"policy-cycles", "exit-policy-cycles"} {
		resultPath := filepath.Join(lab.artifactDir, mode+".json")
		child := lab.startChild(mode, namespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
			"--mode", mode, "--cycles", "100", "--result", resultPath)
		waitM8Child(t, child)
		var result m8ProtectionReport
		if err := readJSON(resultPath, &result); err != nil {
			t.Fatal(err)
		}
		if result.Cycles != 100 || result.FDBefore != result.FDAfter || result.NetlinkBefore != result.NetlinkAfter ||
			result.GoroutinesBefore != result.GoroutinesAfter {
			t.Fatalf("%s leaked resources: %+v", mode, result)
		}
	}
	list := lab.inNamespace(namespace, "nft", "list", "tables")
	if strings.Contains(list, "hydra_protect") || strings.Contains(list, "hydra_exit") {
		t.Fatalf("policy residue after lifecycle stress: %s", list)
	}

	ready1 := filepath.Join(lab.artifactDir, "exit-crash-ready-1.json")
	release1 := filepath.Join(lab.artifactDir, "exit-crash-release-1")
	first := lab.startChild("exit-crash-1", namespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
		"--mode", "exit-policy", "--ready", ready1, "--release", release1)
	waitM8File(t, first, ready1)
	if err := syscall.Kill(-first.command.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitM8ChildFailure(t, first)
	lab.inNamespace(namespace, "nft", "list", "table", "inet", "hydra_exit")
	// Simulate owned crash residue with a missing mandatory rule/set and an unexpected chain.
	removeNFTCommentRules(t, lab, namespace, "inet", "hydra_exit", "forward", "drop-safety-v0-udp")
	lab.inNamespace(namespace, "nft", "delete", "set", "inet", "hydra_exit", "safe_udp_v0")
	lab.inNamespace(namespace, "nft", "add", "chain", "inet", "hydra_exit", "stale_residue")

	ready2 := filepath.Join(lab.artifactDir, "exit-crash-ready-2.json")
	release2 := filepath.Join(lab.artifactDir, "exit-crash-release-2")
	second := lab.startChild("exit-crash-2", namespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
		"--mode", "exit-policy", "--ready", ready2, "--release", release2)
	waitM8File(t, second, ready2)
	rules := lab.inNamespace(namespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	for value, want := range map[string]int{
		"hydra-exit-firewall-v1": 1,
		// Named sets occur once in their declaration and once in their lookup.
		"safe_tcp_v0": 2, "safe_udp_v0": 2,
		"drop-safety-v0-tcp": 1, "drop-safety-v0-udp": 1,
	} {
		if count := strings.Count(rules, value); count != want {
			t.Fatalf("adopted policy object %q count=%d want=%d", value, count, want)
		}
	}
	if err := touch(release2); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := second.wait(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := runM8Command("ip", "netns", "exec", namespace, "nft", "list", "table", "inet", "hydra_exit"); err == nil {
		t.Fatal("adopted Exit policy remained after graceful close")
	}

	// Reject unrelated reserved-name tables without adoption, widening, or permissive fallback.
	lab.installNFT(namespace, "m7-exit-policy-conflict", `table inet hydra_exit {
 chain forward { type filter hook forward priority filter; policy accept; }
}`)
	conflict := lab.startChild("exit-policy-conflict", namespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
		"--mode", "exit-policy", "--ready", filepath.Join(lab.artifactDir, "conflict-ready"),
		"--release", filepath.Join(lab.artifactDir, "conflict-release"))
	waitM8ChildFailure(t, conflict)
	conflictRules := lab.inNamespace(namespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	if strings.Contains(conflictRules, "safe_tcp_v0") || strings.Contains(conflictRules, "safe_udp_v0") ||
		strings.Contains(conflictRules, "accept-hydra-wan") {
		t.Fatalf("conflicting unowned table was mutated into an Exit policy: %s", conflictRules)
	}
	lab.inNamespace(namespace, "nft", "delete", "table", "inet", "hydra_exit")
	if err := lab.Close(); err != nil {
		t.Fatalf("first explicit lab cleanup: %v", err)
	}
	if err := lab.Close(); err != nil {
		t.Fatalf("second idempotent lab cleanup: %v", err)
	}
}
