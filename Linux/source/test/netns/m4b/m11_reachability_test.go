// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type m11PATTopology struct {
	SubjectNamespace  string
	NATNamespace      string
	VerifierNamespace string
	SubjectIf         string
	NATPrivateIf      string
	NATPublicIf       string
	VerifierIf        string
	Subject           netip.Addr
	NATPrivate        netip.Addr
	NATPublic         netip.Addr
	Verifier          netip.Addr
	External          netip.Addr
	ExternalNext      netip.Addr
}

func TestM11_StaticPATQuorumAndAcceptedControlBypass(t *testing.T) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m11-static-pat")
	topology := buildM11PATTopology(lab)
	internet, internetRelease, _ := startM7InternetServer(t, lab, m7Topology{
		InternetNamespace: topology.VerifierNamespace, Internet4: topology.Verifier,
	})

	runtimeDir := lab.tempDir("m11-static-pat")
	verifierAddresses := make([]string, 0, 3)
	verifiers := make([]*childProcess, 0, 3)
	verifierPeers := make(map[string]struct{}, 3)
	for index := 0; index < 3; index++ {
		dataDir := filepath.Join(runtimeDir, fmt.Sprintf("verifier-%d", index+1))
		if err := os.Mkdir(dataDir, 0o700); err != nil {
			t.Fatal(err)
		}
		label := fmt.Sprintf("m11-verifier-%d", index+1)
		process := lab.startChild(label, topology.VerifierNamespace, irviod,
			"--data-dir", dataDir, "--listen-port", fmt.Sprintf("%d", 46001+index),
			"--public-proof-ttl", "4s", "--public-reverify-lead", "2s",
			"--allow-test-public-ip", "--print-node-info")
		info := waitNodeInfo(t, lab, process, label+".stdout.log", 1)
		verifierAddresses = append(verifierAddresses, selectMultiaddr(t, info, "ip4", topology.Verifier))
		verifierPeers[info.PeerID] = struct{}{}
		verifiers = append(verifiers, process)
	}
	if len(verifierPeers) != 3 {
		t.Fatal("reachability quorum did not use three distinct verifier PeerIDs")
	}

	subjectDir := filepath.Join(runtimeDir, "subject")
	if err := os.Mkdir(subjectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--data-dir", subjectDir, "--listen-port", "4001", "--protected-mode", "--public-node",
		"--public-control-port4", "45001", "--public-proof-ttl", "4s",
		"--public-reverify-lead", "2s", "--public-retry", "1s", "--allow-test-public-ip",
		"--print-node-info",
	}
	for _, address := range verifierAddresses {
		args = append(args, "--reachability-verifier", address)
	}
	subject := lab.startChild("m11-subject", topology.SubjectNamespace, irviod, args...)
	_ = waitNodeInfo(t, lab, subject, "m11-subject.stdout.log", 1)
	verified := waitM11Log(t, lab, subject, "m11-subject.stderr.log", "PUBLIC_REACHABILITY_VERIFIED", 12*time.Second, nil)
	if verified["endpoint"] != netip.AddrPortFrom(topology.External, 45001).String() {
		t.Fatalf("verified endpoint=%v want=%s", verified["endpoint"], netip.AddrPortFrom(topology.External, 45001))
	}

	natRules := lab.inNamespace(topology.NATNamespace, "nft", "-j", "list", "table", "ip", "hydra_m11_pat")
	requireCounter(t, natRules, "m11-pat-inbound", 2)
	requireCounter(t, natRules, "m11-pat-outbound", 2)
	subjectRules := lab.inNamespace(topology.SubjectNamespace, "nft", "-j", "list", "ruleset")
	requireAcceptedControlBypass(t, subjectRules)
	requireCounter(t, subjectRules, "accept-bypass", 1)
	runM8ProbeExpect(t, lab, topology.SubjectNamespace, "http",
		netip.AddrPortFrom(topology.Verifier, 18080), topology.SubjectIf, 0, false)

	replaceM11PAT(lab, topology.NATNamespace, "m11-dynamic-public-ip", m11PATRulesFor(topology, topology.ExternalNext, true))
	changed := waitM11Log(t, lab, subject, "m11-subject.stderr.log", "PUBLIC_REACHABILITY_VERIFIED", 8*time.Second, func(event map[string]any) bool {
		return event["endpoint"] == netip.AddrPortFrom(topology.ExternalNext, 45001).String()
	})
	if changed["network_generation"] != verified["network_generation"] {
		t.Fatalf("external NAT address change unexpectedly changed local NetworkGeneration: before=%v after=%v", verified, changed)
	}

	lab.inNamespace(topology.SubjectNamespace, "ip", "addr", "add", "91.203.0.7/32", "dev", "lo")
	replaceM11PAT(lab, topology.NATNamespace, "m11-broken-pat", m11PATRulesFor(topology, topology.ExternalNext, false))
	_ = waitM11Log(t, lab, subject, "m11-subject.stderr.log", "PUBLIC_REACHABILITY_UNVERIFIED", 8*time.Second, nil)

	if err := stopM10Child(subject, 12*time.Second); err != nil {
		t.Fatalf("stop public subject: %v", err)
	}
	remaining := lab.inNamespace(topology.SubjectNamespace, "nft", "list", "ruleset")
	if strings.Contains(remaining, "hydra_ctl_") || strings.Contains(remaining, "hydra_protect") {
		t.Fatalf("public-role kernel ownership remained after graceful close: %s", remaining)
	}
	replaceM11PAT(lab, topology.NATNamespace, "m11-wrong-external-port", m11PATRulesFor(topology, topology.ExternalNext, true))
	wrongPortArgs := append([]string(nil), args...)
	for index := 0; index+1 < len(wrongPortArgs); index++ {
		if wrongPortArgs[index] == "--public-control-port4" {
			wrongPortArgs[index+1] = "45002"
		}
	}
	wrongPort := lab.startChild("m11-wrong-port-subject", topology.SubjectNamespace, irviod, wrongPortArgs...)
	_ = waitNodeInfo(t, lab, wrongPort, "m11-wrong-port-subject.stdout.log", 1)
	_ = waitM11Log(t, lab, wrongPort, "m11-wrong-port-subject.stderr.log", "PUBLIC_REACHABILITY_UNVERIFIED", 8*time.Second, nil)
	if err := stopM10Child(wrongPort, 12*time.Second); err != nil {
		t.Fatalf("stop wrong-port subject: %v", err)
	}
	for index, verifier := range verifiers {
		if err := stopM10Child(verifier, 12*time.Second); err != nil {
			t.Fatalf("stop verifier %d: %v", index+1, err)
		}
	}
	if err := touch(internetRelease); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := internet.wait(ctx); err != nil {
		t.Fatalf("stop deterministic Internet: %v", err)
	}
}

