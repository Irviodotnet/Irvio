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
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type m8Topology struct {
	ClientNamespace string
	WAN1Namespace   string
	WAN2Namespace   string
	ClientWAN1If    string
	ClientWAN2If    string
	WAN1If          string
	WAN2If          string
	ClientWAN14     netip.Addr
	ClientWAN16     netip.Addr
	ClientWAN24     netip.Addr
	ClientWAN26     netip.Addr
	WAN14           netip.Addr
	WAN16           netip.Addr
	WAN24           netip.Addr
	WAN26           netip.Addr
}

type m8ProtectionReport struct {
	PID              int `json:"pid"`
	Cycles           int `json:"cycles"`
	FDBefore         int `json:"fd_before"`
	FDAfter          int `json:"fd_after"`
	NetlinkBefore    int `json:"netlink_before"`
	NetlinkAfter     int `json:"netlink_after"`
	GoroutinesBefore int `json:"goroutines_before"`
	GoroutinesAfter  int `json:"goroutines_after"`
	Protected        struct {
		Adopted bool  `json:"adopted"`
		State   uint8 `json:"state"`
	} `json:"protected_mode"`
}

type m8ControlReport struct {
	PeerID           string   `json:"peer_id"`
	ListenAddresses  []string `json:"listen_addresses"`
	Authenticated    bool     `json:"authenticated"`
	MuxPort          uint16   `json:"mux_port"`
	SocketMark       uint32   `json:"socket_mark"`
	Remote           string   `json:"remote"`
	AttemptedPackets uint32   `json:"attempted_packets"`
}

func TestM8_ProtectedModeKernelPolicy(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m8-policy")
	topology := buildM8Topology(lab)
	server1, release1, observations1 := startM8Internet(t, lab, "wan1", topology.WAN1Namespace, topology.WAN14, topology.WAN16)
	server2, release2, _ := startM8Internet(t, lab, "wan2", topology.WAN2Namespace, topology.WAN24, topology.WAN26)
	policy, policyRelease, policyResult := startM8Policy(t, lab, topology.ClientNamespace, "policy")

	before := countM8Observations(t, observations1)
	for _, item := range []struct {
		kind   string
		target netip.AddrPort
		iface  string
	}{
		{"http", netip.AddrPortFrom(topology.WAN14, 18080), topology.ClientWAN1If},
		{"udp", netip.AddrPortFrom(topology.WAN16, 18081), topology.ClientWAN1If},
		{"dns-udp", netip.AddrPortFrom(topology.WAN14, 1053), topology.ClientWAN1If},
		{"dns-tcp", netip.AddrPortFrom(topology.WAN16, 1053), topology.ClientWAN1If},
	} {
		runM8ProbeExpect(t, lab, topology.ClientNamespace, item.kind, item.target, item.iface, 0, false)
	}
	if after := countM8Observations(t, observations1); after != before {
		t.Fatalf("ordinary physical-WAN traffic leaked: before=%d after=%d", before, after)
	}
	runM8CommandExpectFailure(t, "ip", "netns", "exec", topology.ClientNamespace, "ping", "-c", "1", "-W", "1", "-I", topology.ClientWAN1If, topology.WAN14.String())
	runM8CommandExpectFailure(t, "ip", "netns", "exec", topology.ClientNamespace, "ping", "-6", "-c", "1", "-W", "1", "-I", topology.ClientWAN1If, topology.WAN16.String())

	for _, item := range []struct {
		target netip.AddrPort
		iface  string
	}{
		{netip.AddrPortFrom(topology.WAN14, 18080), topology.ClientWAN1If},
		{netip.AddrPortFrom(topology.WAN24, 18080), topology.ClientWAN2If},
	} {
		runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", item.target, item.iface, m7OuterMark, true)
	}
	assertM8LiteralControl(t, lab, topology)
	assertM8MuxControl(t, lab, topology)
	assertM8UnauthorizedMark(t, lab, topology)

	rules := lab.inNamespace(topology.ClientNamespace, "nft", "-j", "list", "table", "inet", "hydra_protect")
	requireCounter(t, rules, "drop-ordinary", 6)
	requireCounter(t, rules, "accept-bypass", 4)
	if !strings.Contains(rules, "accept-dhcp4") || !strings.Contains(rules, "accept-dhcp6") ||
		!strings.Contains(rules, "accept-icmpv6-ns") || strings.Contains(rules, "accept-dns") {
		t.Fatalf("unexpected maintenance or DNS exception policy: %s", rules)
	}

	if err := touch(policyRelease); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, policy)
	var disconnected m8ProtectionReport
	if err := readJSON(policyResult, &disconnected); err != nil {
		t.Fatal(err)
	}
	if disconnected.Protected.State != 0 {
		t.Fatalf("explicit Disconnect did not reach VPN_OFF: %+v", disconnected)
	}
	if _, err := runM8Command("ip", "netns", "exec", topology.ClientNamespace, "nft", "list", "table", "inet", "hydra_protect"); err == nil {
		t.Fatal("Irvio protection remained after explicit Disconnect")
	}
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.WAN14, 18080), topology.ClientWAN1If, 0, true)

	for _, path := range []string{release1, release2} {
		if err := touch(path); err != nil {
			t.Fatal(err)
		}
	}
	waitM8Child(t, server1)
	waitM8Child(t, server2)
}

