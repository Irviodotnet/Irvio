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

const m5c3NewStunPort = 53011

type migrationChild struct {
	child        *childProcess
	readyPath    string
	resultPath   string
	releasePath  string
	awgReadyPath string
	awgStartPath string
}

func TestM5C3_RealNetlinkNATTransportMigration(t *testing.T) {
	runM5C3RealNetlinkNATTransportMigration(t, false)
}

func TestM5C3_RealNetlinkNATHardBreakMigration(t *testing.T) {
	runM5C3RealNetlinkNATTransportMigration(t, true)
}

func TestM5C3_RealNetlinkIPv4AddressMigration(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	lab := newLab(t, "m5c3-ipv4-migration")
	topology := lab.buildNAT44()
	server := lab.startResponder("m5c3-ipv4-migration-stun", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
	lab.inNamespace(topology.ServerNamespace, "ip", "route", "add", "10.241.10.0/24", "via", topology.RouterOutside.String())

	clientControlIf := lab.interfaceName('x')
	serverControlIf := lab.interfaceName('y')
	clientControl := netip.MustParseAddr("198.18.74.1")
	serverControl := netip.MustParseAddr("198.18.74.2")
	lab.createVeth(topology.ClientNamespace, clientControlIf, topology.ServerNamespace, serverControlIf)
	for _, item := range []struct {
		namespace string
		name      string
		address   netip.Addr
	}{
		{topology.ClientNamespace, clientControlIf, clientControl},
		{topology.ServerNamespace, serverControlIf, serverControl},
	} {
		lab.inNamespace(item.namespace, "ip", "link", "set", item.name, "up")
		lab.inNamespace(item.namespace, "ip", "addr", "add", item.address.String()+"/24", "dev", item.name)
	}

	stunServer := endpointValue(topology.ServerA1, stunPrimaryPort)
	responder, responderReady := startMigrationHelper(t, lab, "responder", topology.ServerNamespace,
		topology.ServerA1, serverControl, stunServer, "")
	remote := selectSignalAddress(t, responderReady.ListenAddresses, "ipv4")
	initiator, firstInitiator := startMigrationHelper(t, lab, "initiator", topology.ClientNamespace,
		topology.ClientIPv4, clientControl, stunServer, remote)
	firstResponder := waitMigrationReport(t, responder.child, responder.resultPath+".generation-1")
	firstMapped := netip.AddrPortFrom(topology.ClientIPv4, clientMuxPort)
	assertMigrationGeneration(t, firstInitiator, firstResponder, 1, topology.ClientIPv4, firstMapped)

	newAddress := netip.MustParseAddr("10.241.10.4")
	runIPBatch(t, lab, topology.ClientNamespace, "m5c3-ipv4-address-migration", fmt.Sprintf(
		"addr del %s/24 dev %s\naddr add %s/24 dev %s\nroute replace default via %s dev %s\n",
		topology.ClientIPv4, lab.interfaceName('c'), newAddress, lab.interfaceName('c'),
		topology.RouterInside, lab.interfaceName('c')))
	secondInitiator := waitMigrationReport(t, initiator.child, initiator.resultPath)
	secondResponder := waitMigrationReport(t, responder.child, responder.resultPath)
	secondMapped := netip.AddrPortFrom(newAddress, clientMuxPort)
	assertMigrationGeneration(t, secondInitiator, secondResponder, 2, newAddress, secondMapped)
	if secondInitiator.PreviousSessionID != firstInitiator.SessionID ||
		secondInitiator.PreviousLocalUfrag != firstInitiator.LocalUfrag ||
		!secondInitiator.CredentialsRotated || !secondInitiator.PionAgentReplaced ||
		secondResponder.PreviousSessionID != firstResponder.SessionID ||
		!secondResponder.CredentialsRotated || !secondResponder.PionAgentReplaced {
		t.Fatalf("IPv4 address migration did not rotate both ICE generations: A1=%+v A2=%+v B1=%+v B2=%+v",
			firstInitiator, secondInitiator, firstResponder, secondResponder)
	}
	if firstInitiator.UDP4SocketInodeAfter == "" || secondInitiator.UDP4SocketInodeAfter == "" ||
		firstInitiator.UDP4SocketInodeAfter == secondInitiator.UDP4SocketInodeAfter ||
		firstInitiator.UDP4SocketInodeReady != firstInitiator.UDP4SocketInodeAfter ||
		secondInitiator.UDP4SocketInodeReady != secondInitiator.UDP4SocketInodeAfter {
		t.Fatal("IPv4 network generation did not preserve per-generation socket ownership")
	}
	for _, candidate := range secondInitiator.LocalCandidates {
		if candidate.Address == topology.ClientIPv4.String() {
			t.Fatalf("new IPv4 description reused the removed address: %+v", candidate)
		}
	}
	if udp4, udp6 := namespaceUDPSockets(t, lab, topology.ClientNamespace); udp4 != 1 || udp6 != 0 {
		t.Fatalf("stale IPv4 transport sockets remain after activation: udp4=%d udp6=%d", udp4, udp6)
	}
	assertSTUNOracle(t, server.observationPath, firstMapped, secondMapped,
		netip.AddrPortFrom(topology.ServerA1, clientMuxPort))

	if err := touch(initiator.releasePath); err != nil {
		t.Fatal(err)
	}
	if err := touch(responder.releasePath); err != nil {
		t.Fatal(err)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := initiator.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	if err := responder.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	lab.captureNamespace(topology.ClientNamespace, "m5c3-ipv4-migration-client", true)
	lab.captureNamespace(topology.RouterNamespace, "m5c3-ipv4-migration-router", true)
	lab.captureNamespace(topology.ServerNamespace, "m5c3-ipv4-migration-server", true)
}

func TestM5C3_RealNetlinkIPv6AddressMigration(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	lab := newLab(t, "m5c3-ipv6-migration")
	topology := lab.buildRoutedIPv6()

	clientControlIf := lab.interfaceName('x')
	serverControlIf := lab.interfaceName('y')
	clientControl := netip.MustParseAddr("198.18.73.1")
	serverControl := netip.MustParseAddr("198.18.73.2")
	lab.createVeth(topology.ClientNamespace, clientControlIf, topology.ServerNamespace, serverControlIf)
	lab.inNamespace(topology.ClientNamespace, "ip", "addr", "add", clientControl.String()+"/24", "dev", clientControlIf)
	lab.inNamespace(topology.ServerNamespace, "ip", "addr", "add", serverControl.String()+"/24", "dev", serverControlIf)
	lab.inNamespace(topology.ClientNamespace, "ip", "link", "set", clientControlIf, "up")
	lab.inNamespace(topology.ServerNamespace, "ip", "link", "set", serverControlIf, "up")

	responder, responderReady := startIPv6MigrationHelper(t, lab, "responder", topology.ServerNamespace,
		topology.ServerA1, serverControl, "")
	remote := selectSignalAddress(t, responderReady.ListenAddresses, "ipv4")
	initiator, firstInitiator := startIPv6MigrationHelper(t, lab, "initiator", topology.ClientNamespace,
		topology.ClientIPv6, clientControl, remote)
	firstResponder := waitMigrationReport(t, responder.child, responder.resultPath+".generation-1")
	assertIPv6MigrationGeneration(t, firstInitiator, firstResponder, 1, topology.ClientIPv6)

	newAddress := netip.MustParseAddr("2001:db8:41::4")
	runIP6Batch(t, lab, topology.ClientNamespace, "m5c3-ipv6-address-migration", fmt.Sprintf(
		"addr add %s/64 dev %s nodad\naddr del %s/64 dev %s\n",
		newAddress, topology.ClientInterface, topology.ClientIPv6, topology.ClientInterface))
	clientMAC := lab.interfaceMAC(topology.ClientNamespace, topology.ClientInterface)
	lab.inNamespace(topology.RouterNamespace, "ip", "-6", "neigh", "replace", newAddress.String(),
		"lladdr", clientMAC, "nud", "permanent", "dev", topology.InsideInterface)

	secondInitiator := waitMigrationReport(t, initiator.child, initiator.resultPath)
	secondResponder := waitMigrationReport(t, responder.child, responder.resultPath)
	assertIPv6MigrationGeneration(t, secondInitiator, secondResponder, 2, newAddress)
	if secondInitiator.PreviousSessionID != firstInitiator.SessionID ||
		secondInitiator.PreviousLocalUfrag != firstInitiator.LocalUfrag ||
		!secondInitiator.CredentialsRotated || !secondInitiator.PionAgentReplaced ||
		secondResponder.PreviousSessionID != firstResponder.SessionID ||
		!secondResponder.CredentialsRotated || !secondResponder.PionAgentReplaced {
		t.Fatalf("IPv6 address migration did not rotate both ICE generations: A1=%+v A2=%+v B1=%+v B2=%+v",
			firstInitiator, secondInitiator, firstResponder, secondResponder)
	}
	if firstInitiator.UDP6SocketInodeAfter == "" || secondInitiator.UDP6SocketInodeAfter == "" ||
		firstInitiator.UDP6SocketInodeAfter == secondInitiator.UDP6SocketInodeAfter ||
		firstInitiator.UDP6SocketInodeReady != firstInitiator.UDP6SocketInodeAfter ||
		secondInitiator.UDP6SocketInodeReady != secondInitiator.UDP6SocketInodeAfter {
		t.Fatal("IPv6 network generation did not preserve per-generation socket ownership")
	}
	for _, candidate := range secondInitiator.LocalCandidates {
		if candidate.Address == topology.ClientIPv6.String() {
			t.Fatalf("new IPv6 description reused the removed address: %+v", candidate)
		}
	}
	if udp4, udp6 := namespaceUDPSockets(t, lab, topology.ClientNamespace); udp4 != 0 || udp6 != 1 {
		t.Fatalf("stale IPv6 transport sockets remain after activation: udp4=%d udp6=%d", udp4, udp6)
	}

	if err := touch(initiator.releasePath); err != nil {
		t.Fatal(err)
	}
	if err := touch(responder.releasePath); err != nil {
		t.Fatal(err)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := initiator.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	if err := responder.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	lab.captureNamespace(topology.ClientNamespace, "m5c3-ipv6-migration-client", true)
	lab.captureNamespace(topology.RouterNamespace, "m5c3-ipv6-migration-router", true)
	lab.captureNamespace(topology.ServerNamespace, "m5c3-ipv6-migration-server", true)
}

func runM5C3RealNetlinkNATTransportMigration(t *testing.T, hardBreak bool) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	name := "m5c3-nat-migration"
	if hardBreak {
		name = "m5c3-nat-hard-migration"
	}
	lab := newLab(t, name)
	topology := lab.buildICENATTopology(true, false)
	server := lab.startResponder("m5c3-migration-stun", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
	oldScenario := iceNATScenario{
		name: "m5c3-old", aNAT: true, aMapping: mappingEIM,
		aFiltering: filteringAPDF, wantConnected: true,
	}
	installICENATScenarioRules(lab, topology, oldScenario)

	newRouter := lab.namespace("router2")
	newClientIf := lab.interfaceName('n')
	newInsideIf := lab.interfaceName('i')
	newOutsideIf := lab.interfaceName('o')
	newSwitchIf := lab.interfaceName('z')
	newData := netip.MustParseAddr("10.52.1.2")
	newGateway := netip.MustParseAddr("10.52.1.1")
	newExternal := netip.MustParseAddr("198.18.90.11")
	lab.inNamespace(newRouter, "ip", "link", "set", "lo", "up")
	lab.attachICEPrivate(topology.ANamespace, newClientIf, newData, newRouter, newInsideIf, newGateway)
	lab.attachICEExternal(newRouter, newOutsideIf, []netip.Addr{newExternal}, topology.SwitchNamespace, newSwitchIf)
	lab.enableICERouter(newRouter)
	lab.installNFT(newRouter, "m5c3-new-nat", iceNATRules(
		newData, newExternal, topology.ServerA1, topology.BData,
		newInsideIf, newOutsideIf, m5c3NewStunPort, m5c3NewStunPort,
		clientMuxPort, clientMuxPort, mappingEIM, filteringAPDF,
	))

	stunServer := endpointValue(topology.ServerA1, stunPrimaryPort)
	responder, responderReady := startMigrationHelper(t, lab, "responder", topology.BNamespace,
		topology.BData, topology.BControl, stunServer, "")
	remote := selectSignalAddress(t, responderReady.ListenAddresses, "ipv4")
	initiator, firstInitiator := startMigrationHelper(t, lab, "initiator", topology.ANamespace,
		topology.AData, topology.AControl, stunServer, remote)
	firstResponder := waitMigrationReport(t, responder.child, responder.resultPath+".generation-1")
	oldMapped := netip.AddrPortFrom(topology.AExternal, m5c2AStunPort)
	assertMigrationGeneration(t, firstInitiator, firstResponder, 1, topology.AData, oldMapped)

	if hardBreak {
		runIPBatch(t, lab, topology.ANamespace, "m5c3-hard-break", fmt.Sprintf(
			"link set dev %s down\nroute replace default via %s dev %s\n",
			topology.ADataIf, newGateway, newClientIf))
	} else {
		lab.inNamespace(topology.ANamespace, "ip", "route", "replace", "default", "via", newGateway.String(), "dev", newClientIf)
	}
	secondInitiator := waitMigrationReport(t, initiator.child, initiator.resultPath)
	secondResponder := waitMigrationReport(t, responder.child, responder.resultPath)
	newMapped := netip.AddrPortFrom(newExternal, m5c3NewStunPort)
	assertMigrationGeneration(t, secondInitiator, secondResponder, 2, newData, newMapped)
	if secondInitiator.PreviousSessionID != firstInitiator.SessionID ||
		secondInitiator.PreviousLocalUfrag != firstInitiator.LocalUfrag ||
		!secondInitiator.CredentialsRotated || !secondInitiator.PionAgentReplaced ||
		secondResponder.PreviousSessionID != firstResponder.SessionID ||
		!secondResponder.CredentialsRotated || !secondResponder.PionAgentReplaced {
		t.Fatalf("network migration did not rotate both ICE generations: A1=%+v A2=%+v B1=%+v B2=%+v",
			firstInitiator, secondInitiator, firstResponder, secondResponder)
	}
	if firstInitiator.SessionID == secondInitiator.SessionID || firstInitiator.LocalUfrag == secondInitiator.LocalUfrag ||
		firstInitiator.ObservedSrflxIPv4 == secondInitiator.ObservedSrflxIPv4 {
		t.Fatal("network migration reused old session or STUN identity")
	}
	for _, candidate := range secondInitiator.LocalCandidates {
		if candidate.Address == topology.AData.String() || candidate.Address == topology.AExternal.String() {
			t.Fatalf("new description reused an old-generation candidate: %+v", candidate)
		}
	}
	if udp4, udp6 := namespaceUDPSockets(t, lab, topology.ANamespace); udp4 != 1 || udp6 != 0 {
		t.Fatalf("stale transport sockets remain after activation: udp4=%d udp6=%d", udp4, udp6)
	}
	assertSTUNOracle(t, server.observationPath,
		oldMapped, newMapped, netip.AddrPortFrom(topology.BData, clientMuxPort))
	for _, item := range []struct {
		namespace string
		comment   string
	}{
		{topology.ARouter, "m5c2-stun-snat"},
		{newRouter, "m5c2-stun-snat"},
	} {
		rules := lab.inNamespace(item.namespace, "nft", "-j", "list", "ruleset")
		if packets, ok := counterForComment(rules, item.comment); !ok || packets == 0 {
			t.Fatalf("router %s lacks generation-specific STUN evidence: %d/%v", item.namespace, packets, ok)
		}
	}

	if err := touch(initiator.releasePath); err != nil {
		t.Fatal(err)
	}
	if err := touch(responder.releasePath); err != nil {
		t.Fatal(err)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := initiator.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	if err := responder.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	lab.captureNamespace(topology.ANamespace, "m5c3-migration-a", true)
	lab.captureNamespace(topology.BNamespace, "m5c3-migration-b", true)
	lab.captureNamespace(topology.ARouter, "m5c3-migration-old-router", true)
	lab.captureNamespace(newRouter, "m5c3-migration-new-router", true)
}

func startMigrationHelper(t *testing.T, lab *lab, side, namespace string, dataAddress, controlAddress netip.Addr, stunServer netip.AddrPort, remote string) (migrationChild, iceSignalReport) {
	t.Helper()
	label := "m5c3-migration-" + side
	process := migrationChild{
		readyPath:   filepath.Join(lab.artifactDir, label+"-ready.json"),
		resultPath:  filepath.Join(lab.artifactDir, label+"-result.json"),
		releasePath: filepath.Join(lab.artifactDir, label+"-release"),
	}
	args := []string{
		"--mode", "migration-" + side, "--bind4", dataAddress.String(), "--tcp4", controlAddress.String(),
		"--stun-server4", stunServer.String(), "--port", strconv.Itoa(clientMuxPort),
		"--ready-file", process.readyPath, "--result-file", process.resultPath,
		"--release-file", process.releasePath, "--timeout", "45s",
		"--ice-disconnected-timeout", "500ms", "--ice-failed-timeout", "1s",
		"--ice-keepalive-interval", "100ms", "--ice-check-interval", "20ms",
	}
	if side == "initiator" {
		args = append(args, "--remote", remote)
	}
	process.child = lab.startChild(label, namespace, os.Getenv("HYDRA_M5B_SIGNALHELPER"), args...)
	ready := waitMigrationReport(t, process.child, process.readyPath)
	if ready.Error != "" || ready.MuxPort != clientMuxPort {
		t.Fatalf("invalid migration helper readiness: %+v", ready)
	}
	return process, ready
}

func startIPv6MigrationHelper(t *testing.T, lab *lab, side, namespace string, dataAddress, controlAddress netip.Addr, remote string) (migrationChild, iceSignalReport) {
	t.Helper()
	label := "m5c3-ipv6-migration-" + side
	process := migrationChild{
		readyPath:   filepath.Join(lab.artifactDir, label+"-ready.json"),
		resultPath:  filepath.Join(lab.artifactDir, label+"-result.json"),
		releasePath: filepath.Join(lab.artifactDir, label+"-release"),
	}
	args := []string{
		"--mode", "migration-" + side, "--bind6", dataAddress.String(), "--tcp4", controlAddress.String(),
		"--port", strconv.Itoa(clientMuxPort), "--ready-file", process.readyPath,
		"--result-file", process.resultPath, "--release-file", process.releasePath, "--timeout", "45s",
		"--ice-disconnected-timeout", "500ms", "--ice-failed-timeout", "1s",
		"--ice-keepalive-interval", "100ms", "--ice-check-interval", "20ms",
	}
	if side == "initiator" {
		args = append(args, "--remote", remote)
	}
	process.child = lab.startChild(label, namespace, os.Getenv("HYDRA_M5B_SIGNALHELPER"), args...)
	ready := waitMigrationReport(t, process.child, process.readyPath)
	if ready.Error != "" || ready.MuxPort != clientMuxPort {
		t.Fatalf("invalid IPv6 migration helper readiness: %+v", ready)
	}
	return process, ready
}

func waitMigrationReport(t *testing.T, child *childProcess, path string) iceSignalReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, path); err != nil {
		t.Fatal(err)
	}
	var result iceSignalReport
	if err := readJSON(path, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("M5c3 migration helper error: %s", result.Error)
	}
	return result
}

func assertMigrationGeneration(t *testing.T, initiator, responder iceSignalReport, generation uint64, local netip.Addr, mapped netip.AddrPort) {
	t.Helper()
	if initiator.NetworkGeneration != generation || initiator.SessionGeneration != generation ||
		initiator.LocalIPv4 != netip.AddrPortFrom(local, clientMuxPort).String() ||
		initiator.ObservedSrflxIPv4 != mapped.String() || initiator.SelectedLocal != netip.AddrPortFrom(local, clientMuxPort).String() ||
		responder.SelectedRemote != mapped.String() || !initiator.Connected || !responder.Connected ||
		!initiator.Nominated || !responder.Nominated || initiator.ApplicationBytes != 0 || responder.ApplicationBytes != 0 ||
		initiator.RequestsSent == 0 || responder.RequestsSent == 0 {
		t.Fatalf("generation %d does not match its network/STUN/ICE path: A=%+v B=%+v", generation, initiator, responder)
	}
	if initiator.SessionID == "" || initiator.SessionID != responder.SessionID {
		t.Fatalf("generation %d signaling identities disagree", generation)
	}
}

func assertIPv6MigrationGeneration(t *testing.T, initiator, responder iceSignalReport, generation uint64, local netip.Addr) {
	t.Helper()
	endpoint := netip.AddrPortFrom(local, clientMuxPort).String()
	if initiator.NetworkGeneration != generation || initiator.SessionGeneration != generation ||
		initiator.LocalIPv6 != endpoint || initiator.ObservedSrflxIPv4 != "" ||
		initiator.SelectedLocal != endpoint || responder.SelectedRemote != endpoint ||
		!initiator.Connected || !responder.Connected || !initiator.Nominated || !responder.Nominated ||
		initiator.ApplicationBytes != 0 || responder.ApplicationBytes != 0 ||
		initiator.RequestsSent == 0 || responder.RequestsSent == 0 {
		t.Fatalf("IPv6 generation %d does not match its direct ICE path: A=%+v B=%+v", generation, initiator, responder)
	}
	if initiator.SessionID == "" || initiator.SessionID != responder.SessionID {
		t.Fatalf("IPv6 generation %d signaling identities disagree", generation)
	}
}
