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

func TestM7_SamePeerReconnectPreservesLogicalTunnel(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m7-reconnect")
	topology := buildM7Topology(lab)
	lab.installNFT(topology.ClientNamespace, "m7-client-oracle", m7ClientOracleRules(topology))
	lab.installNFT(topology.ClientNamespace, "m7-reconnect-client", reconnectBlockRules())
	lab.installNFT(topology.ExitNamespace, "m7-reconnect-exit", reconnectBlockRules())
	server, serverRelease, _ := startM7InternetServer(t, lab, topology)

	exit, exitReady := startM7ReconnectPeer(t, lab, topology, "responder", "")
	remote := selectSignalAddress(t, exitReady.ListenAddresses, "ipv4")
	client, _ := startM7ReconnectPeer(t, lab, topology, "initiator", remote)
	waitReconnectReady(t, client, 1)
	waitReconnectReady(t, exit, 1)
	touchReconnectStart(t, client, 1)
	touchReconnectStart(t, exit, 1)
	waitM7ReconnectAWGReady(t, client)
	waitM7ReconnectAWGReady(t, exit)
	if err := touch(exit.awgStartPath); err != nil {
		t.Fatal(err)
	}
	if err := touch(client.awgStartPath); err != nil {
		t.Fatal(err)
	}
	firstClient := waitReconnectReport(t, client.child, client.resultPath+".generation-1")
	firstExit := waitReconnectReport(t, exit.child, exit.resultPath+".generation-1")
	assertConnectedReports(t, firstClient, firstExit, true, false)
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	setReconnectBlocked(t, lab, topology.ClientNamespace, topology.ExitOuter4, true)
	setReconnectBlocked(t, lab, topology.ExitNamespace, topology.ClientOuter4, true)
	waitReconnectReport(t, client.child, client.failedPath)
	waitReconnectReport(t, exit.child, exit.failedPath)
	waitReconnectReady(t, client, 2)
	waitReconnectReady(t, exit, 2)
	setReconnectBlocked(t, lab, topology.ClientNamespace, topology.ExitOuter4, false)
	setReconnectBlocked(t, lab, topology.ExitNamespace, topology.ClientOuter4, false)
	touchReconnectStart(t, client, 2)
	touchReconnectStart(t, exit, 2)
	secondClient := waitReconnectReport(t, client.child, client.resultPath)
	secondExit := waitReconnectReport(t, exit.child, exit.resultPath)
	assertConnectedReports(t, secondClient, secondExit, true, false)
	assertReconnectRotation(t, firstClient, secondClient)
	assertReconnectRotation(t, firstExit, secondExit)
	if secondClient.AWGTUNIfIndex != firstClient.AWGTUNIfIndex || secondExit.AWGTUNIfIndex != firstExit.AWGTUNIfIndex ||
		secondClient.UDP4SocketInodeAfter != firstClient.UDP4SocketInodeAfter || secondExit.UDP4SocketInodeAfter != firstExit.UDP4SocketInodeAfter {
		t.Fatalf("same-peer reconnect replaced logical tunnel ownership: C1=%+v C2=%+v E1=%+v E2=%+v", firstClient, secondClient, firstExit, secondExit)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

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
}

func startM7ReconnectPeer(t *testing.T, lab *lab, topology m7Topology, side, remote string, extraArgs ...string) (reconnectChild, iceSignalReport) {
	t.Helper()
	label := "m7-reconnect-" + side
	ready := filepath.Join(lab.artifactDir, label+"-ready.json")
	process := reconnectChild{
		resultPath:         filepath.Join(lab.artifactDir, label+"-result.json"),
		connectReadyPrefix: filepath.Join(lab.artifactDir, label+"-connect-ready"),
		connectStartPrefix: filepath.Join(lab.artifactDir, label+"-connect-start"),
		disconnectedPath:   filepath.Join(lab.artifactDir, label+"-disconnected.json"),
		failedPath:         filepath.Join(lab.artifactDir, label+"-failed.json"),
		releasePath:        filepath.Join(lab.artifactDir, label+"-release"),
		awgReadyPath:       filepath.Join(lab.artifactDir, label+"-awg-ready.json"),
		awgStartPath:       filepath.Join(lab.artifactDir, label+"-awg-start"),
	}
	bind := topology.ExitOuter4
	if side == "initiator" {
		bind = topology.ClientOuter4
	}
	args := []string{
		"--mode", "reconnect-" + side, "--bind4", bind.String(), "--tcp4", bind.String(),
		"--port", strconv.Itoa(clientMuxPort), "--ready-file", ready, "--result-file", process.resultPath,
		"--connect-ready-file", process.connectReadyPrefix, "--connect-start-file", process.connectStartPrefix,
		"--disconnected-file", process.disconnectedPath, "--failed-file", process.failedPath,
		"--release-file", process.releasePath, "--timeout", "35s",
		"--ice-disconnected-timeout", "250ms", "--ice-failed-timeout", "400ms",
		"--ice-keepalive-interval", "50ms", "--ice-check-interval", "20ms",
		"--awg", "--awg-real-tun", "--awg-tun-name", "hydra0",
		"--awg-ready-file", process.awgReadyPath, "--awg-start-file", process.awgStartPath,
	}
	if side == "initiator" {
		args = append(args, "--remote", remote,
			"--awg-inner-local", "10.77.0.2", "--awg-inner-remote", "10.77.0.1", "--awg-local-key-seed", "71", "--awg-remote-key-seed", "91",
			"--awg-tun-addresses", "10.77.0.2/32", "--awg-allowed-ips", "0.0.0.0/0",
			"--m7-role", "client", "--m7-wan-name", topology.ClientOuterIf, "--m7-protect4",
			"--m7-wan4-source", topology.ClientOuter4.String(), "--m7-wan4-connected", "198.18.77.0/24", "--m7-bypass", "198.18.77.0/24")
	} else {
		args = append(args,
			"--awg-inner-local", "10.77.0.1", "--awg-inner-remote", "10.77.0.2", "--awg-local-key-seed", "91", "--awg-remote-key-seed", "71",
			"--awg-tun-addresses", "10.77.0.1/24", "--awg-allowed-ips", "10.77.0.2/32",
			"--m7-role", "exit", "--m7-wan-name", topology.ExitWANIf, "--m7-client-pool4", "10.77.0.0/24",
			"--m7-management", topology.ExitWAN4.String()+"/32")
	}
	args = append(args, extraArgs...)
	namespace := topology.ExitNamespace
	if side == "initiator" {
		namespace = topology.ClientNamespace
	}
	process.child = lab.startChild(label, namespace, os.Getenv("HYDRA_M5B_SIGNALHELPER"), args...)
	readyReport := waitReconnectReport(t, process.child, ready)
	if readyReport.Error != "" || readyReport.MuxPort != clientMuxPort {
		t.Fatalf("invalid M7 reconnect readiness: %+v", readyReport)
	}
	return process, readyReport
}

func waitM7ReconnectAWGReady(t *testing.T, process reconnectChild) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, process.child, process.awgReadyPath); err != nil {
		t.Fatal(err)
	}
}
