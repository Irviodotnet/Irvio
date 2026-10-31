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

	"github.com/pion/ice/v4"
)

type iceNATScenario struct {
	name                          string
	aNAT, bNAT                    bool
	aMapping, bMapping            mappingPolicy
	aFiltering, bFiltering        filteringPolicy
	wantConnected                 bool
	wantAPeerReflexiveForBMapping bool
}

func TestM5C2_ControlledNATConnectivity(t *testing.T) {
	requirePrivileged(t)
	scenarios := []iceNATScenario{
		{name: "A-nat-eim-apdf-to-public", aNAT: true, aMapping: mappingEIM, aFiltering: filteringAPDF, wantConnected: true},
		{name: "B-public-to-nat-eim-apdf", bNAT: true, bMapping: mappingEIM, bFiltering: filteringAPDF, wantConnected: true},
		{name: "C-nat-eim-apdf-to-nat-eim-apdf", aNAT: true, bNAT: true, aMapping: mappingEIM, bMapping: mappingEIM, aFiltering: filteringAPDF, bFiltering: filteringAPDF, wantConnected: true},
		{name: "D-nat-eim-eif-to-nat-apdm-apdf-prflx", aNAT: true, bNAT: true, aMapping: mappingEIM, bMapping: mappingAPDM, aFiltering: filteringEIF, bFiltering: filteringAPDF, wantConnected: true, wantAPeerReflexiveForBMapping: true},
		{name: "E-nat-apdm-apdf-to-nat-apdm-apdf", aNAT: true, bNAT: true, aMapping: mappingAPDM, bMapping: mappingAPDM, aFiltering: filteringAPDF, bFiltering: filteringAPDF},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			runControlledICENATScenario(t, scenario)
		})
	}
}

func TestM5C2_WrongServerReflexivePortFails(t *testing.T) {
	requirePrivileged(t)
	scenario := iceNATScenario{
		name: "wrong-srflx", aNAT: true, aMapping: mappingEIM, aFiltering: filteringAPDF,
	}
	lab := newLab(t, "m5c2-wrong-srflx")
	topology := lab.buildICENATTopology(true, false)
	server := lab.startResponder("m5c2-stun", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
	installICENATScenarioRules(lab, topology, scenario)
	lab.inNamespace(topology.ARouter, "nft", "insert", "rule", "ip", "hydra_m5c2", "forward",
		"iifname", topology.AInsideIf, "ip", "saddr", topology.AData.String(), "ip", "daddr", topology.BData.String(),
		"udp", "sport", strconv.Itoa(clientMuxPort), "counter", "drop", "comment", "m5c2-wrong-srflx-outbound-drop")

	stunServer := endpointValue(topology.ServerA1, stunPrimaryPort)
	responder, responderReady := startICENATHelper(t, lab, "responder", topology.BNamespace,
		topology.BData, topology.BControl, stunServer, "", false, 4*time.Second)
	remote := selectSignalAddress(t, responderReady.ListenAddresses, "ipv4")
	wrong := netip.AddrPortFrom(topology.AExternal, 51099)
	initiator, initiatorReady := startICENATHelper(t, lab, "initiator", topology.ANamespace,
		topology.AData, topology.AControl, stunServer, remote, false, 4*time.Second,
		"--srflx4-override", wrong.String())
	realMapping := netip.AddrPortFrom(topology.AExternal, m5c2AStunPort)
	assertSTUNReadiness(t, initiatorReady, realMapping, topology.AData)
	waitForICEReady(t, initiator, responder)
	initiatorPrepared := readICEReady(t, initiator)
	assertPreparedCandidates(t, initiatorPrepared, wrong, topology.AData, topology.AControl, true)
	if initiatorPrepared.ObservedSrflxIPv4 != realMapping.String() || initiatorPrepared.ObservedSrflxIPv4 == wrong.String() {
		t.Fatalf("fault injection did not retain the real STUN observation: %+v", initiatorPrepared)
	}
	if err := touch(initiator.connectStartPath); err != nil {
		t.Fatal(err)
	}
	if err := touch(responder.connectStartPath); err != nil {
		t.Fatal(err)
	}
	waitNATChildren(t, initiator, responder, false)
	assertNATFailureReport(t, initiator)
	assertNATFailureReport(t, responder)
	assertPostCloseLifecycle(t, initiator)
	assertPostCloseLifecycle(t, responder)
	rules := lab.inNamespace(topology.ARouter, "nft", "-j", "list", "ruleset")
	if packets, ok := counterForComment(rules, "m5c2-wrong-srflx-outbound-drop"); !ok || packets == 0 {
		t.Fatalf("wrong-SRFLX failure lacks blocked peer-check evidence: %d/%v", packets, ok)
	}
	assertSTUNOracle(t, server.observationPath, realMapping, netip.AddrPortFrom(topology.BData, clientMuxPort))
}

func TestM5C2_ConcurrentIndependentPairs(t *testing.T) {
	requirePrivileged(t)
	for index := 0; index < 3; index++ {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()
			runControlledICENATScenario(t, iceNATScenario{
				name: "concurrent-" + strconv.Itoa(index), aNAT: true, bNAT: true,
				aMapping: mappingEIM, bMapping: mappingEIM,
				aFiltering: filteringAPDF, bFiltering: filteringAPDF, wantConnected: true,
			})
		})
	}
}