func TestM8_HealthyVPNAndExitDeathRemainFailClosed(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")
	lab := newLab(t, "m8-exit-death")
	topology := buildM7Topology(lab)
	server, serverRelease, observations := startM7InternetServer(t, lab, topology)
	control, controlRelease, _ := startM8Internet(t, lab, "m8-recovery", topology.ExitNamespace, topology.ExitOuter4, topology.ExitOuter6)
	test := struct {
		name                string
		outer4, outer6      bool
		protect4, protect6  bool
		reject4, reject6    bool
		probe4, probe6, dns bool
	}{name: "m8-dual", outer4: true, protect4: true, protect6: true, probe4: true, probe6: true, dns: true}

	exit, exitReady := startM7Peer(t, lab, topology, "responder", test, "")
	remote := selectSignalAddress(t, exitReady.ListenAddresses, "ipv4")
	client, clientReady := startM7Peer(t, lab, topology, "initiator", test, remote, "--m8-protect")
	if !clientReady.ProtectedMode {
		t.Fatalf("M8 protection was not committed before transport readiness: %+v", clientReady)
	}
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
	assertM7Ownership(t, clientReady, clientResult, exitReady, exitResult)
	if !clientResult.ProtectedMode {
		t.Fatal("healthy encrypted tunnel lost ProtectedMode")
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet6, 18080)), topology.ExitWAN6, netip.MustParseAddr("fd77::2"))
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080), topology.ClientOuterIf, 0, false)
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet6, 18080), topology.ClientOuterIf, 0, false)

	before := countM8Observations(t, observations)
	leak4, checkpoint4, continue4, result4 := startM8LeakProbe(t, lab, topology.ClientNamespace, "leak4", topology.ClientOuterIf, endpointValue(topology.Internet4, 18081))
	leak6, checkpoint6, continue6, result6 := startM8LeakProbe(t, lab, topology.ClientNamespace, "leak6", topology.ClientOuterIf, endpointValue(topology.Internet6, 18081))
	waitM8File(t, leak4, checkpoint4)
	waitM8File(t, leak6, checkpoint6)
	if err := syscall.Kill(-exit.child.command.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitM8ChildFailure(t, exit.child)
	for _, path := range []string{continue4, continue6} {
		if err := touch(path); err != nil {
			t.Fatal(err)
		}
	}
	waitM8Child(t, leak4)
	waitM8Child(t, leak6)
	for _, path := range []string{result4, result6} {
		var probe m8ControlReport
		if err := readJSON(path, &probe); err != nil {
			t.Fatal(err)
		}
		if probe.AttemptedPackets != 64 {
			t.Fatalf("continuous fault probe did not cross barrier: %+v", probe)
		}
	}
	if after := countM8Observations(t, observations); after != before {
		t.Fatalf("ordinary packets escaped during Exit death: before=%d after=%d", before, after)
	}
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpointValue(topology.ExitOuter4, 18080), topology.ClientOuterIf, m7OuterMark, true)
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpointValue(topology.ExitOuter6, 18080), topology.ClientOuterIf, m7OuterMark, true)

	if err := touch(client.releasePath); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, client.child)
	for _, path := range []string{serverRelease, controlRelease} {
		if err := touch(path); err != nil {
			t.Fatal(err)
		}
	}
	waitM8Child(t, server)
	waitM8Child(t, control)
}

