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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const m7OuterMark = 0x48594452

var m7ProbeCounter atomic.Uint64

type m7Topology struct {
	ClientNamespace   string     `json:"client_namespace"`
	ExitNamespace     string     `json:"exit_namespace"`
	InternetNamespace string     `json:"internet_namespace"`
	ClientOuterIf     string     `json:"client_outer_interface"`
	ExitOuterIf       string     `json:"exit_outer_interface"`
	ExitWANIf         string     `json:"exit_wan_interface"`
	InternetIf        string     `json:"internet_interface"`
	ClientOuter4      netip.Addr `json:"client_outer_ipv4"`
	ExitOuter4        netip.Addr `json:"exit_outer_ipv4"`
	ClientOuter6      netip.Addr `json:"client_outer_ipv6"`
	ExitOuter6        netip.Addr `json:"exit_outer_ipv6"`
	ExitWAN4          netip.Addr `json:"exit_wan_ipv4"`
	Internet4         netip.Addr `json:"internet_ipv4"`
	ExitWAN6          netip.Addr `json:"exit_wan_ipv6"`
	Internet6         netip.Addr `json:"internet_ipv6"`
}

type m7ProbeResult struct {
	Kind           string `json:"kind"`
	Target         string `json:"target"`
	LocalEndpoint  string `json:"local_endpoint"`
	ObservedSource string `json:"observed_source"`
	DNSAnswer      string `json:"dns_answer"`
	Success        bool   `json:"success"`
}

type m7Observation struct {
	Kind           string `json:"kind"`
	Family         string `json:"family"`
	ObservedSource string `json:"observed_source"`
}

func TestM7_EncryptedInternetExit(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	tests := []struct {
		name                string
		outer4, outer6      bool
		protect4, protect6  bool
		reject4, reject6    bool
		probe4, probe6, dns bool
	}{
		{name: "outer-udp4-dual-inner", outer4: true, protect4: true, protect6: true, probe4: true, probe6: true, dns: true},
		{name: "outer-udp6-inner-ipv4", outer6: true, protect4: true, protect6: true, probe4: true},
		{name: "unsupported-ipv6-explicit-unreachable", outer4: true, protect4: true, reject6: true, probe4: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lab := newLab(t, "m7-"+test.name)
			topology := buildM7Topology(lab)
			lab.installNFT(topology.ClientNamespace, "m7-client-oracle", m7ClientOracleRules(topology))
			server, serverRelease, observations := startM7InternetServer(t, lab, topology)

			exit, exitReady := startM7Peer(t, lab, topology, "responder", test, "")
			family := "ipv4"
			if test.outer6 {
				family = "ipv6"
			}
			remote := selectSignalAddress(t, exitReady.ListenAddresses, family)
			client, clientReady := startM7Peer(t, lab, topology, "initiator", test, remote)
			waitDirectAWGReady(t, client, exit)
			if err := touch(exit.awgStartPath); err != nil {
				t.Fatal(err)
			}
			if err := touch(client.awgStartPath); err != nil {
				t.Fatal(err)
			}
			clientResult := waitConnectReport(t, client)
			exitResult := waitConnectReport(t, exit)
			assertConnectedReports(t, clientResult, exitResult, test.outer4, test.outer6)
			assertM7Ownership(t, clientReady, clientResult, exitReady, exitResult)

			if test.probe4 {
				assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
				assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "udp", endpointValue(topology.Internet4, 18081)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
			}
			if test.probe6 {
				assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet6, 18080)), topology.ExitWAN6, netip.MustParseAddr("fd77::2"))
				assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "udp", endpointValue(topology.Internet6, 18081)), topology.ExitWAN6, netip.MustParseAddr("fd77::2"))
			}
			if test.dns {
				for _, kind := range []string{"dns-udp", "dns-tcp"} {
					result := runM7Probe(t, lab, topology.ClientNamespace, kind, endpointValue(topology.Internet4, 1053))
					if !result.Success || result.DNSAnswer != "198.51.100.77" {
						t.Fatalf("%s result=%+v", kind, result)
					}
				}
			}
			if test.reject6 {
				assertM7ProbeFails(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet6, 18080))
			}
			assertM7ProbeFails(t, lab, topology.ClientNamespace, "http", endpointValue(topology.ExitWAN4, 18080))
			assertM7ProbeFails(t, lab, topology.ClientNamespace, "http", endpointValue(netip.MustParseAddr("10.77.0.3"), 18080))
			assertM7KernelEvidence(t, lab, topology, test, observations)

			for _, path := range []string{client.releasePath, exit.releasePath, serverRelease} {
				if err := touch(path); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			for _, process := range []*childProcess{client.child, exit.child, server} {
				if err := process.wait(ctx); err != nil {
					t.Fatal(err)
				}
			}
			lab.inNamespace(topology.ClientNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
			lab.inNamespace(topology.ExitNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
			lab.captureNamespace(topology.ClientNamespace, "m7-client", false)
			lab.captureNamespace(topology.ExitNamespace, "m7-exit", true)
			lab.captureNamespace(topology.InternetNamespace, "m7-internet", false)
		})
	}
}