type natConnectChild struct {
	child            *childProcess
	readyPath        string
	connectReadyPath string
	connectStartPath string
	awgReadyPath     string
	awgStartPath     string
	resultPath       string
	releasePath      string
}

func runControlledICENATScenario(t *testing.T, scenario iceNATScenario) {
	t.Helper()
	lab := newLab(t, "m5c2-"+scenario.name)
	topology := lab.buildICENATTopology(scenario.aNAT, scenario.bNAT)
	server := lab.startResponder("m5c2-stun", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
	installICENATScenarioRules(lab, topology, scenario)

	timeout := 15 * time.Second
	if !scenario.wantConnected {
		timeout = 4 * time.Second
	}
	responder, responderReady := startICENATHelper(t, lab, "responder", topology.BNamespace,
		topology.BData, topology.BControl, endpointValue(topology.ServerA1, stunPrimaryPort), "", scenario.wantConnected, timeout)
	remote := selectSignalAddress(t, responderReady.ListenAddresses, "ipv4")
	initiator, initiatorReady := startICENATHelper(t, lab, "initiator", topology.ANamespace,
		topology.AData, topology.AControl, endpointValue(topology.ServerA1, stunPrimaryPort), remote, scenario.wantConnected, timeout)

	expectedA := expectedNATSTUNEndpoint(topology.AData, topology.AExternal, scenario.aNAT, m5c2AStunPort)
	expectedB := expectedNATSTUNEndpoint(topology.BData, topology.BExternal, scenario.bNAT, m5c2BStunPort)
	assertSTUNReadiness(t, initiatorReady, expectedA, topology.AData)
	assertSTUNReadiness(t, responderReady, expectedB, topology.BData)
	waitForICEReady(t, initiator, responder)
	initiatorPrepared := readICEReady(t, initiator)
	responderPrepared := readICEReady(t, responder)
	assertPreparedCandidates(t, initiatorPrepared, expectedA, topology.AData, topology.AControl, scenario.aNAT)
	assertPreparedCandidates(t, responderPrepared, expectedB, topology.BData, topology.BControl, scenario.bNAT)
	if err := touch(initiator.connectStartPath); err != nil {
		t.Fatal(err)
	}
	if err := touch(responder.connectStartPath); err != nil {
		t.Fatal(err)
	}

	if scenario.wantConnected {
		initiatorResult := waitNATConnectResult(t, initiator)
		responderResult := waitNATConnectResult(t, responder)
		assertNATConnected(t, initiatorResult, responderResult, topology, expectedA, expectedB, scenario)
		assertSocketContinuity(t, initiatorReady, initiatorResult)
		assertSocketContinuity(t, responderReady, responderResult)
		if count, _ := namespaceUDPSockets(t, lab, topology.ANamespace); count != 1 {
			t.Fatalf("A has %d UDP4 sockets, want one", count)
		}
		if count, _ := namespaceUDPSockets(t, lab, topology.BNamespace); count != 1 {
			t.Fatalf("B has %d UDP4 sockets, want one", count)
		}
		if err := touch(initiator.releasePath); err != nil {
			t.Fatal(err)
		}
		if err := touch(responder.releasePath); err != nil {
			t.Fatal(err)
		}
		waitNATChildren(t, initiator, responder, true)
		assertPostCloseLifecycle(t, initiator)
		assertPostCloseLifecycle(t, responder)
	} else {
		waitNATChildren(t, initiator, responder, false)
		assertNATFailureReport(t, initiator)
		assertNATFailureReport(t, responder)
		assertPostCloseLifecycle(t, initiator)
		assertPostCloseLifecycle(t, responder)
		assertExpectedAPDMFailureEvidence(t, lab, topology)
	}
	assertSTUNOracle(t, server.observationPath, expectedA, expectedB)
	assertNATPacketOracle(t, lab, topology, scenario)
	assertNoPrivateCrossRoute(t, lab, topology)
	lab.captureNamespace(topology.ANamespace, "m5c2-a", false)
	lab.captureNamespace(topology.BNamespace, "m5c2-b", false)
	if topology.ANAT {
		lab.captureNamespace(topology.ARouter, "m5c2-router-a", true)
	}
	if topology.BNAT {
		lab.captureNamespace(topology.BRouter, "m5c2-router-b", true)
	}
}

func installICENATScenarioRules(lab *lab, topology iceNATTopology, scenario iceNATScenario) {
	lab.t.Helper()
	aAdvertised, aPeer := peerNATPorts(scenario.aNAT, scenario.aMapping, m5c2AStunPort, m5c2APeerPort)
	bAdvertised, bPeer := peerNATPorts(scenario.bNAT, scenario.bMapping, m5c2BStunPort, m5c2BPeerPort)
	aExternal := peerExternal(topology.AData, topology.AExternal, scenario.aNAT)
	bExternal := peerExternal(topology.BData, topology.BExternal, scenario.bNAT)
	if scenario.aNAT {
		lab.installNFT(topology.ARouter, "m5c2-router-a", iceNATRules(
			topology.AData, topology.AExternal, topology.ServerA1, bExternal,
			topology.AInsideIf, topology.AOutsideIf, m5c2AStunPort, m5c2APeerPort,
			bAdvertised, bPeer, scenario.aMapping, scenario.aFiltering))
	} else {
		lab.installNFT(topology.ANamespace, "m5c2-public-a", publicICEOracleRules(topology.AData, topology.ServerA1, bExternal))
	}
	if scenario.bNAT {
		lab.installNFT(topology.BRouter, "m5c2-router-b", iceNATRules(
			topology.BData, topology.BExternal, topology.ServerA1, aExternal,
			topology.BInsideIf, topology.BOutsideIf, m5c2BStunPort, m5c2BPeerPort,
			aAdvertised, aPeer, scenario.bMapping, scenario.bFiltering))
	} else {
		lab.installNFT(topology.BNamespace, "m5c2-public-b", publicICEOracleRules(topology.BData, topology.ServerA1, aExternal))
	}
}

func startICENATHelper(t *testing.T, lab *lab, side, namespace string, dataAddress, controlAddress netip.Addr,
	stunServer netip.AddrPort, remote string, hold bool, timeout time.Duration, extra ...string,
) (natConnectChild, iceSignalReport) {
	return startICENATHelperMode(t, lab, side, namespace, dataAddress, controlAddress, stunServer, remote, hold, timeout, false, extra...)
}

func startM6NATHelper(t *testing.T, lab *lab, side, namespace string, dataAddress, controlAddress netip.Addr,
	stunServer netip.AddrPort, remote string, timeout time.Duration,
) (natConnectChild, iceSignalReport) {
	return startICENATHelperMode(t, lab, side, namespace, dataAddress, controlAddress, stunServer, remote, true, timeout, true)
}

func startICENATHelperMode(t *testing.T, lab *lab, side, namespace string, dataAddress, controlAddress netip.Addr,
	stunServer netip.AddrPort, remote string, hold bool, timeout time.Duration, withAWG bool, extra ...string,
) (natConnectChild, iceSignalReport) {
	t.Helper()
	prefix := "m5c2-"
	if withAWG {
		prefix = "m6-"
	}
	label := prefix + side
	process := natConnectChild{
		readyPath:        filepath.Join(lab.artifactDir, label+"-ready.json"),
		connectReadyPath: filepath.Join(lab.artifactDir, label+"-connect-ready.json"),
		connectStartPath: filepath.Join(lab.artifactDir, label+"-connect-start"),
		awgReadyPath:     filepath.Join(lab.artifactDir, label+"-awg-ready.json"),
		awgStartPath:     filepath.Join(lab.artifactDir, label+"-awg-start"),
		resultPath:       filepath.Join(lab.artifactDir, label+"-result.json"),
		releasePath:      filepath.Join(lab.artifactDir, label+"-release"),
	}
	args := []string{
		"--mode", "connect-" + side, "--port", strconv.Itoa(clientMuxPort),
		"--bind4", dataAddress.String(), "--tcp4", controlAddress.String(),
		"--stun-server4", stunServer.String(), "--ready-file", process.readyPath,
		"--connect-ready-file", process.connectReadyPath, "--connect-start-file", process.connectStartPath,
		"--result-file", process.resultPath, "--timeout", timeout.String(), "--verify-post-close",
	}
	if side == "initiator" {
		args = append(args, "--remote", remote)
	}
	if hold {
		args = append(args, "--release-file", process.releasePath)
	}
	if withAWG {
		localInner, remoteInner := "fd00:99::2", "fd00:99::1"
		localSeed, remoteSeed := "91", "71"
		if side == "initiator" {
			localInner, remoteInner = "fd00:99::1", "fd00:99::2"
			localSeed, remoteSeed = "71", "91"
		}
		args = append(args,
			"--awg", "--awg-ready-file", process.awgReadyPath, "--awg-start-file", process.awgStartPath,
			"--awg-inner-local", localInner, "--awg-inner-remote", remoteInner,
			"--awg-local-key-seed", localSeed, "--awg-remote-key-seed", remoteSeed,
		)
	}
	args = append(args, extra...)
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
	if ready.Error != "" || ready.MuxPort != clientMuxPort || ready.PID == 0 {
		t.Fatalf("invalid M5c2 readiness: %+v", ready)
	}
	return process, ready
}

func waitForAWGReady(t *testing.T, processes ...natConnectChild) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	for _, process := range processes {
		if err := waitForChildFile(ctx, process.child, process.awgReadyPath); err != nil {
			t.Fatal(err)
		}
	}
}

