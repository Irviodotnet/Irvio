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

type reconnectChild struct {
	child              *childProcess
	resultPath         string
	connectReadyPrefix string
	connectStartPrefix string
	disconnectedPath   string
	failedPath         string
	releasePath        string
	awgReadyPath       string
	awgStartPath       string
}

func TestM5C3_TransientDisconnectedRecoversWithoutRestart(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	lab := newLab(t, "m5c3-recovery")
	topology := lab.buildDirectDualStack()
	lab.installNFT(topology.ClientNamespace, "m5c3-recovery-client", reconnectBlockRules())
	lab.installNFT(topology.ServerNamespace, "m5c3-recovery-server", reconnectBlockRules())

	responder, ready := startRecoveryHelper(t, lab, "responder", topology.ServerNamespace, topology.ServerIPv4A1, "")
	remote := selectSignalAddress(t, ready.ListenAddresses, "ipv4")
	initiator, _ := startRecoveryHelper(t, lab, "initiator", topology.ClientNamespace, topology.ClientIPv4, remote)
	waitReconnectReady(t, initiator, 1)
	waitReconnectReady(t, responder, 1)
	touchReconnectStart(t, initiator, 1)
	touchReconnectStart(t, responder, 1)
	firstInitiator := waitReconnectReport(t, initiator.child, initiator.resultPath+".generation-1")
	firstResponder := waitReconnectReport(t, responder.child, responder.resultPath+".generation-1")
	assertConnectedReports(t, firstInitiator, firstResponder, true, false)

	setReconnectBlocked(t, lab, topology.ClientNamespace, topology.ServerIPv4A1, true)
	setReconnectBlocked(t, lab, topology.ServerNamespace, topology.ClientIPv4, true)
	waitReconnectReport(t, initiator.child, initiator.disconnectedPath)
	waitReconnectReport(t, responder.child, responder.disconnectedPath)
	setReconnectBlocked(t, lab, topology.ClientNamespace, topology.ServerIPv4A1, false)
	setReconnectBlocked(t, lab, topology.ServerNamespace, topology.ClientIPv4, false)
	recoveredInitiator := waitReconnectReport(t, initiator.child, initiator.resultPath)
	recoveredResponder := waitReconnectReport(t, responder.child, responder.resultPath)
	assertConnectedReports(t, recoveredInitiator, recoveredResponder, true, false)
	for side, pair := range map[string][2]iceSignalReport{
		"initiator": {firstInitiator, recoveredInitiator}, "responder": {firstResponder, recoveredResponder},
	} {
		first, recovered := pair[0], pair[1]
		if !recovered.DisconnectedObserved || recovered.FailedObserved || recovered.SessionGeneration != 1 ||
			recovered.SessionID != first.SessionID || recovered.LocalUfrag != first.LocalUfrag ||
			recovered.PreviousSessionID != "" || recovered.CredentialsRotated || recovered.PionAgentReplaced ||
			recovered.UDP4SocketInodeAfter != first.UDP4SocketInodeReady {
			t.Fatalf("%s transient loss rotated the ICE generation: first=%+v recovered=%+v", side, first, recovered)
		}
	}
	for _, process := range []reconnectChild{initiator, responder} {
		if _, err := os.Stat(fmt.Sprintf("%s-2.json", process.connectReadyPrefix)); !os.IsNotExist(err) {
			t.Fatalf("transient recovery prepared a second ICE generation: %v", err)
		}
		if err := touch(process.releasePath); err != nil {
			t.Fatal(err)
		}
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := initiator.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	if err := responder.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
}

func TestM5C3_PersistentFailureCreatesFreshSessionGeneration(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	lab := newLab(t, "m5c3-reconnect")
	topology := lab.buildDirectDualStack()
	lab.installNFT(topology.ClientNamespace, "m5c3-client-block", reconnectBlockRules())
	lab.installNFT(topology.ServerNamespace, "m5c3-server-block", reconnectBlockRules())

	responder, responderReady := startReconnectHelper(t, lab, "responder", topology.ServerNamespace, topology.ServerIPv4A1, "")
	remote := selectSignalAddress(t, responderReady.ListenAddresses, "ipv4")
	initiator, initiatorReady := startReconnectHelper(t, lab, "initiator", topology.ClientNamespace, topology.ClientIPv4, remote)
	if initiatorReady.MuxPort != clientMuxPort || responderReady.MuxPort != clientMuxPort {
		t.Fatal("reconnect helpers did not bind the requested Irvio port")
	}

	waitReconnectReady(t, initiator, 1)
	waitReconnectReady(t, responder, 1)
	touchReconnectStart(t, initiator, 1)
	touchReconnectStart(t, responder, 1)
	firstInitiator := waitReconnectReport(t, initiator.child, initiator.resultPath+".generation-1")
	firstResponder := waitReconnectReport(t, responder.child, responder.resultPath+".generation-1")
	assertConnectedReports(t, firstInitiator, firstResponder, true, false)
	if count, _ := namespaceUDPSockets(t, lab, topology.ClientNamespace); count != 1 {
		t.Fatalf("initiator has %d UDP4 sockets before failure", count)
	}
	if count, _ := namespaceUDPSockets(t, lab, topology.ServerNamespace); count != 1 {
		t.Fatalf("responder has %d UDP4 sockets before failure", count)
	}

	setReconnectBlocked(t, lab, topology.ClientNamespace, topology.ServerIPv4A1, true)
	setReconnectBlocked(t, lab, topology.ServerNamespace, topology.ClientIPv4, true)
	waitReconnectReport(t, initiator.child, initiator.failedPath)
	waitReconnectReport(t, responder.child, responder.failedPath)
	waitReconnectReady(t, initiator, 2)
	waitReconnectReady(t, responder, 2)

	setReconnectBlocked(t, lab, topology.ClientNamespace, topology.ServerIPv4A1, false)
	setReconnectBlocked(t, lab, topology.ServerNamespace, topology.ClientIPv4, false)
	touchReconnectStart(t, initiator, 2)
	touchReconnectStart(t, responder, 2)
	secondInitiator := waitReconnectReport(t, initiator.child, initiator.resultPath)
	secondResponder := waitReconnectReport(t, responder.child, responder.resultPath)
	assertConnectedReports(t, secondInitiator, secondResponder, true, false)
	assertReconnectRotation(t, firstInitiator, secondInitiator)
	assertReconnectRotation(t, firstResponder, secondResponder)
	if firstInitiator.SessionID != firstResponder.SessionID || secondInitiator.SessionID != secondResponder.SessionID {
		t.Fatal("authenticated reconnect SessionIDs disagree between peers")
	}
	if firstInitiator.UDP4SocketInodeReady != secondInitiator.UDP4SocketInodeAfter ||
		firstResponder.UDP4SocketInodeReady != secondResponder.UDP4SocketInodeAfter {
		t.Fatal("same-network reconnect replaced a Irvio physical UDP socket")
	}
	for _, item := range []struct{ namespace, label string }{
		{topology.ClientNamespace, "initiator"}, {topology.ServerNamespace, "responder"},
	} {
		rules := lab.inNamespace(item.namespace, "nft", "-j", "list", "ruleset")
		inPackets, inOK := counterForComment(rules, "m5c3-path-block-in")
		outPackets, outOK := counterForComment(rules, "m5c3-path-block-out")
		if !inOK || !outOK || inPackets+outPackets == 0 {
			t.Fatalf("%s failure lacks independent blocked-packet evidence: in=%d/%v out=%d/%v", item.label, inPackets, inOK, outPackets, outOK)
		}
	}

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
}

func TestM5C3_PersistentIPv6FailureCreatesFreshSessionGeneration(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	lab := newLab(t, "m5c3-reconnect6")
	topology := lab.buildDirectDualStack()
	lab.installNFT(topology.ClientNamespace, "m5c3-client6-block", reconnectBlockRules6())
	lab.installNFT(topology.ServerNamespace, "m5c3-server6-block", reconnectBlockRules6())

	responder, responderReady := startReconnectHelper(t, lab, "responder", topology.ServerNamespace, topology.ServerIPv6A1, "")
	remote := selectSignalAddress(t, responderReady.ListenAddresses, "ipv6")
	initiator, initiatorReady := startReconnectHelper(t, lab, "initiator", topology.ClientNamespace, topology.ClientIPv6, remote)
	waitReconnectReady(t, initiator, 1)
	waitReconnectReady(t, responder, 1)
	touchReconnectStart(t, initiator, 1)
	touchReconnectStart(t, responder, 1)
	firstInitiator := waitReconnectReport(t, initiator.child, initiator.resultPath+".generation-1")
	firstResponder := waitReconnectReport(t, responder.child, responder.resultPath+".generation-1")
	assertConnectedReports(t, firstInitiator, firstResponder, false, true)

	setReconnectBlockedFamily(t, lab, topology.ClientNamespace, topology.ServerIPv6A1, true)
	setReconnectBlockedFamily(t, lab, topology.ServerNamespace, topology.ClientIPv6, true)
	waitReconnectReport(t, initiator.child, initiator.failedPath)
	waitReconnectReport(t, responder.child, responder.failedPath)
	waitReconnectReady(t, initiator, 2)
	waitReconnectReady(t, responder, 2)
	setReconnectBlockedFamily(t, lab, topology.ClientNamespace, topology.ServerIPv6A1, false)
	setReconnectBlockedFamily(t, lab, topology.ServerNamespace, topology.ClientIPv6, false)
	touchReconnectStart(t, initiator, 2)
	touchReconnectStart(t, responder, 2)
	secondInitiator := waitReconnectReport(t, initiator.child, initiator.resultPath)
	secondResponder := waitReconnectReport(t, responder.child, responder.resultPath)
	assertConnectedReports(t, secondInitiator, secondResponder, false, true)
	assertReconnectRotation(t, firstInitiator, secondInitiator)
	assertReconnectRotation(t, firstResponder, secondResponder)
	if initiatorReady.UDP6SocketInodeReady == "" || initiatorReady.UDP6SocketInodeReady != secondInitiator.UDP6SocketInodeAfter ||
		responderReady.UDP6SocketInodeReady == "" || responderReady.UDP6SocketInodeReady != secondResponder.UDP6SocketInodeAfter {
		t.Fatal("same-network IPv6 reconnect replaced a Irvio physical UDP socket")
	}
	for _, process := range []reconnectChild{initiator, responder} {
		if err := touch(process.releasePath); err != nil {
			t.Fatal(err)
		}
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := initiator.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	if err := responder.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
}

func TestM5C3_DualStackSelectedFamilyLossRecoversOrRestarts(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	lab := newLab(t, "m5c3-dual-failover")
	topology := lab.buildDirectDualStack()
	clientControlIf := lab.interfaceName('k')
	serverControlIf := lab.interfaceName('l')
	clientControl := netip.MustParseAddr("192.0.2.51")
	serverControl := netip.MustParseAddr("192.0.2.52")
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

	responder, responderReady := startDualStackFailoverHelper(t, lab, "responder", topology.ServerNamespace,
		topology.ServerIPv4A1, topology.ServerIPv6A1, serverControl, "")
	remote := selectSignalAddress(t, responderReady.ListenAddresses, "ipv4")
	initiator, initiatorReady := startDualStackFailoverHelper(t, lab, "initiator", topology.ClientNamespace,
		topology.ClientIPv4, topology.ClientIPv6, clientControl, remote)
	waitReconnectReady(t, initiator, 1)
	waitReconnectReady(t, responder, 1)
	touchReconnectStart(t, initiator, 1)
	touchReconnectStart(t, responder, 1)
	firstInitiator := waitReconnectReport(t, initiator.child, initiator.resultPath+".generation-1")
	firstResponder := waitReconnectReport(t, responder.child, responder.resultPath+".generation-1")
	assertConnectedReports(t, firstInitiator, firstResponder, true, true)
	assertControlAddressNotCandidate(t, firstInitiator, clientControl, serverControl)
	assertControlAddressNotCandidate(t, firstResponder, clientControl, serverControl)
	if firstInitiator.SelectedNetwork != firstResponder.SelectedNetwork {
		t.Fatalf("dual-stack peers disagree on the selected family: A=%+v B=%+v", firstInitiator, firstResponder)
	}

	removeSelectedFamilyRoute(t, lab, topology, firstInitiator.SelectedNetwork)
	waitReconnectReport(t, initiator.child, initiator.disconnectedPath)
	waitReconnectReport(t, responder.child, responder.disconnectedPath)
	initiatorOutcome, initiatorFailed := waitReconnectOutcome(t, initiator)
	responderOutcome, responderFailed := waitReconnectOutcome(t, responder)
	if initiatorFailed != responderFailed {
		t.Fatalf("dual-stack peers reached inconsistent failover outcomes: initiator_failed=%v responder_failed=%v", initiatorFailed, responderFailed)
	}
	if initiatorFailed {
		waitReconnectReady(t, initiator, 2)
		waitReconnectReady(t, responder, 2)
		touchReconnectStart(t, initiator, 2)
		touchReconnectStart(t, responder, 2)
		initiatorOutcome = waitReconnectReport(t, initiator.child, initiator.resultPath)
		responderOutcome = waitReconnectReport(t, responder.child, responder.resultPath)
		assertReconnectRotation(t, firstInitiator, initiatorOutcome)
		assertReconnectRotation(t, firstResponder, responderOutcome)
	} else {
		for side, reports := range map[string][2]iceSignalReport{
			"initiator": {firstInitiator, initiatorOutcome},
			"responder": {firstResponder, responderOutcome},
		} {
			first, recovered := reports[0], reports[1]
			if recovered.SessionID != first.SessionID || recovered.LocalUfrag != first.LocalUfrag ||
				recovered.SessionGeneration != 1 || recovered.FailedObserved || recovered.PionAgentReplaced {
				t.Fatalf("%s natural family failover unexpectedly rotated the ICE session: first=%+v recovered=%+v", side, first, recovered)
			}
		}
	}
	assertConnectedReports(t, initiatorOutcome, responderOutcome, true, true)
	if initiatorOutcome.SelectedNetwork == firstInitiator.SelectedNetwork || responderOutcome.SelectedNetwork == firstResponder.SelectedNetwork {
		t.Fatalf("selected family did not move away from the removed route: first=%d final=%d/%d",
			firstInitiator.SelectedNetwork, initiatorOutcome.SelectedNetwork, responderOutcome.SelectedNetwork)
	}
	for side, reports := range map[string][2]iceSignalReport{
		"initiator": {initiatorReady, initiatorOutcome},
		"responder": {responderReady, responderOutcome},
	} {
		ready, final := reports[0], reports[1]
		if ready.UDP4SocketInodeReady == "" || ready.UDP6SocketInodeReady == "" ||
			ready.UDP4SocketInodeReady != final.UDP4SocketInodeAfter ||
			ready.UDP6SocketInodeReady != final.UDP6SocketInodeAfter {
			t.Fatalf("%s family failover replaced a Irvio socket: ready=%+v final=%+v", side, ready, final)
		}
	}
	for _, item := range []struct{ namespace, side string }{
		{topology.ClientNamespace, "initiator"},
		{topology.ServerNamespace, "responder"},
	} {
		udp4, udp6 := namespaceUDPSockets(t, lab, item.namespace)
		if udp4 != 1 || udp6 != 1 {
			t.Fatalf("%s has extra dual-stack UDP sockets after failover: udp4=%d udp6=%d", item.side, udp4, udp6)
		}
	}
	for _, process := range []reconnectChild{initiator, responder} {
		if err := touch(process.releasePath); err != nil {
			t.Fatal(err)
		}
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := initiator.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	if err := responder.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
}

func assertControlAddressNotCandidate(t *testing.T, value iceSignalReport, addresses ...netip.Addr) {
	t.Helper()
	blocked := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		blocked[address.String()] = struct{}{}
	}
	for _, candidates := range [][]iceSignalCandidateReport{value.LocalCandidates, value.RemoteCandidates, value.LearnedCandidates} {
		for _, candidate := range candidates {
			if _, exists := blocked[candidate.Address]; exists {
				t.Fatalf("test-only control address entered ICE candidate state: %+v", candidate)
			}
		}
	}
}

func TestM5C3_EIMAPDFNATReconnectUsesFreshICESessionOnSameSocket(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	lab := newLab(t, "m5c3-nat-reconnect")
	topology := lab.buildICENATTopology(true, false)
	server := lab.startResponder("m5c3-nat-stun", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
	scenario := iceNATScenario{
		name: "m5c3-eim-apdf", aNAT: true, aMapping: mappingEIM,
		aFiltering: filteringAPDF, wantConnected: true,
	}
	installICENATScenarioRules(lab, topology, scenario)
	lab.installNFT(topology.ARouter, "m5c3-nat-block-router", reconnectRouterBlockRules())
	lab.installNFT(topology.BNamespace, "m5c3-nat-block-public", reconnectBlockRules())
	stunServer := endpointValue(topology.ServerA1, stunPrimaryPort)

	responder, responderReady := startNATReconnectHelper(t, lab, "responder", topology.BNamespace,
		topology.BData, topology.BControl, stunServer, "")
	remote := selectSignalAddress(t, responderReady.ListenAddresses, "ipv4")
	initiator, initiatorReady := startNATReconnectHelper(t, lab, "initiator", topology.ANamespace,
		topology.AData, topology.AControl, stunServer, remote)
	expectedA := netip.AddrPortFrom(topology.AExternal, m5c2AStunPort)
	expectedB := netip.AddrPortFrom(topology.BData, clientMuxPort)
	assertSTUNReadiness(t, initiatorReady, expectedA, topology.AData)
	assertSTUNReadiness(t, responderReady, expectedB, topology.BData)

	waitReconnectReady(t, initiator, 1)
	waitReconnectReady(t, responder, 1)
	touchReconnectStart(t, initiator, 1)
	touchReconnectStart(t, responder, 1)
	firstInitiator := waitReconnectReport(t, initiator.child, initiator.resultPath+".generation-1")
	firstResponder := waitReconnectReport(t, responder.child, responder.resultPath+".generation-1")
	assertNATConnected(t, firstInitiator, firstResponder, topology, expectedA, expectedB, scenario)

	setReconnectBlocked(t, lab, topology.ARouter, topology.BData, true)
	setReconnectBlocked(t, lab, topology.BNamespace, topology.AExternal, true)
	waitReconnectReport(t, initiator.child, initiator.failedPath)
	waitReconnectReport(t, responder.child, responder.failedPath)
	waitReconnectReady(t, initiator, 2)
	waitReconnectReady(t, responder, 2)
	setReconnectBlocked(t, lab, topology.ARouter, topology.BData, false)
	setReconnectBlocked(t, lab, topology.BNamespace, topology.AExternal, false)
	touchReconnectStart(t, initiator, 2)
	touchReconnectStart(t, responder, 2)
	secondInitiator := waitReconnectReport(t, initiator.child, initiator.resultPath)
	secondResponder := waitReconnectReport(t, responder.child, responder.resultPath)
	assertNATConnected(t, secondInitiator, secondResponder, topology, expectedA, expectedB, scenario)
	assertReconnectRotation(t, firstInitiator, secondInitiator)
	assertReconnectRotation(t, firstResponder, secondResponder)
	assertSocketContinuity(t, initiatorReady, secondInitiator)
	assertSocketContinuity(t, responderReady, secondResponder)
	if secondInitiator.ObservedSrflxIPv4 != expectedA.String() || secondResponder.ObservedSrflxIPv4 != expectedB.String() {
		t.Fatalf("same-generation reconnect lost its generation-bound STUN observations: A=%+v B=%+v", secondInitiator, secondResponder)
	}

	for _, item := range []struct{ namespace, label string }{
		{topology.ARouter, "NAT router"}, {topology.BNamespace, "public peer"},
	} {
		rules := lab.inNamespace(item.namespace, "nft", "-j", "list", "table", "ip", "hydra_m5c3")
		inPackets, inOK := counterForComment(rules, "m5c3-path-block-in")
		outPackets, outOK := counterForComment(rules, "m5c3-path-block-out")
		if !inOK || !outOK || inPackets+outPackets == 0 {
			t.Fatalf("%s failure lacks independent blocked NAT-path evidence: in=%d/%v out=%d/%v", item.label, inPackets, inOK, outPackets, outOK)
		}
	}
	assertSTUNOracle(t, server.observationPath, expectedA, expectedB)
	assertNATPacketOracle(t, lab, topology, scenario)
	for _, process := range []reconnectChild{initiator, responder} {
		if err := touch(process.releasePath); err != nil {
			t.Fatal(err)
		}
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := initiator.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	if err := responder.child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
}

func startReconnectHelper(t *testing.T, lab *lab, side, namespace string, bind netip.Addr, remote string) (reconnectChild, iceSignalReport) {
	t.Helper()
	return startLifecycleHelper(t, lab, side, namespace, bind, remote, false)
}

func startRecoveryHelper(t *testing.T, lab *lab, side, namespace string, bind netip.Addr, remote string) (reconnectChild, iceSignalReport) {
	t.Helper()
	return startLifecycleHelper(t, lab, side, namespace, bind, remote, true)
}

func startNATReconnectHelper(t *testing.T, lab *lab, side, namespace string, dataAddress, controlAddress netip.Addr, stunServer netip.AddrPort, remote string) (reconnectChild, iceSignalReport) {
	t.Helper()
	return startLifecycleHelperWithNetwork(t, lab, side, namespace, dataAddress, controlAddress, stunServer, remote, false)
}

func startDualStackFailoverHelper(t *testing.T, lab *lab, side, namespace string, bind4, bind6, control4 netip.Addr, remote string) (reconnectChild, iceSignalReport) {
	t.Helper()
	return startLifecycleHelperWithBinds(t, lab, side, namespace, bind4, bind6, control4, netip.Addr{}, netip.AddrPort{}, remote, false, true)
}

func startLifecycleHelper(t *testing.T, lab *lab, side, namespace string, bind netip.Addr, remote string, transient bool) (reconnectChild, iceSignalReport) {
	t.Helper()
	return startLifecycleHelperWithNetwork(t, lab, side, namespace, bind, bind, netip.AddrPort{}, remote, transient)
}

func startLifecycleHelperWithNetwork(t *testing.T, lab *lab, side, namespace string, dataAddress, controlAddress netip.Addr, stunServer netip.AddrPort, remote string, transient bool) (reconnectChild, iceSignalReport) {
	t.Helper()
	var bind4, bind6, tcp4, tcp6 netip.Addr
	if dataAddress.Is4() {
		bind4, tcp4 = dataAddress, controlAddress
	} else {
		bind6, tcp6 = dataAddress, controlAddress
	}
	return startLifecycleHelperWithBinds(t, lab, side, namespace, bind4, bind6, tcp4, tcp6, stunServer, remote, transient, false)
}

func startLifecycleHelperWithBinds(t *testing.T, lab *lab, side, namespace string, bind4, bind6, tcp4, tcp6 netip.Addr, stunServer netip.AddrPort, remote string, transient, familyFailover bool) (reconnectChild, iceSignalReport) {
	t.Helper()
	label := "m5c3-" + side
	readyPath := filepath.Join(lab.artifactDir, label+"-ready.json")
	process := reconnectChild{
		resultPath:         filepath.Join(lab.artifactDir, label+"-result.json"),
		connectReadyPrefix: filepath.Join(lab.artifactDir, label+"-connect-ready"),
		connectStartPrefix: filepath.Join(lab.artifactDir, label+"-connect-start"),
		disconnectedPath:   filepath.Join(lab.artifactDir, label+"-disconnected.json"),
		failedPath:         filepath.Join(lab.artifactDir, label+"-failed.json"),
		releasePath:        filepath.Join(lab.artifactDir, label+"-release"),
	}
	failedTimeout := "400ms"
	if transient {
		failedTimeout = "3s"
	}
	args := []string{
		"--mode", "reconnect-" + side,
		"--port", strconv.Itoa(clientMuxPort), "--ready-file", readyPath, "--result-file", process.resultPath,
		"--connect-ready-file", process.connectReadyPrefix, "--connect-start-file", process.connectStartPrefix,
		"--disconnected-file", process.disconnectedPath, "--failed-file", process.failedPath,
		"--release-file", process.releasePath, "--timeout", "20s",
		"--ice-disconnected-timeout", "250ms", "--ice-failed-timeout", failedTimeout,
		"--ice-keepalive-interval", "50ms", "--ice-check-interval", "20ms",
	}
	if bind4.IsValid() {
		args = append(args, "--bind4", bind4.String())
	}
	if bind6.IsValid() {
		args = append(args, "--bind6", bind6.String())
	}
	if tcp4.IsValid() {
		args = append(args, "--tcp4", tcp4.String())
	}
	if tcp6.IsValid() {
		args = append(args, "--tcp6", tcp6.String())
	}
	if stunServer.IsValid() {
		args = append(args, "--stun-server4", stunServer.String())
	}
	if transient {
		args = append(args, "--transient-recovery")
	}
	if familyFailover {
		args = append(args, "--family-failover")
	}
	if side == "initiator" {
		args = append(args, "--remote", remote)
	}
	process.child = lab.startChild(label, namespace, os.Getenv("HYDRA_M5B_SIGNALHELPER"), args...)
	ready := waitReconnectReport(t, process.child, readyPath)
	if ready.Error != "" || ready.MuxPort != clientMuxPort ||
		(bind4.IsValid() && ready.UDP4SocketInodeReady == "") || (bind6.IsValid() && ready.UDP6SocketInodeReady == "") {
		t.Fatalf("invalid reconnect helper readiness: %+v", ready)
	}
	return process, ready
}

func waitReconnectOutcome(t *testing.T, process reconnectChild) (iceSignalReport, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, item := range []struct {
			path   string
			failed bool
		}{{process.resultPath, false}, {process.failedPath, true}} {
			if info, err := os.Stat(item.path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				var result iceSignalReport
				if err := readJSON(item.path, &result); err != nil {
					t.Fatal(err)
				}
				if result.Error != "" {
					t.Fatalf("M5c3 family-failover helper error: %s", result.Error)
				}
				return result, item.failed
			} else if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-process.child.done:
			process.child.mu.Lock()
			err := process.child.waitErr
			process.child.mu.Unlock()
			t.Fatalf("family-failover helper exited before an outcome: %v", err)
		case <-ticker.C:
		}
	}
}

func removeSelectedFamilyRoute(t *testing.T, lab *lab, topology directTopology, network uint8) {
	t.Helper()
	serverInterface := lab.interfaceName('s')
	if network == 1 {
		prefix := "198.18.10.0/24"
		lab.inNamespace(topology.ClientNamespace, "ip", "route", "del", prefix, "dev", topology.ClientInterface)
		lab.inNamespace(topology.ServerNamespace, "ip", "route", "del", prefix, "dev", serverInterface)
		for _, item := range []struct{ namespace, destination string }{
			{topology.ClientNamespace, topology.ServerIPv4A1.String()},
			{topology.ServerNamespace, topology.ClientIPv4.String()},
		} {
			if routes := strings.TrimSpace(lab.inNamespace(item.namespace, "ip", "route", "show", "to", item.destination)); routes != "" {
				t.Fatalf("IPv4 selected-family route remains in %s: %s", item.namespace, routes)
			}
		}
		return
	}
	if network != 2 {
		t.Fatalf("unexpected selected ICE network %d", network)
	}
	prefix := "2001:db8:10::/64"
	lab.inNamespace(topology.ClientNamespace, "ip", "-6", "route", "del", prefix, "dev", topology.ClientInterface)
	lab.inNamespace(topology.ServerNamespace, "ip", "-6", "route", "del", prefix, "dev", serverInterface)
	for _, item := range []struct{ namespace, destination string }{
		{topology.ClientNamespace, topology.ServerIPv6A1.String()},
		{topology.ServerNamespace, topology.ClientIPv6.String()},
	} {
		if routes := strings.TrimSpace(lab.inNamespace(item.namespace, "ip", "-6", "route", "show", "to", item.destination)); routes != "" {
			t.Fatalf("IPv6 selected-family route remains in %s: %s", item.namespace, routes)
		}
	}
}

func waitReconnectReady(t *testing.T, process reconnectChild, generation int) iceSignalReport {
	t.Helper()
	return waitReconnectReport(t, process.child, fmt.Sprintf("%s-%d.json", process.connectReadyPrefix, generation))
}

func touchReconnectStart(t *testing.T, process reconnectChild, generation int) {
	t.Helper()
	if err := touch(fmt.Sprintf("%s-%d", process.connectStartPrefix, generation)); err != nil {
		t.Fatal(err)
	}
}

func waitReconnectReport(t *testing.T, child *childProcess, path string) iceSignalReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, path); err != nil {
		t.Fatal(err)
	}
	var result iceSignalReport
	if err := readJSON(path, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("M5c3 reconnect helper error: %s", result.Error)
	}
	return result
}

func assertReconnectRotation(t *testing.T, first, second iceSignalReport) {
	t.Helper()
	if first.NetworkGeneration != 1 || second.NetworkGeneration != 1 || first.SessionGeneration != 1 || second.SessionGeneration != 2 ||
		first.SessionID == second.SessionID || first.LocalUfrag == second.LocalUfrag || second.PreviousSessionID != first.SessionID ||
		second.PreviousLocalUfrag != first.LocalUfrag || !second.CredentialsRotated || !second.PionAgentReplaced ||
		!second.DisconnectedObserved || !second.FailedObserved {
		t.Fatalf("fresh same-network ICE generation was not proven: first=%+v second=%+v", first, second)
	}
}

func reconnectBlockRules() string {
	return fmt.Sprintf(`table ip hydra_m5c3 {
 set blocked {
  type ipv4_addr
 }
 chain input {
  type filter hook input priority filter; policy accept;
  ip saddr @blocked udp dport %d counter drop comment "m5c3-path-block-in"
 }
 chain output {
  type filter hook output priority filter; policy accept;
  ip daddr @blocked udp sport %d counter drop comment "m5c3-path-block-out"
 }
}
`, clientMuxPort, clientMuxPort)
}

func reconnectRouterBlockRules() string {
	return `table ip hydra_m5c3 {
 set blocked {
  type ipv4_addr
 }
 chain forward {
  type filter hook forward priority -100; policy accept;
  ip saddr @blocked meta l4proto udp counter drop comment "m5c3-path-block-in"
 ip daddr @blocked meta l4proto udp counter drop comment "m5c3-path-block-out"
 }
}
`
}

func reconnectBlockRules6() string {
	return fmt.Sprintf(`table ip6 hydra_m5c3 {
 set blocked {
  type ipv6_addr
 }
 chain input {
  type filter hook input priority filter; policy accept;
  ip6 saddr @blocked udp dport %d counter drop comment "m5c3-path-block-in"
 }
 chain output {
  type filter hook output priority filter; policy accept;
  ip6 daddr @blocked udp sport %d counter drop comment "m5c3-path-block-out"
 }
}
`, clientMuxPort, clientMuxPort)
}

func setReconnectBlocked(t *testing.T, lab *lab, namespace string, peerAddress netip.Addr, blocked bool) {
	t.Helper()
	setReconnectBlockedFamily(t, lab, namespace, peerAddress, blocked)
}

func setReconnectBlockedFamily(t *testing.T, lab *lab, namespace string, peerAddress netip.Addr, blocked bool) {
	t.Helper()
	operation := "add"
	if !blocked {
		operation = "delete"
	}
	family := "ip"
	if peerAddress.Is6() {
		family = "ip6"
	}
	lab.inNamespace(namespace, "nft", operation, "element", family, "hydra_m5c3", "blocked", "{", peerAddress.String(), "}")
}