func buildM7Topology(lab *lab) m7Topology {
	lab.t.Helper()
	topology := m7Topology{
		ClientNamespace: lab.namespace("client"), ExitNamespace: lab.namespace("exit"), InternetNamespace: lab.namespace("internet"),
		ClientOuterIf: lab.interfaceName('c'), ExitOuterIf: lab.interfaceName('e'), ExitWANIf: lab.interfaceName('w'), InternetIf: lab.interfaceName('i'),
		ClientOuter4: netip.MustParseAddr("198.18.77.1"), ExitOuter4: netip.MustParseAddr("198.18.77.2"),
		ClientOuter6: netip.MustParseAddr("2001:db8:77::1"), ExitOuter6: netip.MustParseAddr("2001:db8:77::2"),
		ExitWAN4: netip.MustParseAddr("203.0.113.1"), Internet4: netip.MustParseAddr("203.0.113.2"),
		ExitWAN6: netip.MustParseAddr("2001:db8:78::1"), Internet6: netip.MustParseAddr("2001:db8:78::2"),
	}
	lab.createVeth(topology.ClientNamespace, topology.ClientOuterIf, topology.ExitNamespace, topology.ExitOuterIf)
	lab.createVeth(topology.ExitNamespace, topology.ExitWANIf, topology.InternetNamespace, topology.InternetIf)
	for _, namespace := range []string{topology.ClientNamespace, topology.ExitNamespace, topology.InternetNamespace} {
		lab.inNamespace(namespace, "ip", "link", "set", "lo", "up")
	}
	for namespace, name := range map[string]string{topology.ClientNamespace: topology.ClientOuterIf, topology.ExitNamespace: topology.ExitOuterIf, topology.InternetNamespace: topology.InternetIf} {
		lab.inNamespace(namespace, "ip", "link", "set", name, "up")
	}
	lab.inNamespace(topology.ExitNamespace, "ip", "link", "set", topology.ExitWANIf, "up")
	for _, item := range []struct {
		namespace, iface string
		ipv4, ipv6       netip.Addr
	}{
		{topology.ClientNamespace, topology.ClientOuterIf, topology.ClientOuter4, topology.ClientOuter6},
		{topology.ExitNamespace, topology.ExitOuterIf, topology.ExitOuter4, topology.ExitOuter6},
		{topology.ExitNamespace, topology.ExitWANIf, topology.ExitWAN4, topology.ExitWAN6},
		{topology.InternetNamespace, topology.InternetIf, topology.Internet4, topology.Internet6},
	} {
		lab.inNamespace(item.namespace, "ip", "addr", "add", item.ipv4.String()+"/24", "dev", item.iface)
		lab.inNamespace(item.namespace, "ip", "-6", "addr", "add", item.ipv6.String()+"/64", "dev", item.iface, "nodad")
	}
	lab.recordTopology(topology)
	return topology
}