func TestM8_RouteAndTunnelLossRemainFailClosed(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m8-route-tunnel-loss")
	topology := buildM7Topology(lab)
	server, serverRelease, _ := startM7InternetServer(t, lab, topology)
	control, controlRelease, controlObservations := startM8Internet(t, lab, "m8-recovery", topology.ExitNamespace, topology.ExitOuter4, topology.ExitOuter6)
	test := struct {
		name                string
		outer4, outer6      bool
		protect4, protect6  bool
		reject4, reject6    bool
		probe4, probe6, dns bool
	}{name: "m8-route-tunnel-loss", outer4: true, protect4: true, protect6: true, probe4: true, probe6: true}

	exit, exitReady := startM7Peer(t, lab, topology, "responder", test, "")
	remote := selectSignalAddress(t, exitReady.ListenAddresses, "ipv4")
	client, clientReady := startM7Peer(t, lab, topology, "initiator", test, remote, "--m8-protect")
	if !clientReady.ProtectedMode {
		t.Fatal("fault client did not commit ProtectedMode")
	}
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
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	lab.inNamespace(topology.ClientNamespace, "ip", "-4", "route", "flush", "table", "51820")
	lab.inNamespace(topology.ClientNamespace, "ip", "-6", "route", "flush", "table", "51820")
	before := countM8Observations(t, controlObservations)
	for _, endpoint := range []netip.AddrPort{endpointValue(topology.ExitOuter4, 18080), endpointValue(topology.ExitOuter6, 18080)} {
		runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpoint, topology.ClientOuterIf, 0, false)
	}
	if after := countM8Observations(t, controlObservations); after != before {
		t.Fatalf("ordinary traffic escaped after hydra_out loss: before=%d after=%d", before, after)
	}
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpointValue(topology.ExitOuter4, 18080), topology.ClientOuterIf, m7OuterMark, true)

	if err := syscall.Kill(-client.child.command.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitM8ChildFailure(t, client.child)
	if _, err := runM8Command("ip", "netns", "exec", topology.ClientNamespace, "ip", "link", "show", "hydra0"); err == nil {
		t.Fatal("hydra0 survived destruction of its owning AWG process")
	}
	if _, err := runM8Command("ip", "netns", "exec", topology.ClientNamespace, "nft", "list", "table", "inet", "hydra_protect"); err != nil {
		t.Fatalf("AWG/TUN loss removed fail-closed protection: %v", err)
	}
	before = countM8Observations(t, controlObservations)
	for _, endpoint := range []netip.AddrPort{endpointValue(topology.ExitOuter4, 18080), endpointValue(topology.ExitOuter6, 18080)} {
		runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpoint, topology.ClientOuterIf, 0, false)
	}
	if after := countM8Observations(t, controlObservations); after != before {
		t.Fatalf("ordinary traffic escaped after AWG/TUN loss: before=%d after=%d", before, after)
	}
	for _, endpoint := range []netip.AddrPort{endpointValue(topology.ExitOuter4, 18080), endpointValue(topology.ExitOuter6, 18080)} {
		runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpoint, topology.ClientOuterIf, m7OuterMark, true)
	}

	for _, path := range []string{exit.releasePath, serverRelease, controlRelease} {
		if err := touch(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, process := range []*childProcess{exit.child, server, control} {
		waitM8Child(t, process)
	}
}

func TestM8_KernelSetupAndTeardownFailuresAreSafe(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m8-policy-failures")
	topology := buildM8Topology(lab)
	server, serverRelease, _ := startM8Internet(t, lab, "policy-failure-wan", topology.WAN1Namespace, topology.WAN14, topology.WAN16)

	lab.inNamespace(topology.ClientNamespace, "nft", "add", "table", "inet", "hydra_protect")
	lab.inNamespace(topology.ClientNamespace, "nft", "add", "chain", "inet", "hydra_protect", "administrator")
	ready := filepath.Join(lab.artifactDir, "conflict-ready.json")
	release := filepath.Join(lab.artifactDir, "conflict-release")
	conflict := lab.startChild("m8-policy-conflict", topology.ClientNamespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
		"--mode", "policy", "--ready", ready, "--release", release)
	waitM8ChildFailure(t, conflict)
	rules := lab.inNamespace(topology.ClientNamespace, "nft", "list", "table", "inet", "hydra_protect")
	if !strings.Contains(rules, "administrator") || strings.Contains(rules, "hydra-protected-mode-v1") {
		t.Fatalf("setup failure mutated unrelated firewall state: %s", rules)
	}
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.WAN14, 18080), topology.ClientWAN1If, 0, true)

	lab.inNamespace(topology.ClientNamespace, "nft", "delete", "table", "inet", "hydra_protect")
	policy, policyRelease, _ := startM8Policy(t, lab, topology.ClientNamespace, "teardown-failure")
	lab.inNamespace(topology.ClientNamespace, "nft", "flush", "chain", "inet", "hydra_protect", "output_v1")
	lab.inNamespace(topology.ClientNamespace, "nft", "add", "rule", "inet", "hydra_protect", "output_v1", "counter", "comment", "administrator-state")
	if err := touch(policyRelease); err != nil {
		t.Fatal(err)
	}
	waitM8ChildFailure(t, policy)
	rules = lab.inNamespace(topology.ClientNamespace, "nft", "list", "table", "inet", "hydra_protect")
	if !strings.Contains(rules, "administrator-state") {
		t.Fatalf("failed teardown deleted conflicting kernel state: %s", rules)
	}
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.WAN14, 18080), topology.ClientWAN1If, 0, false)

	if err := touch(serverRelease); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, server)
}