func waitForICEReady(t *testing.T, processes ...natConnectChild) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, process := range processes {
		if err := waitForChildFile(ctx, process.child, process.connectReadyPath); err != nil {
			t.Fatal(err)
		}
	}
}

func readICEReady(t *testing.T, process natConnectChild) iceSignalReport {
	t.Helper()
	var result iceSignalReport
	if err := readJSON(process.connectReadyPath, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func waitNATConnectResult(t *testing.T, process natConnectChild) iceSignalReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, process.child, process.resultPath); err != nil {
		t.Fatal(err)
	}
	var result iceSignalReport
	if err := readJSON(process.resultPath, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("M5c2 helper error: %s", result.Error)
	}
	return result
}

func waitNATChildren(t *testing.T, initiator, responder natConnectChild, wantSuccess bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, process := range []natConnectChild{initiator, responder} {
		err := process.child.wait(ctx)
		if wantSuccess && err != nil {
			t.Fatalf("connected helper failed during shutdown: %v", err)
		}
		if !wantSuccess && err == nil {
			t.Fatal("expected-unreachable helper exited successfully")
		}
	}
}

func assertSTUNReadiness(t *testing.T, report iceSignalReport, mapped netip.AddrPort, data netip.Addr) {
	t.Helper()
	if report.ObservedSrflxIPv4 != mapped.String() || report.LocalIPv4 != netip.AddrPortFrom(data, clientMuxPort).String() ||
		report.UDP4SocketInodeReady == "" {
		t.Fatalf("invalid same-socket STUN readiness: %+v want mapped=%s", report, mapped)
	}
}

