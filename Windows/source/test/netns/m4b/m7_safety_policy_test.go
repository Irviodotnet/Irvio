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
)

var (
	safetyBlockedTCP = []uint16{19, 25, 135, 137, 138, 139, 445}
	safetyBlockedUDP = []uint16{19, 135, 137, 138, 139, 1900, 11211}
	safetyAllowedTCP = []uint16{18, 20, 24, 26, 134, 136, 140, 444, 446, 443, 18082}
	safetyAllowedUDP = []uint16{18, 20, 134, 136, 140, 1899, 1901, 11210, 11212, 18083}
)

func TestM7_EgressSafetyPolicyV0(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")
	lab := newLab(t, "m7-safety-v0")
	topology := buildM7Topology(lab)
	lab.installNFT(topology.ExitNamespace, "m7-safety-oracle", safetyOracleRules(topology))
	server, serverRelease, observations := startM7InternetServer(t, lab, topology,
		"--http-port", "80", "--dns-port", "53",
		"--tcp-ports", joinPorts(append(append([]uint16(nil), safetyBlockedTCP...), safetyAllowedTCP...)),
		"--udp-ports", joinPorts(append(append([]uint16(nil), safetyBlockedUDP...), safetyAllowedUDP...)),
	)

	scenario := struct {
		name                string
		outer4, outer6      bool
		protect4, protect6  bool
		reject4, reject6    bool
		probe4, probe6, dns bool
	}{name: "safety-v0", outer4: true, protect4: true, protect6: true, probe4: true, probe6: true, dns: true}
	// Outer UDP/1900 must remain usable despite inner SSDP blocking.
	exit, exitReady := startM7Peer(t, lab, topology, "responder", scenario, "", "--port", "1900")
	remote := selectSignalAddress(t, exitReady.ListenAddresses, "ipv4")
	// Use real TUN/AWG without client protection to test a modified client's Exit boundary.
	client, _ := startM7Peer(t, lab, topology, "initiator", scenario, remote, "--port", "1900")
	waitDirectAWGReady(t, client, exit)
	if err := touch(exit.awgStartPath); err != nil {
		t.Fatal(err)
	}
	if err := touch(client.awgStartPath); err != nil {
		t.Fatal(err)
	}
	clientResult := waitConnectReport(t, client)
	exitResult := waitConnectReport(t, exit)
	assertConnectedReports(t, clientResult, exitResult, true, false)

	observedBefore := countM8Observations(t, observations)
	assertSafetyMatrixBlocked(t, lab, topology.ClientNamespace, topology.Internet4, topology.Internet6, "modified-client")
	assertFragmentedSafetyBlocked(t, lab, topology)
	exitRules := lab.inNamespace(topology.ExitNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	requireCounter(t, exitRules, "drop-safety-v0-tcp", uint64(len(safetyBlockedTCP)*2))
	requireCounter(t, exitRules, "drop-safety-v0-udp", uint64(len(safetyBlockedUDP)*2))
	oracle := lab.inNamespace(topology.ExitNamespace, "nft", "-j", "list", "table", "inet", "m7_safety_oracle")
	requireCounter(t, oracle, "safety-inner-tcp", uint64(len(safetyBlockedTCP)*2))
	requireCounter(t, oracle, "safety-inner-udp", uint64(len(safetyBlockedUDP)*2))
	assertZeroCounter(t, oracle, "safety-wan-tcp")
	assertZeroCounter(t, oracle, "safety-wan-udp")
	if observedAfter := countM8Observations(t, observations); observedAfter != observedBefore {
		t.Fatalf("blocked modified-client packets reached Internet: before=%d after=%d", observedBefore, observedAfter)
	}

	assertSafetyAllowedTraffic(t, lab, topology, observations)

	policy, policyRelease, _ := startM8Policy(t, lab, topology.ClientNamespace, "m7-safety-client-policy")
	exitTCPBefore := counterValue(t, exitRules, "drop-safety-v0-tcp")
	exitUDPBefore := counterValue(t, exitRules, "drop-safety-v0-udp")
	observedBefore = countM8Observations(t, observations)
	assertSafetyMatrixBlocked(t, lab, topology.ClientNamespace, topology.Internet4, topology.Internet6, "official-client")
	clientRules := lab.inNamespace(topology.ClientNamespace, "nft", "-j", "list", "table", "inet", "hydra_protect")
	requireCounter(t, clientRules, "reject-safety-v0-tcp", uint64(len(safetyBlockedTCP)*2))
	requireCounter(t, clientRules, "reject-safety-v0-udp", uint64(len(safetyBlockedUDP)*2))
	exitRules = lab.inNamespace(topology.ExitNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	if got := counterValue(t, exitRules, "drop-safety-v0-tcp"); got != exitTCPBefore {
		t.Fatalf("client-blocked TCP reached Exit: before=%d after=%d", exitTCPBefore, got)
	}
	if got := counterValue(t, exitRules, "drop-safety-v0-udp"); got != exitUDPBefore {
		t.Fatalf("client-blocked UDP reached Exit: before=%d after=%d", exitUDPBefore, got)
	}
	if observedAfter := countM8Observations(t, observations); observedAfter != observedBefore {
		t.Fatalf("client-blocked packets reached Internet: before=%d after=%d", observedBefore, observedAfter)
	}
	assertSafetyAllowedTraffic(t, lab, topology, observations)

	if err := touch(policyRelease); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, policy)
	if _, err := runM8Command("ip", "netns", "exec", topology.ClientNamespace, "nft", "list", "table", "inet", "hydra_protect"); err == nil {
		t.Fatal("client safety policy remained after explicit disconnect")
	}

	for _, path := range []string{client.releasePath, exit.releasePath, serverRelease} {
		if err := touch(path); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, process := range []*childProcess{client.child, exit.child, server} {
		if err := process.wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func assertFragmentedSafetyBlocked(t *testing.T, lab *lab, topology m7Topology) {
	t.Helper()
	pcap := filepath.Join(lab.artifactDir, "safety-fragments.pcap")
	capture := lab.startChild("safety-fragment-capture", topology.ExitNamespace, "/usr/bin/tcpdump",
		"-U", "-n", "-i", "hydra0", "-c", "20", "-w", pcap,
		"(ip[6:2] & 0x3fff != 0) or (ip6[6] == 44)")
	time.Sleep(150 * time.Millisecond)
	for _, item := range []struct {
		kind   string
		source netip.Addr
		target netip.AddrPort
	}{
		{"tcp", netip.MustParseAddr("10.77.0.2"), netip.AddrPortFrom(topology.Internet4, 25)},
		{"tcp", netip.MustParseAddr("10.77.0.2"), netip.AddrPortFrom(topology.Internet4, 445)},
		{"udp", netip.MustParseAddr("10.77.0.2"), netip.AddrPortFrom(topology.Internet4, 19)},
		{"udp", netip.MustParseAddr("10.77.0.2"), netip.AddrPortFrom(topology.Internet4, 1900)},
		{"udp", netip.MustParseAddr("10.77.0.2"), netip.AddrPortFrom(topology.Internet4, 11211)},
		{"tcp", netip.MustParseAddr("fd77::2"), netip.AddrPortFrom(topology.Internet6, 25)},
		{"tcp", netip.MustParseAddr("fd77::2"), netip.AddrPortFrom(topology.Internet6, 445)},
		{"udp", netip.MustParseAddr("fd77::2"), netip.AddrPortFrom(topology.Internet6, 19)},
		{"udp", netip.MustParseAddr("fd77::2"), netip.AddrPortFrom(topology.Internet6, 1900)},
		{"udp", netip.MustParseAddr("fd77::2"), netip.AddrPortFrom(topology.Internet6, 11211)},
	} {
		child := lab.startChild(fmt.Sprintf("safety-fragment-%s-%d-%s", item.kind, item.target.Port(),
			strings.ReplaceAll(item.source.String(), ":", "")),
			topology.ClientNamespace, os.Getenv("HYDRA_M7_INTERNET"),
			"--mode", "fragment", "--kind", item.kind, "--source", item.source.String(), "--target", item.target.String())
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := child.wait(ctx)
		cancel()
		if err != nil {
			t.Fatalf("send %s fragments from %s: %v", item.kind, item.source, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := capture.wait(ctx)
	cancel()
	if err != nil {
		t.Fatalf("fragment capture: %v", err)
	}
	info, err := os.Stat(pcap)
	if err != nil || info.Size() <= 24 {
		t.Fatalf("fragment pcap missing or empty: size=%d error=%v", func() int64 {
			if info == nil {
				return 0
			}
			return info.Size()
		}(), err)
	}
	decoded := lab.inNamespace(topology.ExitNamespace, "tcpdump", "-nn", "-r", pcap)
	if !strings.Contains(decoded, "frag") {
		t.Fatalf("capture does not contain fragmented inner traffic: %s", decoded)
	}
}

func assertSafetyMatrixBlocked(t *testing.T, lab *lab, namespace string, ipv4, ipv6 netip.Addr, label string) {
	t.Helper()
	for _, family := range []netip.Addr{ipv4, ipv6} {
		for _, port := range safetyBlockedTCP {
			if _, err := tryM7Probe(t, lab, namespace, "tcp", netip.AddrPortFrom(family, port), "--probe-timeout", "350ms"); err == nil {
				t.Fatalf("%s TCP/%d %s bypassed safety policy", label, port, family)
			}
		}
		for _, port := range safetyBlockedUDP {
			if _, err := tryM7Probe(t, lab, namespace, "udp", netip.AddrPortFrom(family, port), "--probe-timeout", "350ms"); err == nil {
				t.Fatalf("%s UDP/%d %s bypassed safety policy", label, port, family)
			}
		}
	}
}

func assertSafetyAllowedTraffic(t *testing.T, lab *lab, topology m7Topology, observations string) {
	t.Helper()
	before := countM8Observations(t, observations)
	for _, family := range []struct {
		internet, nat, inner netip.Addr
	}{
		{topology.Internet4, topology.ExitWAN4, netip.MustParseAddr("10.77.0.2")},
		{topology.Internet6, topology.ExitWAN6, netip.MustParseAddr("fd77::2")},
	} {
		for _, port := range safetyAllowedTCP {
			assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "tcp", netip.AddrPortFrom(family.internet, port)), family.nat, family.inner)
		}
		for _, port := range safetyAllowedUDP {
			assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "udp", netip.AddrPortFrom(family.internet, port)), family.nat, family.inner)
		}
		assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(family.internet, 80)), family.nat, family.inner)
		for _, kind := range []string{"dns-udp", "dns-tcp"} {
			result := runM7Probe(t, lab, topology.ClientNamespace, kind, netip.AddrPortFrom(family.internet, 53))
			if !result.Success || result.DNSAnswer != "198.51.100.77" {
				t.Fatalf("%s result=%+v", kind, result)
			}
		}
	}
	if after := countM8Observations(t, observations); after <= before {
		t.Fatalf("allowed traffic produced no Internet observations: before=%d after=%d", before, after)
	}
}

func safetyOracleRules(topology m7Topology) string {
	return fmt.Sprintf(`table inet m7_safety_oracle {
 chain inner {
  type filter hook forward priority -10; policy accept;
  iifname "hydra0" meta l4proto tcp th dport { %s } counter comment "safety-inner-tcp"
  iifname "hydra0" meta l4proto udp th dport { %s } counter comment "safety-inner-udp"
 }
 chain wan {
  type filter hook forward priority 10; policy accept;
  oifname "%s" meta l4proto tcp th dport { %s } counter comment "safety-wan-tcp"
  oifname "%s" meta l4proto udp th dport { %s } counter comment "safety-wan-udp"
 }
}
`, joinPorts(safetyBlockedTCP), joinPorts(safetyBlockedUDP), topology.ExitWANIf,
		joinPorts(safetyBlockedTCP), topology.ExitWANIf, joinPorts(safetyBlockedUDP))
}

func joinPorts(ports []uint16) string {
	values := make([]string, 0, len(ports))
	for _, port := range ports {
		values = append(values, fmt.Sprint(port))
	}
	return strings.Join(values, ", ")
}

func counterValue(t *testing.T, raw, label string) uint64 {
	t.Helper()
	value, ok := counterForComment(raw, label)
	if !ok {
		t.Fatalf("missing nft counter %q", label)
	}
	return value
}

func assertZeroCounter(t *testing.T, raw, label string) {
	t.Helper()
	if value := counterValue(t, raw, label); value != 0 {
		t.Fatalf("counter %q=%d, want zero", label, value)
	}
}
