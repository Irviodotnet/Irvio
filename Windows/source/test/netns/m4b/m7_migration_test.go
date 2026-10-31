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
	"testing"
	"time"
)

func TestM7_NetworkGenerationMigrationPreservesLogicalTunnel(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m7-network-migration")
	topology := buildM7Topology(lab)
	clientControlIf := lab.interfaceName('k')
	exitControlIf := lab.interfaceName('l')
	clientControl := netip.MustParseAddr("192.0.2.77")
	exitControl := netip.MustParseAddr("192.0.2.78")
	lab.createVeth(topology.ClientNamespace, clientControlIf, topology.ExitNamespace, exitControlIf)
	for _, item := range []struct {
		namespace string
		iface     string
		address   netip.Addr
	}{{topology.ClientNamespace, clientControlIf, clientControl}, {topology.ExitNamespace, exitControlIf, exitControl}} {
		lab.inNamespace(item.namespace, "ip", "link", "set", item.iface, "up")
		lab.inNamespace(item.namespace, "ip", "addr", "add", item.address.String()+"/24", "dev", item.iface)
	}
	lab.inNamespace(topology.ClientNamespace, "ip", "route", "add", "default", "via", topology.ExitOuter4.String(), "dev", topology.ClientOuterIf)
	lab.installNFT(topology.ClientNamespace, "m7-client-oracle", m7ClientOracleRules(topology))
	server, serverRelease, _ := startM7InternetServer(t, lab, topology)

	exit, exitReady := startM7MigrationPeer(t, lab, topology, "responder", topology.ExitOuter4, exitControl, "")
	remote := selectSignalAddress(t, exitReady.ListenAddresses, "ipv4")
	client, _ := startM7MigrationPeer(t, lab, topology, "initiator", topology.ClientOuter4, clientControl, remote)
	waitM7MigrationAWGReady(t, client)
	waitM7MigrationAWGReady(t, exit)
	if err := touch(exit.awgStartPath); err != nil {
		t.Fatal(err)
	}
	if err := touch(client.awgStartPath); err != nil {
		t.Fatal(err)
	}
	firstClient := waitMigrationReport(t, client.child, client.resultPath+".generation-1")
	firstExit := waitMigrationReport(t, exit.child, exit.resultPath+".generation-1")
	assertConnectedReports(t, firstClient, firstExit, true, false)
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	newAddress := netip.MustParseAddr("198.18.77.3")
	runIPBatch(t, lab, topology.ClientNamespace, "m7-network-migration", fmt.Sprintf(
		"addr del %s/24 dev %s\naddr add %s/24 dev %s\nroute replace default via %s dev %s src %s\n",
		topology.ClientOuter4, topology.ClientOuterIf, newAddress, topology.ClientOuterIf,
		topology.ExitOuter4, topology.ClientOuterIf, newAddress))
	secondClient := waitMigrationReport(t, client.child, client.resultPath)
	secondExit := waitMigrationReport(t, exit.child, exit.resultPath)
	assertConnectedReports(t, secondClient, secondExit, true, false)
	if secondClient.NetworkGeneration != 2 || secondClient.LocalIPv4 != netip.AddrPortFrom(newAddress, clientMuxPort).String() ||
		secondClient.AWGTUNIfIndex != firstClient.AWGTUNIfIndex || secondClient.UDP4SocketInodeAfter == firstClient.UDP4SocketInodeAfter ||
		secondClient.PreviousSessionID != firstClient.SessionID || !secondClient.CredentialsRotated || !secondClient.PionAgentReplaced ||
		secondExit.PreviousSessionID != firstExit.SessionID || secondExit.AWGTUNIfIndex != firstExit.AWGTUNIfIndex {
		t.Fatalf("network migration did not preserve logical TUN while rotating transport: C1=%+v C2=%+v E1=%+v E2=%+v", firstClient, secondClient, firstExit, secondExit)
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

func startM7MigrationPeer(t *testing.T, lab *lab, topology m7Topology, side string, dataAddress, controlAddress netip.Addr, remote string, extraArgs ...string) (migrationChild, iceSignalReport) {
	t.Helper()
	label := "m7-migration-" + side
	process := migrationChild{
		readyPath:    filepath.Join(lab.artifactDir, label+"-ready.json"),
		resultPath:   filepath.Join(lab.artifactDir, label+"-result.json"),
		releasePath:  filepath.Join(lab.artifactDir, label+"-release"),
		awgReadyPath: filepath.Join(lab.artifactDir, label+"-awg-ready.json"),
		awgStartPath: filepath.Join(lab.artifactDir, label+"-awg-start"),
	}
	args := []string{
		"--mode", "migration-" + side, "--bind4", dataAddress.String(), "--tcp4", controlAddress.String(),
		"--port", strconv.Itoa(clientMuxPort), "--ready-file", process.readyPath, "--result-file", process.resultPath,
		"--release-file", process.releasePath, "--timeout", "45s",
		"--ice-disconnected-timeout", "500ms", "--ice-failed-timeout", "1s", "--ice-keepalive-interval", "100ms", "--ice-check-interval", "20ms",
		"--awg", "--awg-real-tun", "--awg-tun-name", "hydra0", "--awg-ready-file", process.awgReadyPath, "--awg-start-file", process.awgStartPath,
	}
	if side == "initiator" {
		args = append(args, "--remote", remote,
			"--awg-inner-local", "10.77.0.2", "--awg-inner-remote", "10.77.0.1", "--awg-local-key-seed", "71", "--awg-remote-key-seed", "91",
			"--awg-tun-addresses", "10.77.0.2/32", "--awg-allowed-ips", "0.0.0.0/0",
			"--m7-role", "client", "--m7-wan-name", topology.ClientOuterIf, "--m7-protect4",
			"--m7-wan4-source", dataAddress.String(), "--m7-wan4-gateway", topology.ExitOuter4.String(), "--m7-wan4-connected", "198.18.77.0/24",
			"--m7-bypass", "198.18.77.0/24,192.0.2.0/24")
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
	ready := waitMigrationReport(t, process.child, process.readyPath)
	if ready.Error != "" || ready.MuxPort != clientMuxPort {
		t.Fatalf("invalid M7 migration readiness: %+v", ready)
	}
	return process, ready
}

func waitM7MigrationAWGReady(t *testing.T, process migrationChild) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, process.child, process.awgReadyPath); err != nil {
		t.Fatal(err)
	}
}
