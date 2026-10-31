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
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestM5C1_DirectSocketOwnership(t *testing.T) {
	requirePrivileged(t)
	tests := []struct {
		name string
		v4   bool
		v6   bool
	}{
		{name: "ipv4", v4: true},
		{name: "ipv6", v6: true},
		{name: "dual-stack", v4: true, v6: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lab := newLab(t, "m5c1-"+test.name)
			topology := lab.buildDirectDualStack()
			beforeClient4, beforeClient6 := namespaceUDPSockets(t, lab, topology.ClientNamespace)
			beforeServer4, beforeServer6 := namespaceUDPSockets(t, lab, topology.ServerNamespace)
			if beforeClient4 != 0 || beforeClient6 != 0 || beforeServer4 != 0 || beforeServer6 != 0 {
				t.Fatalf("fresh namespaces have UDP sockets: client=%d/%d server=%d/%d", beforeClient4, beforeClient6, beforeServer4, beforeServer6)
			}
			lab.installNFT(topology.ClientNamespace, "m5c1-client-oracle", directICEOracleRules(
				topology.ClientIPv4, topology.ServerIPv4A1, topology.ClientIPv6, topology.ServerIPv6A1, test.v4, test.v6))
			lab.installNFT(topology.ServerNamespace, "m5c1-server-oracle", directICEOracleRules(
				topology.ServerIPv4A1, topology.ClientIPv4, topology.ServerIPv6A1, topology.ClientIPv6, test.v4, test.v6))

			responder, ready := startConnectHelper(t, lab, "responder", topology.ServerNamespace,
				topology.ServerIPv4A1, topology.ServerIPv6A1, test.v4, test.v6, "")
			family := "ipv4"
			if !test.v4 {
				family = "ipv6"
			}
			remote := selectSignalAddress(t, ready.ListenAddresses, family)
			initiator, _ := startConnectHelper(t, lab, "initiator", topology.ClientNamespace,
				topology.ClientIPv4, topology.ClientIPv6, test.v4, test.v6, remote)
			initiatorReport := waitConnectReport(t, initiator)
			responderReport := waitConnectReport(t, responder)
			assertConnectedReports(t, initiatorReport, responderReport, test.v4, test.v6)

			client4, client6 := namespaceUDPSockets(t, lab, topology.ClientNamespace)
			server4, server6 := namespaceUDPSockets(t, lab, topology.ServerNamespace)
			want4, want6 := 0, 0
			if test.v4 {
				want4 = 1
			}
			if test.v6 {
				want6 = 1
			}
			if client4 != want4 || client6 != want6 || server4 != want4 || server6 != want6 {
				t.Fatalf("unexpected physical UDP sockets client=%d/%d server=%d/%d want=%d/%d", client4, client6, server4, server6, want4, want6)
			}
			assertDirectOracle(t, lab, topology.ClientNamespace, test.v4, test.v6)
			assertDirectOracle(t, lab, topology.ServerNamespace, test.v4, test.v6)

			if err := touch(initiator.releasePath); err != nil {
				t.Fatal(err)
			}
			if err := touch(responder.releasePath); err != nil {
				t.Fatal(err)
			}
			waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := initiator.child.wait(waitContext); err != nil {
				t.Fatal(err)
			}
			if err := responder.child.wait(waitContext); err != nil {
				t.Fatal(err)
			}
			lab.captureNamespace(topology.ClientNamespace, "m5c1-client", true)
			lab.captureNamespace(topology.ServerNamespace, "m5c1-server", true)
		})
	}
}

func TestM5C1_DirectUDPBlockFailsBoundedly(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "m5c1-block")
	topology := lab.buildDirectDualStack()
	lab.installNFT(topology.ClientNamespace, "m5c1-block-client", directICEDropRules(topology.ServerIPv4A1, topology.ServerIPv6A1))
	lab.installNFT(topology.ServerNamespace, "m5c1-block-server", directICEDropRules(topology.ClientIPv4, topology.ClientIPv6))
	responder, ready := startConnectFailureHelper(t, lab, "responder", topology.ServerNamespace,
		topology.ServerIPv4A1, topology.ServerIPv6A1, "")
	remote := selectSignalAddress(t, ready.ListenAddresses, "ipv4")
	initiator, _ := startConnectFailureHelper(t, lab, "initiator", topology.ClientNamespace,
		topology.ClientIPv4, topology.ClientIPv6, remote)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	for _, process := range []connectChild{initiator, responder} {
		if err := process.child.wait(ctx); err == nil {
			t.Fatal("UDP-blocked helper unexpectedly succeeded")
		}
		var result iceSignalReport
		if err := readJSON(process.resultPath, &result); err != nil {
			t.Fatal(err)
		}
		if result.Error == "" || result.Connected || result.SelectedLocal != "" || result.SelectedRemote != "" {
			t.Fatalf("UDP-blocked result falsely connected: %+v", result)
		}
	}
}