func startM7InternetServer(t *testing.T, lab *lab, topology m7Topology, extraArgs ...string) (*childProcess, string, string) {
	t.Helper()
	ready := filepath.Join(lab.artifactDir, "m7-internet-ready")
	release := filepath.Join(lab.artifactDir, "m7-internet-release")
	observations := filepath.Join(lab.artifactDir, "m7-internet-observations.jsonl")
	args := []string{"--mode", "server", "--ready", ready, "--release", release, "--observations", observations}
	if topology.Internet4.IsValid() {
		args = append(args, "--listen4", topology.Internet4.String())
	}
	if topology.Internet6.IsValid() {
		args = append(args, "--listen6", topology.Internet6.String())
	}
	args = append(args, extraArgs...)
	child := lab.startChild("m7-internet", topology.InternetNamespace, os.Getenv("HYDRA_M7_INTERNET"), args...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, ready); err != nil {
		t.Fatal(err)
	}
	return child, release, observations
}

func startM7Peer(t *testing.T, lab *lab, topology m7Topology, side string, test struct {
	name                string
	outer4, outer6      bool
	protect4, protect6  bool
	reject4, reject6    bool
	probe4, probe6, dns bool
}, remote string, extraArgs ...string) (connectChild, iceSignalReport) {
	t.Helper()
	expectedPort := clientMuxPort
	for index := 0; index+1 < len(extraArgs); index++ {
		if extraArgs[index] != "--port" {
			continue
		}
		value, err := strconv.Atoi(extraArgs[index+1])
		if err != nil || value < 1 || value > 65535 {
			t.Fatalf("invalid M7 test port %q", extraArgs[index+1])
		}
		expectedPort = value
	}
	label := "m7-" + side
	ready := filepath.Join(lab.artifactDir, label+"-ready.json")
	result := filepath.Join(lab.artifactDir, label+"-result.json")
	release := filepath.Join(lab.artifactDir, label+"-release")
	awgReady := filepath.Join(lab.artifactDir, label+"-awg-ready.json")
	awgStart := filepath.Join(lab.artifactDir, label+"-awg-start")
	args := []string{"--mode", "connect-" + side, "--port", strconv.Itoa(clientMuxPort), "--ready-file", ready, "--result-file", result, "--release-file", release, "--timeout", "30s"}
	if test.outer4 {
		address := topology.ExitOuter4
		if side == "initiator" {
			address = topology.ClientOuter4
		}
		args = append(args, "--bind4", address.String(), "--tcp4", address.String())
	}
	if test.outer6 {
		address := topology.ExitOuter6
		if side == "initiator" {
			address = topology.ClientOuter6
		}
		args = append(args, "--bind6", address.String(), "--tcp6", address.String())
	}
	if side == "initiator" {
		args = append(args, "--remote", remote,
			"--awg-inner-local", "10.77.0.2", "--awg-inner-remote", "10.77.0.1", "--awg-local-key-seed", "71", "--awg-remote-key-seed", "91",
			"--awg-tun-addresses", "10.77.0.2/32,fd77::2/128", "--awg-allowed-ips", "0.0.0.0/0,::/0",
			"--m7-role", "client", "--m7-wan-name", topology.ClientOuterIf,
			"--m7-bypass", netip.PrefixFrom(topology.ClientOuter4, 24).Masked().String()+","+netip.PrefixFrom(topology.ClientOuter6, 64).Masked().String())
		if test.protect4 {
			args = append(args, "--m7-protect4")
		}
		if test.protect6 {
			args = append(args, "--m7-protect6")
		}
		if test.reject4 {
			args = append(args, "--m7-reject4")
		}
		if test.reject6 {
			args = append(args, "--m7-reject6")
		}
		if test.outer4 {
			args = append(args, "--m7-wan4-source", topology.ClientOuter4.String(), "--m7-wan4-connected", netip.PrefixFrom(topology.ClientOuter4, 24).Masked().String())
		}
		if test.outer6 {
			args = append(args, "--m7-wan6-source", topology.ClientOuter6.String(), "--m7-wan6-connected", netip.PrefixFrom(topology.ClientOuter6, 64).Masked().String())
		}
	} else {
		args = append(args,
			"--awg-inner-local", "10.77.0.1", "--awg-inner-remote", "10.77.0.2", "--awg-local-key-seed", "91", "--awg-remote-key-seed", "71",
			"--awg-tun-addresses", "10.77.0.1/24,fd77::1/64", "--awg-allowed-ips", "10.77.0.2/32,fd77::2/128",
			"--m7-role", "exit", "--m7-wan-name", topology.ExitWANIf, "--m7-client-pool4", "10.77.0.0/24", "--m7-client-pool6", "fd77::/64",
			"--m7-management", topology.ExitWAN4.String()+"/32,"+topology.ExitWAN6.String()+"/128")
	}
	args = append(args, "--awg", "--awg-real-tun", "--awg-tun-name", "hydra0", "--awg-ready-file", awgReady, "--awg-start-file", awgStart)
	args = append(args, extraArgs...)
	child := lab.startChild(label, map[bool]string{true: topology.ClientNamespace, false: topology.ExitNamespace}[side == "initiator"], os.Getenv("HYDRA_M5B_SIGNALHELPER"), args...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, ready); err != nil {
		t.Fatal(err)
	}
	var report iceSignalReport
	if err := readJSON(ready, &report); err != nil {
		t.Fatal(err)
	}
	if report.Error != "" || report.MuxPort != uint16(expectedPort) {
		t.Fatalf("invalid M7 readiness: %+v", report)
	}
	return connectChild{child: child, resultPath: result, releasePath: release, awgReadyPath: awgReady, awgStartPath: awgStart}, report
}