func TestM8_SamePeerReconnectGapRemainsFailClosed(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m8-reconnect")
	topology := buildM7Topology(lab)
	lab.installNFT(topology.ClientNamespace, "m8-reconnect-client", reconnectBlockRules())
	lab.installNFT(topology.ExitNamespace, "m8-reconnect-exit", reconnectBlockRules())
	server, serverRelease, _ := startM7InternetServer(t, lab, topology)
	control, controlRelease, _ := startM8Internet(t, lab, "m8-recovery", topology.ExitNamespace, topology.ExitOuter4, topology.ExitOuter6)

	exit, exitReady := startM7ReconnectPeer(t, lab, topology, "responder", "")
	remote := selectSignalAddress(t, exitReady.ListenAddresses, "ipv4")
	client, clientReady := startM7ReconnectPeer(t, lab, topology, "initiator", remote, "--m8-protect")
	if !clientReady.ProtectedMode {
		t.Fatal("reconnect client did not commit ProtectedMode before ICE")
	}
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
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080), topology.ClientOuterIf, 0, false)
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet6, 18080), topology.ClientOuterIf, 0, false)
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpointValue(topology.ExitOuter4, 18080), topology.ClientOuterIf, m7OuterMark, true)
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
	if !secondClient.ProtectedMode {
		t.Fatal("same-peer recovery disabled ProtectedMode")
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	for _, path := range []string{client.releasePath, exit.releasePath, serverRelease, controlRelease} {
		if err := touch(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, process := range []*childProcess{client.child, exit.child, server, control} {
		waitM8Child(t, process)
	}
}

func TestM8_NetworkGenerationMigrationRemainsFailClosed(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m8-migration")
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
	server, serverRelease, observations := startM7InternetServer(t, lab, topology)

	exit, exitReady := startM7MigrationPeer(t, lab, topology, "responder", topology.ExitOuter4, exitControl, "")
	remote := selectSignalAddress(t, exitReady.ListenAddresses, "ipv4")
	client, clientReady := startM7MigrationPeer(t, lab, topology, "initiator", topology.ClientOuter4, clientControl, remote, "--m8-protect")
	if !clientReady.ProtectedMode {
		t.Fatal("migration client did not commit ProtectedMode")
	}
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

	before := countM8Observations(t, observations)
	leak, checkpoint, continueFile, result := startM8LeakProbe(t, lab, topology.ClientNamespace, "migration-leak", topology.ClientOuterIf, endpointValue(topology.Internet4, 18081))
	waitM8File(t, leak, checkpoint)
	newAddress := netip.MustParseAddr("198.18.77.3")
	runIPBatch(t, lab, topology.ClientNamespace, "m8-network-migration", "addr del "+topology.ClientOuter4.String()+"/24 dev "+topology.ClientOuterIf+"\n"+
		"addr add "+newAddress.String()+"/24 dev "+topology.ClientOuterIf+"\n"+
		"route replace default via "+topology.ExitOuter4.String()+" dev "+topology.ClientOuterIf+" src "+newAddress.String()+"\n")
	secondClient := waitMigrationReport(t, client.child, client.resultPath)
	secondExit := waitMigrationReport(t, exit.child, exit.resultPath)
	assertConnectedReports(t, secondClient, secondExit, true, false)
	if secondClient.NetworkGeneration != 2 || !secondClient.ProtectedMode ||
		secondClient.PreviousSessionID != firstClient.SessionID || secondExit.PreviousSessionID != firstExit.SessionID ||
		secondClient.AWGTUNIfIndex != firstClient.AWGTUNIfIndex || secondClient.UDP4SocketInodeAfter == firstClient.UDP4SocketInodeAfter {
		t.Fatalf("protected migration ownership mismatch: C1=%+v C2=%+v E1=%+v E2=%+v", firstClient, secondClient, firstExit, secondExit)
	}
	if err := touch(continueFile); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, leak)
	var probe m8ControlReport
	if err := readJSON(result, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.AttemptedPackets != 64 {
		t.Fatalf("migration leak probe=%+v", probe)
	}
	if after := countM8Observations(t, observations); after != before {
		t.Fatalf("ordinary packets escaped network migration: before=%d after=%d", before, after)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	for _, path := range []string{client.releasePath, exit.releasePath, serverRelease} {
		if err := touch(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, process := range []*childProcess{client.child, exit.child, server} {
		waitM8Child(t, process)
	}
}

func TestM8_CrashAdoptionAndExplicitDisconnect(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m8-crash")
	topology := buildM8Topology(lab)
	server, serverRelease, observations := startM8Internet(t, lab, "crash-wan", topology.WAN1Namespace, topology.WAN14, topology.WAN16)
	runtimeDir := lab.tempDir("m8-hydrad")
	dataDir := filepath.Join(runtimeDir, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	first := lab.startChild("m8-hydrad-first", topology.ClientNamespace, os.Getenv("HYDRA_M4B_HYDRAD"),
		"--data-dir", dataDir, "--listen-port", "0", "--print-node-info", "--protected-mode")
	firstInfo := waitNodeInfo(t, lab, first, "m8-hydrad-first.stdout.log", 1)
	before := countM8Observations(t, observations)
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.WAN14, 18080), topology.ClientWAN1If, 0, false)
	if err := syscall.Kill(-first.command.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitM8ChildFailure(t, first)
	if _, err := runM8Command("ip", "netns", "exec", topology.ClientNamespace, "nft", "list", "table", "inet", "hydra_protect"); err != nil {
		t.Fatalf("SIGKILL of hydrad removed fail-closed kernel state: %v", err)
	}
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.WAN14, 18080), topology.ClientWAN1If, 0, false)
	if after := countM8Observations(t, observations); after != before {
		t.Fatalf("ordinary traffic leaked after hydrad SIGKILL: before=%d after=%d", before, after)
	}
	second := lab.startChild("m8-hydrad-adopt", topology.ClientNamespace, os.Getenv("HYDRA_M4B_HYDRAD"),
		"--data-dir", dataDir, "--listen-port", "0", "--print-node-info", "--protected-mode")
	secondInfo := waitNodeInfo(t, lab, second, "m8-hydrad-adopt.stdout.log", 1)
	if secondInfo.PeerID != firstInfo.PeerID {
		t.Fatalf("hydrad identity changed across protection adoption: first=%s second=%s", firstInfo.PeerID, secondInfo.PeerID)
	}
	if !m8LogAdopted(filepath.Join(lab.artifactDir, "m8-hydrad-adopt.stderr.log")) {
		t.Fatal("restarted hydrad did not report adoption of the persistent policy")
	}
	if err := second.stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := runM8Command("ip", "netns", "exec", topology.ClientNamespace, "nft", "list", "table", "inet", "hydra_protect"); err == nil {
		t.Fatal("adopted protection was not removed by graceful hydrad shutdown")
	}
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.WAN14, 18080), topology.ClientWAN1If, 0, true)
	if err := touch(serverRelease); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, server)
}