func assertPreparedCandidates(t *testing.T, report iceSignalReport, mapped netip.AddrPort, data, control netip.Addr, nat bool) {
	t.Helper()
	host, srflx := false, false
	for _, candidate := range report.LocalCandidates {
		if candidate.Address == control.String() {
			t.Fatalf("control address leaked into ICE candidates: %+v", report.LocalCandidates)
		}
		if candidate.Type == "1" && candidate.Address == data.String() && candidate.Port == clientMuxPort {
			host = true
		}
		if candidate.Type == "2" && candidate.Address == mapped.Addr().String() && candidate.Port == mapped.Port() {
			srflx = true
		}
	}
	if !host || srflx != nat {
		t.Fatalf("candidate chain host=%v srflx=%v nat=%v candidates=%+v", host, srflx, nat, report.LocalCandidates)
	}
}

func assertNATConnected(t *testing.T, initiator, responder iceSignalReport, topology iceNATTopology,
	expectedA, expectedB netip.AddrPort, scenario iceNATScenario,
) {
	t.Helper()
	for side, report := range map[string]iceSignalReport{"initiator": initiator, "responder": responder} {
		if !report.Connected || !report.Nominated || report.SelectedNetwork != 1 ||
			report.SelectedLocalType != uint8(ice.CandidateTypeHost) || report.PairID == 0 ||
			report.RequestsSent == 0 || report.ResponsesReceived == 0 || report.AdapterAfter <= report.AdapterBefore ||
			report.ApplicationPackets != 0 || report.ApplicationBytes != 0 {
			t.Fatalf("%s lacks nominated ICE/check/zero-data evidence: %+v", side, report)
		}
		for _, candidate := range append(append([]iceSignalCandidateReport{}, report.LocalCandidates...), report.RemoteCandidates...) {
			if candidate.Address == topology.AControl.String() || candidate.Address == topology.BControl.String() {
				t.Fatalf("%s signaled control-plane candidate: %+v", side, candidate)
			}
		}
	}
	if initiator.SessionID == "" || initiator.SessionID != responder.SessionID {
		t.Fatalf("authenticated session mismatch: A=%+v B=%+v", initiator, responder)
	}
	assertSelectedRemoteType(t, "initiator", initiator.SelectedRemoteType, scenario.bNAT)
	assertSelectedRemoteType(t, "responder", responder.SelectedRemoteType, scenario.aNAT)
	wantRemoteA := expectedB
	if scenario.wantAPeerReflexiveForBMapping {
		wantRemoteA = netip.AddrPortFrom(topology.BExternal, m5c2BPeerPort)
	}
	if initiator.SelectedLocal != netip.AddrPortFrom(topology.AData, clientMuxPort).String() ||
		responder.SelectedLocal != netip.AddrPortFrom(topology.BData, clientMuxPort).String() ||
		initiator.SelectedRemote != wantRemoteA.String() || responder.SelectedRemote != expectedA.String() {
		t.Fatalf("selected endpoints disagree with NAT oracle: A=%+v B=%+v expected=%s/%s", initiator, responder, expectedA, expectedB)
	}
	if scenario.wantAPeerReflexiveForBMapping {
		want := netip.AddrPortFrom(topology.BExternal, m5c2BPeerPort)
		if initiator.SelectedRemote != want.String() || initiator.SelectedRemoteType != uint8(ice.CandidateTypePeerReflexive) ||
			!containsLearnedCandidate(initiator.LearnedCandidates, ice.CandidateTypePeerReflexive, want) {
			t.Fatalf("missing independently expected PRFLX %s: %+v", want, initiator)
		}
		if initiator.ObservedSrflxIPv4 == want.String() {
			t.Fatalf("PRFLX endpoint was incorrectly signaled as STUN mapping: %+v", initiator)
		}
	}
}