func runM7Probe(t *testing.T, lab *lab, namespace, kind string, target netip.AddrPort, extraArgs ...string) m7ProbeResult {
	t.Helper()
	result, err := tryM7Probe(t, lab, namespace, kind, target, extraArgs...)
	if err != nil {
		t.Fatalf("%s probe: %v", kind, err)
	}
	return result
}

func tryM7Probe(t *testing.T, lab *lab, namespace, kind string, target netip.AddrPort, extraArgs ...string) (m7ProbeResult, error) {
	t.Helper()
	label := "m7-probe-" + strconv.FormatUint(m7ProbeCounter.Add(1), 10) + "-" + strings.ReplaceAll(kind, "-", "") + "-" + strings.ReplaceAll(target.Addr().String(), ":", "")
	resultPath := filepath.Join(lab.artifactDir, label+".json")
	args := []string{"--mode", "probe", "--kind", kind, "--target", target.String(), "--result", resultPath}
	args = append(args, extraArgs...)
	child := lab.startChild(label, namespace, os.Getenv("HYDRA_M7_INTERNET"), args...)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := child.wait(ctx); err != nil {
		return m7ProbeResult{}, err
	}
	var result m7ProbeResult
	if err := readJSON(resultPath, &result); err != nil {
		return m7ProbeResult{}, err
	}
	return result, nil
}

func assertM7ProbeFails(t *testing.T, lab *lab, namespace, kind string, target netip.AddrPort) {
	t.Helper()
	label := "m7-blocked-" + strings.ReplaceAll(target.Addr().String(), ":", "")
	resultPath := filepath.Join(lab.artifactDir, label+".json")
	child := lab.startChild(label, namespace, os.Getenv("HYDRA_M7_INTERNET"), "--mode", "probe", "--kind", kind, "--target", target.String(), "--result", resultPath)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	if err := child.wait(ctx); err == nil {
		t.Fatalf("protected probe unexpectedly reached %s", target)
	}
}

func assertM7Probe(t *testing.T, result m7ProbeResult, expectedNAT, expectedInner netip.Addr) {
	t.Helper()
	observed, err := netip.ParseAddrPort(result.ObservedSource)
	if err != nil || !result.Success || observed.Addr() != expectedNAT {
		t.Fatalf("Internet source result=%+v expected=%s", result, expectedNAT)
	}
	local, err := netip.ParseAddrPort(result.LocalEndpoint)
	if err != nil || local.Addr() != expectedInner {
		t.Fatalf("client inner source=%q expected=%s", result.LocalEndpoint, expectedInner)
	}
}