func TestM8_KernelPolicyLifecycle(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")
	lab := newLab(t, "m8-lifecycle")
	topology := buildM8Topology(lab)
	policy, release, _ := startM8Policy(t, lab, topology.ClientNamespace, "lifecycle")
	if err := touch(release); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, policy)
}

func buildM8Topology(lab *lab) m8Topology {
	topology := m8Topology{
		ClientNamespace: lab.namespace("client"), WAN1Namespace: lab.namespace("wan1"), WAN2Namespace: lab.namespace("wan2"),
		ClientWAN1If: lab.interfaceName('a'), WAN1If: lab.interfaceName('b'),
		ClientWAN2If: lab.interfaceName('c'), WAN2If: lab.interfaceName('d'),
		ClientWAN14: netip.MustParseAddr("203.0.113.10"), WAN14: netip.MustParseAddr("203.0.113.20"),
		ClientWAN16: netip.MustParseAddr("2001:db8:88::10"), WAN16: netip.MustParseAddr("2001:db8:88::20"),
		ClientWAN24: netip.MustParseAddr("198.51.100.10"), WAN24: netip.MustParseAddr("198.51.100.20"),
		ClientWAN26: netip.MustParseAddr("2001:db8:89::10"), WAN26: netip.MustParseAddr("2001:db8:89::20"),
	}
	lab.createVeth(topology.ClientNamespace, topology.ClientWAN1If, topology.WAN1Namespace, topology.WAN1If)
	lab.createVeth(topology.ClientNamespace, topology.ClientWAN2If, topology.WAN2Namespace, topology.WAN2If)
	for _, namespace := range []string{topology.ClientNamespace, topology.WAN1Namespace, topology.WAN2Namespace} {
		lab.inNamespace(namespace, "ip", "link", "set", "lo", "up")
	}
	for _, item := range []struct {
		namespace, iface string
		ipv4, ipv6       netip.Addr
	}{
		{topology.ClientNamespace, topology.ClientWAN1If, topology.ClientWAN14, topology.ClientWAN16},
		{topology.WAN1Namespace, topology.WAN1If, topology.WAN14, topology.WAN16},
		{topology.ClientNamespace, topology.ClientWAN2If, topology.ClientWAN24, topology.ClientWAN26},
		{topology.WAN2Namespace, topology.WAN2If, topology.WAN24, topology.WAN26},
	} {
		lab.inNamespace(item.namespace, "ip", "link", "set", item.iface, "up")
		lab.inNamespace(item.namespace, "ip", "addr", "add", item.ipv4.String()+"/24", "dev", item.iface)
		lab.inNamespace(item.namespace, "ip", "-6", "addr", "add", item.ipv6.String()+"/64", "dev", item.iface, "nodad")
	}
	lab.recordTopology(topology)
	return topology
}

