// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestM7_EIMAPDFNATInternetExit(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m7-eim-apdf-internet")
	iceTopology := lab.buildICENATTopology(true, false)
	scenario := iceNATScenario{name: "m7-eim-apdf", aNAT: true, aMapping: mappingEIM, aFiltering: filteringAPDF, wantConnected: true}
	installICENATScenarioRules(lab, iceTopology, scenario)
	stun := lab.startResponder("m7-stun", iceTopology.ServerNamespace, iceTopology.ServerA1, iceTopology.ServerA2)

	internetNamespace := lab.namespace("internet")
	exitWANIf := lab.interfaceName('w')
	internetIf := lab.interfaceName('i')
	exitWAN := netip.MustParseAddr("203.0.114.1")
	internet := netip.MustParseAddr("203.0.114.2")
	lab.createVeth(iceTopology.BNamespace, exitWANIf, internetNamespace, internetIf)
	for _, item := range []struct {
		namespace string
		iface     string
		address   netip.Addr
	}{{iceTopology.BNamespace, exitWANIf, exitWAN}, {internetNamespace, internetIf, internet}} {
		lab.inNamespace(item.namespace, "ip", "link", "set", "lo", "up")
		lab.inNamespace(item.namespace, "ip", "link", "set", item.iface, "up")
		lab.inNamespace(item.namespace, "ip", "addr", "add", item.address.String()+"/24", "dev", item.iface)
	}
	m7View := m7Topology{
		ClientNamespace: iceTopology.ANamespace, ExitNamespace: iceTopology.BNamespace, InternetNamespace: internetNamespace,
		ClientOuterIf: iceTopology.ADataIf, ExitOuterIf: lab.interfaceName('b'), ExitWANIf: exitWANIf, InternetIf: internetIf,
		ClientOuter4: iceTopology.AData, ExitOuter4: iceTopology.BData, ExitWAN4: exitWAN, Internet4: internet,
	}
	lab.installNFT(iceTopology.ANamespace, "m7-client-oracle", m7ClientOracleRules(m7View))
	internetServer, serverRelease, observations := startM7InternetServer(t, lab, m7View)

	stunServer := endpointValue(iceTopology.ServerA1, stunPrimaryPort)
	exit, exitReady := startM7NATPeer(t, lab, iceTopology, m7View, "responder", stunServer, "")
	remote := selectSignalAddress(t, exitReady.ListenAddresses, "ipv4")
	client, clientReady := startM7NATPeer(t, lab, iceTopology, m7View, "initiator", stunServer, remote)
	expectedMapped := netip.AddrPortFrom(iceTopology.AExternal, m5c2AStunPort)
	assertSTUNReadiness(t, clientReady, expectedMapped, iceTopology.AData)
	waitForICEReady(t, client, exit)
	if err := touch(client.connectStartPath); err != nil {
		t.Fatal(err)
	}
	if err := touch(exit.connectStartPath); err != nil {
		t.Fatal(err)
	}
	waitForAWGReady(t, client, exit)
	if err := touch(exit.awgStartPath); err != nil {
		t.Fatal(err)
	}
	if err := touch(client.awgStartPath); err != nil {
		t.Fatal(err)
	}
	clientResult := waitNATConnectResult(t, client)
	exitResult := waitNATConnectResult(t, exit)
	assertNATConnected(t, clientResult, exitResult, iceTopology, expectedMapped, netip.AddrPortFrom(iceTopology.BData, clientMuxPort), scenario)
	assertM7Ownership(t, clientReady, clientResult, exitReady, exitResult)
	assertM7Probe(t, runM7Probe(t, lab, iceTopology.ANamespace, "http", endpointValue(internet, 18080)), exitWAN, netip.MustParseAddr("10.77.0.2"))
	assertM7Probe(t, runM7Probe(t, lab, iceTopology.ANamespace, "udp", endpointValue(internet, 18081)), exitWAN, netip.MustParseAddr("10.77.0.2"))
	for _, kind := range []string{"dns-udp", "dns-tcp"} {
		result := runM7Probe(t, lab, iceTopology.ANamespace, kind, endpointValue(internet, 1053))
		if !result.Success || result.DNSAnswer != "198.51.100.77" {
			t.Fatalf("%s over NAT result=%+v", kind, result)
		}
	}
	assertM7ProbeFails(t, lab, iceTopology.ANamespace, "http", endpointValue(exitWAN, 18080))
	assertM7ProbeFails(t, lab, iceTopology.ANamespace, "http", endpointValue(netip.MustParseAddr("10.77.0.3"), 18080))
	assertM7KernelEvidence(t, lab, m7View, struct {
		name                string
		outer4, outer6      bool
		protect4, protect6  bool
		reject4, reject6    bool
		probe4, probe6, dns bool
	}{name: "nat", outer4: true, protect4: true, probe4: true, dns: true}, observations)
	assertSTUNOracle(t, stun.observationPath, expectedMapped, netip.AddrPortFrom(iceTopology.BData, clientMuxPort))
	assertNATPacketOracle(t, lab, iceTopology, scenario)

	for _, path := range []string{client.releasePath, exit.releasePath, serverRelease} {
		if err := touch(path); err != nil {
			t.Fatal(err)
		}
	}
	waitNATChildren(t, client, exit, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := internetServer.wait(ctx); err != nil {
		t.Fatal(err)
	}
	lab.inNamespace(iceTopology.ANamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(iceTopology.BNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
}

func startM7NATPeer(t *testing.T, lab *lab, iceTopology iceNATTopology, m7View m7Topology, side string, stunServer netip.AddrPort, remote string) (natConnectChild, iceSignalReport) {
	t.Helper()
	label := "m7-nat-" + side
	process := natConnectChild{
		readyPath:        filepath.Join(lab.artifactDir, label+"-ready.json"),
		connectReadyPath: filepath.Join(lab.artifactDir, label+"-connect-ready.json"),
		connectStartPath: filepath.Join(lab.artifactDir, label+"-connect-start"),
		awgReadyPath:     filepath.Join(lab.artifactDir, label+"-awg-ready.json"),
		awgStartPath:     filepath.Join(lab.artifactDir, label+"-awg-start"),
		resultPath:       filepath.Join(lab.artifactDir, label+"-result.json"),
		releasePath:      filepath.Join(lab.artifactDir, label+"-release"),
	}
	data, control := iceTopology.BData, iceTopology.BControl
	if side == "initiator" {
		data, control = iceTopology.AData, iceTopology.AControl
	}
	args := []string{
		"--mode", "connect-" + side, "--port", strconv.Itoa(clientMuxPort), "--bind4", data.String(), "--tcp4", control.String(),
		"--stun-server4", stunServer.String(), "--ready-file", process.readyPath,
		"--connect-ready-file", process.connectReadyPath, "--connect-start-file", process.connectStartPath,
		"--result-file", process.resultPath, "--release-file", process.releasePath, "--timeout", "30s",
		"--awg", "--awg-real-tun", "--awg-tun-name", "hydra0", "--awg-ready-file", process.awgReadyPath, "--awg-start-file", process.awgStartPath,
	}
	if side == "initiator" {
		args = append(args, "--remote", remote,
			"--awg-inner-local", "10.77.0.2", "--awg-inner-remote", "10.77.0.1", "--awg-local-key-seed", "71", "--awg-remote-key-seed", "91",
			"--awg-tun-addresses", "10.77.0.2/32", "--awg-allowed-ips", "0.0.0.0/0",
			"--m7-role", "client", "--m7-wan-name", iceTopology.ADataIf, "--m7-protect4",
			"--m7-wan4-source", iceTopology.AData.String(), "--m7-wan4-gateway", "10.51.1.1", "--m7-wan4-connected", "10.51.1.0/24",
			"--m7-bypass", "10.51.1.0/24,192.0.2.0/24")
	} else {
		args = append(args,
			"--awg-inner-local", "10.77.0.1", "--awg-inner-remote", "10.77.0.2", "--awg-local-key-seed", "91", "--awg-remote-key-seed", "71",
			"--awg-tun-addresses", "10.77.0.1/24", "--awg-allowed-ips", "10.77.0.2/32",
			"--m7-role", "exit", "--m7-wan-name", m7View.ExitWANIf, "--m7-client-pool4", "10.77.0.0/24",
			"--m7-management", m7View.ExitWAN4.String()+"/32")
	}
	namespace := iceTopology.BNamespace
	if side == "initiator" {
		namespace = iceTopology.ANamespace
	}
	process.child = lab.startChild(label, namespace, os.Getenv("HYDRA_M5B_SIGNALHELPER"), args...)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, process.child, process.readyPath); err != nil {
		t.Fatal(err)
	}
	var ready iceSignalReport
	if err := readJSON(process.readyPath, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.Error != "" || ready.MuxPort != clientMuxPort {
		t.Fatalf("invalid M7 NAT readiness: %+v", ready)
	}
	return process, ready
}