type connectChild struct {
	child        *childProcess
	resultPath   string
	releasePath  string
	awgReadyPath string
	awgStartPath string
}

func startConnectHelper(t *testing.T, lab *lab, side, namespace string, ipv4, ipv6 netip.Addr, use4, use6 bool, remote string) (connectChild, iceSignalReport) {
	t.Helper()
	return startConnectHelperWithMode(t, lab, side, namespace, ipv4, ipv6, use4, use6, remote, true, 12*time.Second, false)
}

func startConnectFailureHelper(t *testing.T, lab *lab, side, namespace string, ipv4, ipv6 netip.Addr, remote string) (connectChild, iceSignalReport) {
	t.Helper()
	return startConnectHelperWithMode(t, lab, side, namespace, ipv4, ipv6, true, true, remote, false, 2*time.Second, false)
}

func startM6DirectHelper(t *testing.T, lab *lab, side, namespace string, ipv4, ipv6 netip.Addr, use4, use6 bool, remote string) (connectChild, iceSignalReport) {
	t.Helper()
	return startConnectHelperWithMode(t, lab, side, namespace, ipv4, ipv6, use4, use6, remote, true, 20*time.Second, true)
}

func startConnectHelperWithMode(t *testing.T, lab *lab, side, namespace string, ipv4, ipv6 netip.Addr, use4, use6 bool, remote string, hold bool, timeout time.Duration, withAWG bool) (connectChild, iceSignalReport) {
	t.Helper()
	prefix := "m5c1-"
	if withAWG {
		prefix = "m6-direct-"
	}
	label := prefix + side
	readyPath := filepath.Join(lab.artifactDir, label+"-ready.json")
	resultPath := filepath.Join(lab.artifactDir, label+"-result.json")
	releasePath := filepath.Join(lab.artifactDir, label+"-release")
	awgReadyPath := filepath.Join(lab.artifactDir, label+"-awg-ready.json")
	awgStartPath := filepath.Join(lab.artifactDir, label+"-awg-start")
	mode := "connect-" + side
	args := []string{
		"--mode", mode, "--port", strconv.Itoa(clientMuxPort),
		"--ready-file", readyPath, "--result-file", resultPath, "--timeout", timeout.String(),
	}
	if hold {
		args = append(args, "--release-file", releasePath)
	}
	if use4 {
		args = append(args, "--bind4", ipv4.String(), "--tcp4", ipv4.String())
	}
	if use6 {
		args = append(args, "--bind6", ipv6.String(), "--tcp6", ipv6.String())
	}
	if side == "initiator" {
		args = append(args, "--remote", remote)
	}
	if withAWG {
		localInner, remoteInner := "10.99.0.2", "10.99.0.1"
		localSeed, remoteSeed := "91", "71"
		if side == "initiator" {
			localInner, remoteInner = "10.99.0.1", "10.99.0.2"
			localSeed, remoteSeed = "71", "91"
		}
		args = append(args,
			"--awg", "--awg-ready-file", awgReadyPath, "--awg-start-file", awgStartPath,
			"--awg-inner-local", localInner, "--awg-inner-remote", remoteInner,
			"--awg-local-key-seed", localSeed, "--awg-remote-key-seed", remoteSeed,
		)
	}
	child := lab.startChild(label, namespace, os.Getenv("HYDRA_M5B_SIGNALHELPER"), args...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, readyPath); err != nil {
		t.Fatal(err)
	}
	var ready iceSignalReport
	if err := readJSON(readyPath, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.Error != "" || ready.MuxPort != clientMuxPort {
		t.Fatalf("invalid M5c1 readiness: %+v", ready)
	}
	return connectChild{
		child: child, resultPath: resultPath, releasePath: releasePath,
		awgReadyPath: awgReadyPath, awgStartPath: awgStartPath,
	}, ready
}

func waitConnectReport(t *testing.T, process connectChild) iceSignalReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, process.child, process.resultPath); err != nil {
		t.Fatal(err)
	}
	var result iceSignalReport
	if err := readJSON(process.resultPath, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("M5c1 helper error: %s", result.Error)
	}
	return result
}