func startM8Internet(t *testing.T, lab *lab, label, namespace string, ipv4, ipv6 netip.Addr) (*childProcess, string, string) {
	t.Helper()
	ready := filepath.Join(lab.artifactDir, label+"-internet-ready")
	release := filepath.Join(lab.artifactDir, label+"-internet-release")
	observations := filepath.Join(lab.artifactDir, label+"-observations.jsonl")
	child := lab.startChild(label+"-internet", namespace, os.Getenv("HYDRA_M7_INTERNET"),
		"--mode", "server", "--listen4", ipv4.String(), "--listen6", ipv6.String(),
		"--ready", ready, "--release", release, "--observations", observations)
	waitM8File(t, child, ready)
	return child, release, observations
}

func startM8Policy(t *testing.T, lab *lab, namespace, label string) (*childProcess, string, string) {
	t.Helper()
	ready := filepath.Join(lab.artifactDir, label+"-ready.json")
	release := filepath.Join(lab.artifactDir, label+"-release")
	result := filepath.Join(lab.artifactDir, label+"-result.json")
	child := lab.startChild(label, namespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
		"--mode", "policy", "--ready", ready, "--release", release, "--result", result)
	waitM8File(t, child, ready)
	var value m8ProtectionReport
	if err := readJSON(ready, &value); err != nil {
		t.Fatal(err)
	}
	if value.Protected.State != 2 {
		t.Fatalf("policy did not enter PROTECTED_CONNECTING: %+v", value)
	}
	return child, release, result
}

