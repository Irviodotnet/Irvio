// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"net/netip"
	"testing"
	"time"
)

func TestM6_ControlledNATEncryptedDataPlane(t *testing.T) {
	requirePrivileged(t)
	scenarios := []iceNATScenario{
		{name: "public-to-eim-apdf", bNAT: true, bMapping: mappingEIM, bFiltering: filteringAPDF, wantConnected: true},
		{
			name: "eim-apdf-to-eim-apdf", aNAT: true, bNAT: true,
			aMapping: mappingEIM, bMapping: mappingEIM,
			aFiltering: filteringAPDF, bFiltering: filteringAPDF, wantConnected: true,
		},
		{
			name: "prflx-selected-endpoint", aNAT: true, bNAT: true,
			aMapping: mappingEIM, bMapping: mappingAPDM,
			aFiltering: filteringEIF, bFiltering: filteringAPDF, wantConnected: true,
			wantAPeerReflexiveForBMapping: true,
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			runM6ControlledNAT(t, scenario)
		})
	}
}

func runM6ControlledNAT(t *testing.T, scenario iceNATScenario) {
	t.Helper()
	lab := newLab(t, "m6-"+scenario.name)
	topology := lab.buildICENATTopology(scenario.aNAT, scenario.bNAT)
	server := lab.startResponder("m6-stun", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
	installICENATScenarioRules(lab, topology, scenario)
	stunServer := endpointValue(topology.ServerA1, stunPrimaryPort)
	responder, responderReady := startM6NATHelper(
		t, lab, "responder", topology.BNamespace, topology.BData, topology.BControl, stunServer, "", 20*time.Second,
	)
	remote := selectSignalAddress(t, responderReady.ListenAddresses, "ipv4")
	initiator, initiatorReady := startM6NATHelper(
		t, lab, "initiator", topology.ANamespace, topology.AData, topology.AControl, stunServer, remote, 20*time.Second,
	)

	expectedA := expectedNATSTUNEndpoint(topology.AData, topology.AExternal, scenario.aNAT, m5c2AStunPort)
	expectedB := expectedNATSTUNEndpoint(topology.BData, topology.BExternal, scenario.bNAT, m5c2BStunPort)
	assertSTUNReadiness(t, initiatorReady, expectedA, topology.AData)
	assertSTUNReadiness(t, responderReady, expectedB, topology.BData)
	waitForICEReady(t, initiator, responder)
	assertPreparedCandidates(t, readICEReady(t, initiator), expectedA, topology.AData, topology.AControl, scenario.aNAT)
	assertPreparedCandidates(t, readICEReady(t, responder), expectedB, topology.BData, topology.BControl, scenario.bNAT)
	if err := touch(initiator.connectStartPath); err != nil {
		t.Fatal(err)
	}
	if err := touch(responder.connectStartPath); err != nil {
		t.Fatal(err)
	}
	waitForAWGReady(t, initiator, responder)
	if err := touch(initiator.awgStartPath); err != nil {
		t.Fatal(err)
	}
	if err := touch(responder.awgStartPath); err != nil {
		t.Fatal(err)
	}
	initiatorResult := waitNATConnectResult(t, initiator)
	responderResult := waitNATConnectResult(t, responder)
	assertNATConnected(t, initiatorResult, responderResult, topology, expectedA, expectedB, scenario)
	assertM6AWGResult(t, "initiator", initiatorReady, initiatorResult)
	assertM6AWGResult(t, "responder", responderReady, responderResult)
	if scenario.wantAPeerReflexiveForBMapping {
		wantPRFLX := netip.AddrPortFrom(topology.BExternal, m5c2BPeerPort)
		if initiatorResult.AWGSelectedRemote != wantPRFLX.String() || initiatorResult.AWGSelectedRemote == initiatorResult.ObservedSrflxIPv4 {
			t.Fatalf("AWG did not use the PRFLX endpoint: result=%+v want=%s", initiatorResult, wantPRFLX)
		}
	}
	if count, _ := namespaceUDPSockets(t, lab, topology.ANamespace); count != 1 {
		t.Fatalf("A has %d UDP4 sockets during AWG, want one", count)
	}
	if count, _ := namespaceUDPSockets(t, lab, topology.BNamespace); count != 1 {
		t.Fatalf("B has %d UDP4 sockets during AWG, want one", count)
	}
	assertSTUNOracle(t, server.observationPath, expectedA, expectedB)
	assertNATPacketOracle(t, lab, topology, scenario)
	assertNoPrivateCrossRoute(t, lab, topology)
	if err := touch(initiator.releasePath); err != nil {
		t.Fatal(err)
	}
	if err := touch(responder.releasePath); err != nil {
		t.Fatal(err)
	}
	waitNATChildren(t, initiator, responder, true)
	assertPostCloseLifecycle(t, initiator)
	assertPostCloseLifecycle(t, responder)
	lab.captureNamespace(topology.ANamespace, "m6-a", false)
	lab.captureNamespace(topology.BNamespace, "m6-b", false)
	if topology.ANAT {
		lab.captureNamespace(topology.ARouter, "m6-router-a", true)
	}
	if topology.BNAT {
		lab.captureNamespace(topology.BRouter, "m6-router-b", true)
	}
}

func assertM6AWGResult(t *testing.T, side string, ready, result iceSignalReport) {
	t.Helper()
	wantInode := ready.UDP4SocketInodeReady
	wantBeforeSTUN := ready.UDP4SocketInodeBeforeSTUN
	if result.SelectedNetwork == 2 {
		wantInode = ready.UDP6SocketInodeReady
		wantBeforeSTUN = ready.UDP6SocketInodeBeforeSTUN
	}
	if !result.AWGHandshake || !result.AWGInnerPacket || result.AWGSelectedRemote != result.SelectedRemote ||
		result.AWGSocketInode == "" || result.AWGSocketInode != wantInode || wantBeforeSTUN != wantInode ||
		result.AWGSendPackets == 0 || result.AWGReceivePackets == 0 || result.AWGPublicFingerprint == "" ||
		result.ApplicationPackets != 0 || result.ApplicationBytes != 0 {
		t.Fatalf("%s lacks M6 AWG/same-socket/zero-ICE-data evidence: %+v", side, result)
	}
}