func assertConnectedReports(t *testing.T, initiator, responder iceSignalReport, allow4, allow6 bool) {
	t.Helper()
	if !initiator.Connected || !responder.Connected || initiator.SessionID == "" || initiator.SessionID != responder.SessionID ||
		initiator.SelectedLocal != responder.SelectedRemote || initiator.SelectedRemote != responder.SelectedLocal ||
		initiator.SelectedNetwork != responder.SelectedNetwork || initiator.SelectedLocalType != 1 || initiator.SelectedRemoteType != 1 ||
		responder.SelectedLocalType != 1 || responder.SelectedRemoteType != 1 || !initiator.Nominated || !responder.Nominated ||
		initiator.PairID == 0 || responder.PairID == 0 || initiator.RequestsSent == 0 || responder.RequestsSent == 0 ||
		initiator.ResponsesReceived == 0 || responder.ResponsesReceived == 0 ||
		initiator.AdapterAfter <= initiator.AdapterBefore || responder.AdapterAfter <= responder.AdapterBefore ||
		initiator.ApplicationPackets != 0 || responder.ApplicationPackets != 0 ||
		initiator.ApplicationBytes != 0 || responder.ApplicationBytes != 0 {
		t.Fatalf("invalid connected reports: initiator=%+v responder=%+v", initiator, responder)
	}
	if initiator.SelectedNetwork == 1 && !allow4 || initiator.SelectedNetwork == 2 && !allow6 {
		t.Fatalf("selected disabled family %d", initiator.SelectedNetwork)
	}
}

func namespaceUDPSockets(t *testing.T, lab *lab, namespace string) (int, int) {
	t.Helper()
	return countProcUDPSockets(lab.inNamespace(namespace, "cat", "/proc/net/udp")),
		countProcUDPSockets(lab.inNamespace(namespace, "cat", "/proc/net/udp6"))
}

func countProcUDPSockets(raw string) int {
	count := 0
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.Contains(fields[1], ":") && fields[0] != "sl" {
			count++
		}
	}
	return count
}

func assertDirectOracle(t *testing.T, lab *lab, namespace string, use4, use6 bool) {
	t.Helper()
	rules := lab.inNamespace(namespace, "nft", "-j", "list", "ruleset")
	for _, family := range []struct {
		name string
		use  bool
	}{{"v4", use4}, {"v6", use6}} {
		if !family.use {
			continue
		}
		good, goodOK := counterForComment(rules, "m5c1-"+family.name+"-mux")
		bad, badOK := counterForComment(rules, "m5c1-"+family.name+"-bad")
		if !goodOK || !badOK || good == 0 || bad != 0 {
			t.Fatalf("%s packet oracle good=%d/%v bad=%d/%v", family.name, good, goodOK, bad, badOK)
		}
	}
}

func directICEOracleRules(source4, destination4, source6, destination6 netip.Addr, use4, use6 bool) string {
	var rules strings.Builder
	rules.WriteString("table inet hydra_m5c1 {\n chain output {\n  type filter hook output priority filter; policy accept;\n")
	if use4 {
		fmt.Fprintf(&rules, "  ip saddr %s ip daddr %s udp sport != %d counter comment \"m5c1-v4-bad\"\n", source4, destination4, clientMuxPort)
		fmt.Fprintf(&rules, "  ip saddr %s ip daddr %s udp sport %d counter comment \"m5c1-v4-mux\"\n", source4, destination4, clientMuxPort)
	}
	if use6 {
		fmt.Fprintf(&rules, "  ip6 saddr %s ip6 daddr %s udp sport != %d counter comment \"m5c1-v6-bad\"\n", source6, destination6, clientMuxPort)
		fmt.Fprintf(&rules, "  ip6 saddr %s ip6 daddr %s udp sport %d counter comment \"m5c1-v6-mux\"\n", source6, destination6, clientMuxPort)
	}
	rules.WriteString(" }\n}\n")
	return rules.String()
}

func directICEDropRules(destination4, destination6 netip.Addr) string {
	return fmt.Sprintf(`table inet hydra_m5c1 {
 chain output {
  type filter hook output priority filter; policy accept;
  ip daddr %s meta l4proto udp counter drop comment "m5c1-drop-v4"
  ip6 daddr %s meta l4proto udp counter drop comment "m5c1-drop-v6"
 }
}
`, destination4, destination6)
}