func startM8LeakProbe(t *testing.T, lab *lab, namespace, label, iface string, target netip.AddrPort) (*childProcess, string, string, string) {
	t.Helper()
	ready := filepath.Join(lab.artifactDir, label+"-ready.json")
	checkpoint := filepath.Join(lab.artifactDir, label+"-checkpoint.json")
	continueFile := filepath.Join(lab.artifactDir, label+"-continue")
	result := filepath.Join(lab.artifactDir, label+"-result.json")
	child := lab.startChild(label, namespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
		"--mode", "leak-probe", "--remote", target.String(), "--bind-device", iface,
		"--ready", ready, "--checkpoint", checkpoint, "--continue", continueFile, "--result", result)
	waitM8File(t, child, ready)
	return child, checkpoint, continueFile, result
}

func runM8ProbeExpect(t *testing.T, lab *lab, namespace, kind string, target netip.AddrPort, iface string, mark uint32, success bool) {
	t.Helper()
	result := filepath.Join(lab.artifactDir, "m8-probe-"+strconv.FormatUint(m7ProbeCounter.Add(1), 10)+".json")
	args := []string{"netns", "exec", namespace, os.Getenv("HYDRA_M7_INTERNET"),
		"--mode", "probe", "--kind", kind, "--target", target.String(), "--result", result, "--bind-device", iface}
	if mark != 0 {
		args = append(args, "--socket-mark", strconv.FormatUint(uint64(mark), 10))
	}
	_, err := runM8Command("ip", args...)
	if success && err != nil {
		t.Fatalf("%s probe to %s failed: %v", kind, target, err)
	}
	if !success && err == nil {
		t.Fatalf("%s probe to %s bypassed ProtectedMode", kind, target)
	}
}