func assertSelectedRemoteType(t *testing.T, side string, candidateType uint8, remoteBehindNAT bool) {
	t.Helper()
	if !remoteBehindNAT && candidateType != uint8(ice.CandidateTypeHost) {
		t.Fatalf("%s selected non-HOST public remote type %d", side, candidateType)
	}
	if remoteBehindNAT && candidateType != uint8(ice.CandidateTypeServerReflexive) && candidateType != uint8(ice.CandidateTypePeerReflexive) {
		t.Fatalf("%s selected non-external NAT remote type %d", side, candidateType)
	}
}

func containsLearnedCandidate(candidates []iceSignalCandidateReport, candidateType ice.CandidateType, endpoint netip.AddrPort) bool {
	for _, candidate := range candidates {
		if candidate.Type == candidateType.String() && candidate.Address == endpoint.Addr().String() && candidate.Port == endpoint.Port() {
			return true
		}
	}
	return false
}

func assertSocketContinuity(t *testing.T, before, after iceSignalReport) {
	t.Helper()
	if before.UDP4SocketInodeReady == "" || before.UDP4SocketInodeReady != after.UDP4SocketInodeReady ||
		after.UDP4SocketInodeReady != after.UDP4SocketInodeAfter {
		t.Fatalf("Irvio UDP socket changed across STUN/ICE: before=%+v after=%+v", before, after)
	}
}