func assertM7Ownership(t *testing.T, clientReady, client, exitReady, exit iceSignalReport) {
	t.Helper()
	for name, report := range map[string]iceSignalReport{"client": client, "exit": exit} {
		if !report.AWGHandshake || report.AWGTUNName != "hydra0" || report.AWGTUNIfIndex <= 0 || report.AWGSocketMark != m7OuterMark || report.AWGSocketInode == "" ||
			report.AWGSocketInode != report.UDP4SocketInodeReady && report.AWGSocketInode != report.UDP6SocketInodeReady || report.AWGSelectedRemote != report.SelectedRemote ||
			report.ApplicationPackets != 0 || report.ApplicationBytes != 0 {
			t.Fatalf("%s ownership report=%+v", name, report)
		}
	}
	if client.AWGTUNIfIndex != clientReady.AWGTUNIfIndex && clientReady.AWGTUNIfIndex != 0 || exit.AWGTUNIfIndex != exitReady.AWGTUNIfIndex && exitReady.AWGTUNIfIndex != 0 {
		t.Fatal("TUN identity changed while peer started")
	}
	if client.RoutingOutTable != 51820 || client.RoutingWANTable != 51821 || client.RoutingRules == 0 || client.RoutingRoutes == 0 || exit.ExitFirewallTable != "hydra_exit" || exit.ExitFirewallRules == 0 {
		t.Fatalf("M7 policy missing client=%+v exit=%+v", client, exit)
	}
}

func assertM7KernelEvidence(t *testing.T, lab *lab, topology m7Topology, test struct {
	name                string
	outer4, outer6      bool
	protect4, protect6  bool
	reject4, reject6    bool
	probe4, probe6, dns bool
}, observationPath string) {
	t.Helper()
	rules := lab.inNamespace(topology.ClientNamespace, "nft", "-j", "list", "ruleset")
	requireCounter(t, rules, "m7-outer-marked", 1)
	requireCounter(t, rules, "m7-inner-protected", 1)
	if packets, ok := counterForComment(rules, "m7-outer-unmarked"); !ok || packets != 0 {
		t.Fatalf("unmarked Irvio outer packets=%d present=%v", packets, ok)
	}
	if packets, ok := counterForComment(rules, "m7-direct-bypass"); !ok || packets != 0 {
		t.Fatalf("protected packets bypassed hydra0=%d present=%v", packets, ok)
	}
	exitRules := lab.inNamespace(topology.ExitNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	if test.probe4 {
		requireCounter(t, exitRules, "masquerade-ipv4", 1)
	}
	if test.probe6 {
		requireCounter(t, exitRules, "masquerade-ipv6", 1)
	}
	requireCounter(t, exitRules, "drop-hydra-to-exit", 1)
	requireCounter(t, exitRules, "drop-hydra-multihop", 1)
	data, err := os.Open(observationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(data)
	for scanner.Scan() {
		var item m7Observation
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			t.Fatal(err)
		}
		seen[item.Kind] = true
		source, err := netip.ParseAddrPort(item.ObservedSource)
		if err != nil || source.Addr() != topology.ExitWAN4 && source.Addr() != topology.ExitWAN6 {
			t.Fatalf("Internet observation=%+v", item)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if test.dns && (!seen["dns-udp"] || !seen["dns-tcp"]) {
		t.Fatalf("DNS observations=%v", seen)
	}
	rules4 := lab.inNamespace(topology.ClientNamespace, "ip", "-j", "rule", "show")
	rules6 := lab.inNamespace(topology.ClientNamespace, "ip", "-j", "-6", "rule", "show")
	if !strings.Contains(rules4+rules6, "51820") || !strings.Contains(rules4+rules6, "51821") {
		t.Fatalf("client policy rules IPv4=%s IPv6=%s", rules4, rules6)
	}
}

func m7ClientOracleRules(topology m7Topology) string {
	directRules := ""
	if topology.Internet4.IsValid() {
		directRules += fmt.Sprintf("  oifname \"%s\" ip daddr %s counter comment \"m7-direct-bypass\"\n", topology.ClientOuterIf, topology.Internet4)
	}
	if topology.Internet6.IsValid() {
		directRules += fmt.Sprintf("  oifname \"%s\" ip6 daddr %s counter comment \"m7-direct-bypass\"\n", topology.ClientOuterIf, topology.Internet6)
	}
	return fmt.Sprintf(`table inet m7_oracle {
 chain output {
  type filter hook output priority -10; policy accept;
  udp sport %d meta mark 0x%x counter comment "m7-outer-marked"
  udp sport %d meta mark != 0x%x counter comment "m7-outer-unmarked"
  oifname "hydra0" counter comment "m7-inner-protected"
%s
 }
}
`, clientMuxPort, m7OuterMark, clientMuxPort, m7OuterMark, directRules)
}