func assertM8LiteralControl(t *testing.T, lab *lab, topology m8Topology) {
	t.Helper()
	ready := filepath.Join(lab.artifactDir, "m8-libp2p-ready.json")
	release := filepath.Join(lab.artifactDir, "m8-libp2p-release")
	server := lab.startChild("m8-libp2p-server", topology.WAN1Namespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
		"--mode", "libp2p-server", "--bind4", topology.WAN14.String(), "--bind6", topology.WAN16.String(),
		"--ready", ready, "--release", release)
	waitM8File(t, server, ready)
	var listening m8ControlReport
	if err := readJSON(ready, &listening); err != nil {
		t.Fatal(err)
	}
	for index, remote := range listening.ListenAddresses {
		result := filepath.Join(lab.artifactDir, "m8-libp2p-"+strconv.Itoa(index)+".json")
		child := lab.startChild("m8-libp2p-client-"+strconv.Itoa(index), topology.ClientNamespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
			"--mode", "libp2p-client", "--remote", remote, "--result", result)
		waitM8Child(t, child)
		var connected m8ControlReport
		if err := readJSON(result, &connected); err != nil {
			t.Fatal(err)
		}
		if !connected.Authenticated || connected.SocketMark != m7OuterMark {
			t.Fatalf("literal-IP authenticated recovery failed: %+v", connected)
		}
	}
	if err := touch(release); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, server)
}

func assertM8MuxControl(t *testing.T, lab *lab, topology m8Topology) {
	t.Helper()
	for _, item := range []struct {
		name, bindFlag string
		bind           netip.Addr
		remote         netip.AddrPort
	}{
		{"ipv4", "--bind4", topology.ClientWAN14, netip.AddrPortFrom(topology.WAN14, 18081)},
		{"ipv6", "--bind6", topology.ClientWAN16, netip.AddrPortFrom(topology.WAN16, 18081)},
	} {
		result := filepath.Join(lab.artifactDir, "m8-mux-"+item.name+".json")
		child := lab.startChild("m8-mux-"+item.name, topology.ClientNamespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
			"--mode", "mux-probe", item.bindFlag, item.bind.String(), "--remote", item.remote.String(), "--result", result)
		waitM8Child(t, child)
		var value m8ControlReport
		if err := readJSON(result, &value); err != nil {
			t.Fatal(err)
		}
		if value.MuxPort == 0 || value.SocketMark != m7OuterMark || value.Remote != item.remote.String() {
			t.Fatalf("marked IrvioSocketMux %s recovery failed: %+v", item.name, value)
		}
	}
}

func assertM8UnauthorizedMark(t *testing.T, lab *lab, topology m8Topology) {
	t.Helper()
	result := filepath.Join(lab.artifactDir, "unauthorized.json")
	output, err := runM8Command("ip", "netns", "exec", topology.ClientNamespace,
		"setpriv", "--reuid=65534", "--regid=65534", "--clear-groups", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all",
		os.Getenv("HYDRA_M7_INTERNET"), "--mode", "probe", "--kind", "http",
		"--target", netip.AddrPortFrom(topology.WAN14, 18080).String(), "--result", result,
		"--socket-mark", strconv.FormatUint(m7OuterMark, 10))
	if err == nil {
		t.Fatal("unprivileged ordinary process obtained Irvio bypass mark")
	}
	if !strings.Contains(err.Error(), "operation not permitted") && !strings.Contains(strings.ToLower(output), "operation not permitted") {
		t.Fatalf("unauthorized mark failed for an unrelated reason: %v: %s", err, output)
	}
}

func countM8Observations(t *testing.T, path string) int {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var item m7Observation
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}

func m8LogAdopted(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	for {
		var item map[string]any
		if err := decoder.Decode(&item); err != nil {
			return false
		}
		adopted, _ := item["adopted"].(bool)
		if item["msg"] == "HYDRAD_PROTECTED_MODE" && adopted {
			return true
		}
	}
}

func waitM8File(t *testing.T, child *childProcess, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, path); err != nil {
		t.Fatal(err)
	}
}

func waitM8Child(t *testing.T, child *childProcess) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := child.wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func waitM8ChildFailure(t *testing.T, child *childProcess) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := child.wait(ctx); err == nil {
		t.Fatal("child unexpectedly exited successfully")
	}
}

func runM8Command(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return suite.command(ctx, name, args...)
}

func runM8CommandExpectFailure(t *testing.T, name string, args ...string) {
	t.Helper()
	if _, err := runM8Command(name, args...); err == nil {
		t.Fatalf("command unexpectedly succeeded: %s %s", name, strings.Join(args, " "))
	}
}