func assertNATFailureReport(t *testing.T, process natConnectChild) {
	t.Helper()
	var result iceSignalReport
	if err := readJSON(process.resultPath, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error == "" || result.Connected || result.Nominated || result.SelectedLocal != "" || result.SelectedRemote != "" {
		t.Fatalf("expected bounded NAT limitation falsely connected: %+v", result)
	}
}

func assertPostCloseLifecycle(t *testing.T, process natConnectChild) {
	t.Helper()
	var result iceSignalReport
	if err := readJSON(process.resultPath, &result); err != nil {
		t.Fatal(err)
	}
	if !result.PostCloseAdapterClean || !result.PostCloseDataWrite || !result.PostCloseSTUNLease {
		t.Fatalf("post-ICE lifecycle proof failed: %+v", result)
	}
}

func assertSTUNOracle(t *testing.T, path string, expected ...netip.AddrPort) {
	t.Helper()
	observations := mustObservations(t, path)
	for _, endpoint := range expected {
		found := false
		for _, observation := range observations {
			if observation.Source == endpoint && observation.Destination.Port() == stunPrimaryPort {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("STUN responder did not observe %s: %+v", endpoint, observations)
		}
	}
}

func assertNATPacketOracle(t *testing.T, lab *lab, topology iceNATTopology, scenario iceNATScenario) {
	t.Helper()
	for _, item := range []struct {
		nat       bool
		namespace string
		mapping   mappingPolicy
	}{
		{scenario.aNAT, chooseNamespace(scenario.aNAT, topology.ARouter, topology.ANamespace), scenario.aMapping},
		{scenario.bNAT, chooseNamespace(scenario.bNAT, topology.BRouter, topology.BNamespace), scenario.bMapping},
	} {
		if !item.nat {
			rules := lab.inNamespace(item.namespace, "nft", "-j", "list", "ruleset")
			good, goodOK := counterForComment(rules, "m5c2-public-mux-port")
			bad, badOK := counterForComment(rules, "m5c2-public-bad-port")
			if !goodOK || good == 0 || !badOK || bad != 0 {
				t.Fatalf("public peer %s source-port oracle good=%d/%v bad=%d/%v", item.namespace, good, goodOK, bad, badOK)
			}
			continue
		}
		rules := lab.inNamespace(item.namespace, "nft", "-j", "list", "ruleset")
		for _, comment := range []string{"m5c2-stun-snat", "m5c2-peer-snat", "m5c2-internal-stun", "m5c2-internal-peer"} {
			packets, ok := counterForComment(rules, comment)
			if !ok || packets == 0 {
				t.Fatalf("router %s missing %s evidence: %d/%v", item.namespace, comment, packets, ok)
			}
		}
		if packets, ok := counterForComment(rules, "m5c2-internal-bad-port"); !ok || packets != 0 {
			t.Fatalf("router %s observed wrong internal source port: %d/%v", item.namespace, packets, ok)
		}
	}
}

func chooseNamespace(condition bool, yes, no string) string {
	if condition {
		return yes
	}
	return no
}

func assertExpectedAPDMFailureEvidence(t *testing.T, lab *lab, topology iceNATTopology) {
	t.Helper()
	for _, namespace := range []string{topology.ARouter, topology.BRouter} {
		rules := lab.inNamespace(namespace, "nft", "-j", "list", "ruleset")
		peer, peerOK := counterForComment(rules, "m5c2-peer-snat")
		dropped, dropOK := counterForComment(rules, "m5c2-inbound-apdf-drop")
		if !peerOK || peer == 0 || !dropOK || dropped == 0 {
			t.Fatalf("APDM/APDF limitation lacks mapping/drop evidence in %s: peer=%d/%v drop=%d/%v", namespace, peer, peerOK, dropped, dropOK)
		}
	}
}

func assertNoPrivateCrossRoute(t *testing.T, lab *lab, topology iceNATTopology) {
	t.Helper()
	if topology.ANAT && topology.BNAT {
		output := lab.inNamespace(topology.ANamespace, "ip", "route", "get", topology.BData.String())
		if output == "" || !strings.Contains(output, "via 10.51.1.1") {
			t.Fatalf("A private destination is not isolated behind its NAT route: %q", output)
		}
		output = lab.inNamespace(topology.BNamespace, "ip", "route", "get", topology.AData.String())
		if output == "" || !strings.Contains(output, "via 10.51.2.1") {
			t.Fatalf("B private destination is not isolated behind its NAT route: %q", output)
		}
	}
}

func peerNATPorts(nat bool, mapping mappingPolicy, stunPort, peerPort int) (int, int) {
	if !nat {
		return clientMuxPort, clientMuxPort
	}
	if mapping == mappingEIM {
		return stunPort, stunPort
	}
	return stunPort, peerPort
}

func peerExternal(data, external netip.Addr, nat bool) netip.Addr {
	if nat {
		return external
	}
	return data
}

func expectedNATSTUNEndpoint(data, external netip.Addr, nat bool, port uint16) netip.AddrPort {
	if nat {
		return netip.AddrPortFrom(external, port)
	}
	return netip.AddrPortFrom(data, clientMuxPort)
}

func publicICEOracleRules(source, stunServer, peer netip.Addr) string {
	return fmt.Sprintf(`table ip hydra_m5c2_public {
 chain output {
  type filter hook output priority filter; policy accept;
  ip saddr %s ip daddr { %s, %s } udp sport != %d counter comment "m5c2-public-bad-port"
  ip saddr %s ip daddr { %s, %s } udp sport %d counter comment "m5c2-public-mux-port"
 }
}
`, source, stunServer, peer, clientMuxPort, source, stunServer, peer, clientMuxPort)
}