func requireAcceptedControlBypass(t *testing.T, ruleset string) {
	t.Helper()
	direct, directOK := counterForComment(ruleset, "mark-hydrad-cgroup")
	restored, restoredOK := counterForComment(ruleset, "restore-inbound-control-mark")
	inbound, inboundOK := counterForComment(ruleset, "mark-inbound-hydrad-cgroup")
	accepted, _ := counterForComment(ruleset, "mark-accepted-hydrad-tcp")
	if !directOK || !restoredOK || !inboundOK {
		t.Fatalf("accepted-control cgroup counters are missing: direct=%t restored=%t inbound=%t", directOK, restoredOK, inboundOK)
	}
	if direct+restored == 0 {
		t.Fatal("accepted-control output was not marked by either cgroup path")
	}
	// Initial SYN may use the listen-port rule before socket cgroup association exists.
	if restored > 0 && inbound+accepted == 0 {
		t.Fatal("accepted-control conntrack mark was restored without an inbound marking rule match")
	}
}

func buildM11PATTopology(lab *lab) m11PATTopology {
	lab.t.Helper()
	topology := m11PATTopology{
		SubjectNamespace: lab.namespace("subject"), NATNamespace: lab.namespace("nat"),
		VerifierNamespace: lab.namespace("verifier"), SubjectIf: lab.interfaceName('s'),
		NATPrivateIf: lab.interfaceName('n'), NATPublicIf: lab.interfaceName('p'), VerifierIf: lab.interfaceName('v'),
		Subject: netip.MustParseAddr("10.111.0.2"), NATPrivate: netip.MustParseAddr("10.111.0.1"),
		NATPublic: netip.MustParseAddr("203.0.113.1"), Verifier: netip.MustParseAddr("203.0.113.2"),
		External:     netip.MustParseAddr("203.0.113.9"),
		ExternalNext: netip.MustParseAddr("203.0.113.10"),
	}
	lab.createVeth(topology.SubjectNamespace, topology.SubjectIf, topology.NATNamespace, topology.NATPrivateIf)
	lab.createVeth(topology.NATNamespace, topology.NATPublicIf, topology.VerifierNamespace, topology.VerifierIf)
	for _, namespace := range []string{topology.SubjectNamespace, topology.NATNamespace, topology.VerifierNamespace} {
		lab.inNamespace(namespace, "ip", "link", "set", "lo", "up")
	}
	for _, item := range []struct{ namespace, iface, address string }{
		{topology.SubjectNamespace, topology.SubjectIf, topology.Subject.String() + "/24"},
		{topology.NATNamespace, topology.NATPrivateIf, topology.NATPrivate.String() + "/24"},
		{topology.NATNamespace, topology.NATPublicIf, topology.NATPublic.String() + "/24"},
		{topology.VerifierNamespace, topology.VerifierIf, topology.Verifier.String() + "/24"},
	} {
		lab.inNamespace(item.namespace, "ip", "link", "set", item.iface, "up")
		lab.inNamespace(item.namespace, "ip", "addr", "add", item.address, "dev", item.iface)
	}
	lab.inNamespace(topology.NATNamespace, "ip", "addr", "add", topology.External.String()+"/32", "dev", topology.NATPublicIf)
	lab.inNamespace(topology.NATNamespace, "ip", "addr", "add", topology.ExternalNext.String()+"/32", "dev", topology.NATPublicIf)
	lab.inNamespace(topology.NATNamespace, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1")
	lab.inNamespace(topology.SubjectNamespace, "ip", "route", "add", "default", "via", topology.NATPrivate.String(), "dev", topology.SubjectIf)
	lab.installNFT(topology.NATNamespace, "m11-static-pat", m11PATRules(topology, true))
	lab.recordTopology(topology)
	return topology
}

func m11PATRules(topology m11PATTopology, inbound bool) string {
	return m11PATRulesFor(topology, topology.External, inbound)
}

func m11PATRulesFor(topology m11PATTopology, external netip.Addr, inbound bool) string {
	pre := ""
	if inbound {
		pre = fmt.Sprintf(`chain prerouting {
  type nat hook prerouting priority dstnat; policy accept;
  ip daddr %s tcp dport 45001 counter dnat to %s:4001 comment "m11-pat-inbound"
}`, external, topology.Subject)
	}
	return fmt.Sprintf(`table ip hydra_m11_pat {
%s
chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
	ip saddr %s tcp dport 46001-46003 counter snat to %s comment "m11-pat-outbound"
}
}
`, pre, topology.Subject, external)
}

func replaceM11PAT(lab *lab, namespace, label, rules string) {
	lab.t.Helper()
	// Change public endpoints atomically so verification never observes intermediate NAT policy.
	lab.installNFT(namespace, label, "delete table ip hydra_m11_pat\n"+rules)
}

func TestM11_IPv6PublicVerificationAndNetworkMigration(t *testing.T) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	lab := newLab(t, "m11-public-ipv6")
	subjectNamespace, verifierNamespace := lab.namespace("subject6"), lab.namespace("verifier6")
	subjectIf, verifierIf := lab.interfaceName('s'), lab.interfaceName('v')
	oldAddress := netip.MustParseAddr("2001:db8:111::1")
	newAddress := netip.MustParseAddr("2001:db8:111::3")
	verifierAddress := netip.MustParseAddr("2001:db8:111::2")
	lab.createVeth(subjectNamespace, subjectIf, verifierNamespace, verifierIf)
	for _, namespace := range []string{subjectNamespace, verifierNamespace} {
		lab.inNamespace(namespace, "ip", "link", "set", "lo", "up")
	}
	for _, item := range []struct{ namespace, iface, address string }{
		{subjectNamespace, subjectIf, oldAddress.String() + "/64"},
		{verifierNamespace, verifierIf, verifierAddress.String() + "/64"},
	} {
		lab.inNamespace(item.namespace, "ip", "link", "set", item.iface, "up")
		lab.inNamespace(item.namespace, "ip", "-6", "addr", "add", item.address, "dev", item.iface, "nodad")
	}
	lab.inNamespace(subjectNamespace, "ip", "-6", "route", "add", "default", "via", verifierAddress.String(), "dev", subjectIf)
	runtimeDir := lab.tempDir("m11-public-ipv6")
	var verifierAddresses []string
	var verifiers []*childProcess
	for index := 0; index < 3; index++ {
		dataDir := filepath.Join(runtimeDir, fmt.Sprintf("verifier-%d", index+1))
		if err := os.Mkdir(dataDir, 0o700); err != nil {
			t.Fatal(err)
		}
		label := fmt.Sprintf("m11-ipv6-verifier-%d", index+1)
		process := lab.startChild(label, verifierNamespace, irviod,
			"--data-dir", dataDir, "--listen-port", fmt.Sprintf("%d", 47010+index),
			"--public-proof-ttl", "6s", "--public-reverify-lead", "3s",
			"--allow-test-public-ip", "--print-node-info")
		info := waitNodeInfo(t, lab, process, label+".stdout.log", 1)
		verifierAddresses = append(verifierAddresses, selectMultiaddr(t, info, "ip6", verifierAddress))
		verifiers = append(verifiers, process)
	}
	subjectDir := filepath.Join(runtimeDir, "subject")
	if err := os.Mkdir(subjectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--data-dir", subjectDir, "--listen-port", "47001", "--protected-mode", "--public-node",
		"--public-control-port6", "47001", "--public-proof-ttl", "6s",
		"--public-reverify-lead", "3s", "--public-retry", "1s", "--allow-test-public-ip", "--print-node-info",
	}
	for _, address := range verifierAddresses {
		args = append(args, "--reachability-verifier", address)
	}
	subject := lab.startChild("m11-ipv6-subject", subjectNamespace, irviod, args...)
	_ = waitNodeInfo(t, lab, subject, "m11-ipv6-subject.stdout.log", 1)
	first := waitM11Log(t, lab, subject, "m11-ipv6-subject.stderr.log", "PUBLIC_REACHABILITY_VERIFIED", 12*time.Second, func(event map[string]any) bool {
		return event["endpoint"] == netip.AddrPortFrom(oldAddress, 47001).String()
	})
	runIPBatch(t, lab, subjectNamespace, "m11-public-ipv6-migration", fmt.Sprintf(
		"addr add %s/64 dev %s nodad\naddr del %s/64 dev %s\n",
		newAddress, subjectIf, oldAddress, subjectIf))
	second := waitM11Log(t, lab, subject, "m11-ipv6-subject.stderr.log", "PUBLIC_REACHABILITY_VERIFIED", 12*time.Second, func(event map[string]any) bool {
		return event["endpoint"] == netip.AddrPortFrom(newAddress, 47001).String()
	})
	if second["network_generation"].(float64) <= first["network_generation"].(float64) {
		t.Fatalf("IPv6 migration did not advance NetworkGeneration: before=%v after=%v", first, second)
	}
	if err := stopM10Child(subject, 12*time.Second); err != nil {
		t.Fatal(err)
	}
	for _, verifier := range verifiers {
		if err := stopM10Child(verifier, 12*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	lab.inNamespace(subjectNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
}

func waitM11Log(t *testing.T, lab *lab, child *childProcess, file, message string, maximum time.Duration, accept func(map[string]any) bool) map[string]any {
	t.Helper()
	path := filepath.Join(lab.artifactDir, file)
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			scanner := bufio.NewScanner(strings.NewReader(string(data)))
			for scanner.Scan() {
				var event map[string]any
				if json.Unmarshal(scanner.Bytes(), &event) == nil && event["msg"] == message && (accept == nil || accept(event)) {
					return event
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s: %v", message, ctx.Err())
		case <-child.done:
			child.mu.Lock()
			err := child.waitErr
			child.mu.Unlock()
			t.Fatalf("child exited before %s: %v", message, err)
		case <-ticker.C:
		}
	}
}
