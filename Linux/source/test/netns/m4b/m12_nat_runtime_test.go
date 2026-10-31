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

	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
)

func TestM12_IrviodBrokeredNATToNATPRFLXInternet(t *testing.T) {
	runM12BrokeredNATToNATPRFLXInternet(t, 0, false)
}

func TestM12_IrviodBrokeredNATRemapRecovery(t *testing.T) {
	runM12BrokeredNATToNATPRFLXInternet(t, 1, false)
}

func TestM12_IrviodBrokeredRepeatedNATRemapRecovery(t *testing.T) {
	runM12BrokeredNATToNATPRFLXInternet(t, 10, false)
}

func TestM12_IrviodBrokeredExitNATRemapRecovery(t *testing.T) {
	runM12BrokeredNATToNATPRFLXInternet(t, 0, true)
}

func runM12BrokeredNATToNATPRFLXInternet(t *testing.T, remapCycles int, exitRemap bool) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	assertNormalUserExecutable(t, "HYDRA_M4B_RFC5780")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	var conntrackHelper string
	if remapCycles > 0 || exitRemap {
		conntrackHelper = assertNormalUserExecutable(t, "HYDRA_M4B_CONNTRACK")
		assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")
	}

	name := "m12-brokered-nat-prflx"
	if remapCycles == 1 {
		name = "m12-brokered-nat-remap"
	} else if remapCycles > 1 {
		name = "m12-brokered-nat-remap-repeated"
	} else if exitRemap {
		name = "m12-brokered-exit-nat-remap"
	}
	lab := newLab(t, name)
	topology := lab.buildICENATTopology(true, true)
	scenario := iceNATScenario{
		name: "m12-eim-eif-to-apdm-apdf-prflx",
		aNAT: true, bNAT: true, aMapping: mappingEIM, bMapping: mappingAPDM,
		aFiltering: filteringEIF, bFiltering: filteringAPDF,
		wantConnected: true, wantAPeerReflexiveForBMapping: true,
	}
	installICENATScenarioRules(lab, topology, scenario)
	allowM12NATControl(lab, topology.ARouter, topology.AInsideIf, topology.AOutsideIf,
		topology.AData, topology.AExternal, topology.BExternal)
	allowM12NATControl(lab, topology.BRouter, topology.BInsideIf, topology.BOutsideIf,
		topology.BData, topology.BExternal, topology.AExternal)
	lab.inNamespace(topology.ANamespace, "sysctl", "-q", "-w", "net.ipv4.conf.all.rp_filter=2")
	lab.inNamespace(topology.ANamespace, "sysctl", "-q", "-w", "net.ipv4.conf."+topology.ADataIf+".rp_filter=2")
	lab.inNamespace(topology.ANamespace, "sysctl", "-q", "-w", "net.ipv4.conf.all.src_valid_mark=0")
	stun := lab.startResponder("m12-nat-stun", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)

	internetNamespace := topology.ServerNamespace
	exitWANIf := lab.interfaceName('b')
	internetIf := lab.interfaceName('s')
	internetAddress := netip.MustParseAddr("198.18.90.110")
	lab.inNamespace(internetNamespace, "ip", "addr", "add", internetAddress.String()+"/24", "dev", internetIf)
	m7View := m7Topology{
		ClientNamespace: topology.ANamespace, ExitNamespace: topology.BNamespace, InternetNamespace: internetNamespace,
		ClientOuterIf: topology.ADataIf, ExitOuterIf: lab.interfaceName('b'), ExitWANIf: exitWANIf, InternetIf: internetIf,
		ClientOuter4: topology.AData, ExitOuter4: topology.BData, ExitWAN4: topology.BExternal, Internet4: internetAddress,
	}
	internet, internetRelease, internetObservations := startM7InternetServer(t, lab, m7View)

	runtimeDir := lab.tempDir("m12-nat-runtime")
	paths := make(map[string]string)
	for _, name := range []string{"bootstrap", "verifier-1", "verifier-2", "verifier-3", "r1", "r2", "exit", "client"} {
		paths[name] = filepath.Join(runtimeDir, name)
		if err := os.Mkdir(paths[name], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	geoIP := filepath.Join(runtimeDir, "country.mmdb")
	writeM12CountryDatabase(t, geoIP, "198.18.90.0/24", "DE")

	bootstrapProcess := lab.startChild("m12-nat-bootstrap", topology.ServerNamespace, irviod,
		"--data-dir", paths["bootstrap"], "--listen-port", "45200", "--print-node-info")
	bootstrapInfo := waitNodeInfo(t, lab, bootstrapProcess, "m12-nat-bootstrap.stdout.log", 1)
	bootstrap := selectMultiaddr(t, bootstrapInfo, "ip4", topology.ServerA1)

	verifierAddresses := make([]string, 0, 3)
	verifiers := make([]*childProcess, 0, 3)
	for index := 0; index < 3; index++ {
		label := fmt.Sprintf("m12-nat-verifier-%d", index+1)
		process := lab.startChild(label, topology.ServerNamespace, irviod,
			"--data-dir", paths[fmt.Sprintf("verifier-%d", index+1)],
			"--listen-port", strconv.Itoa(45210+index),
			"--allow-test-public-ip", "--print-node-info")
		info := waitNodeInfo(t, lab, process, label+".stdout.log", 1)
		verifierAddresses = append(verifierAddresses, selectMultiaddr(t, info, "ip4", topology.ServerA1))
		verifiers = append(verifiers, process)
	}

	r1Args := m12RendezvousArgs(paths["r1"], bootstrap, "45201", geoIP, verifierAddresses)
	r1 := lab.startChild("m12-nat-r1", topology.ServerNamespace, irviod, r1Args...)
	r1Info := waitNodeInfo(t, lab, r1, "m12-nat-r1.stdout.log", 1)
	_ = waitM11Log(t, lab, r1, "m12-nat-r1.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 15*time.Second, nil)
	r1Address := selectMultiaddr(t, r1Info, "ip4", topology.ServerA1)

	r2Args := m12RendezvousArgs(paths["r2"], bootstrap, "45202", geoIP, verifierAddresses)
	r2 := lab.startChild("m12-nat-r2", topology.ServerNamespace, irviod, r2Args...)
	r2Info := waitNodeInfo(t, lab, r2, "m12-nat-r2.stdout.log", 1)
	_ = waitM11Log(t, lab, r2, "m12-nat-r2.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 15*time.Second, nil)
	r2Address := selectMultiaddr(t, r2Info, "ip4", topology.ServerA1)

	stunAddress := endpointValue(topology.ServerA1, stunPrimaryPort)
	exitArgs := m10FailoverExitArgs(paths["exit"], bootstrap, "45203", strconv.Itoa(clientMuxPort), exitWANIf, topology.BData)
	exitArgs = append(exitArgs,
		"--stun4", stunAddress.String(), "--rendezvous-v1", "--rendezvous-peer", r2Address,
		"--lease-ttl", "8s", "--lease-sweep", "100ms", "--lease-renew-lead", "2s", "--lease-renew-retry", "200ms")
	exitProcess := lab.startChild("m12-nat-exit", topology.BNamespace, irviod, exitArgs...)
	exitInfo := waitNodeInfo(t, lab, exitProcess, "m12-nat-exit.stdout.log", 1)
	registration := waitM11Log(t, lab, exitProcess, "m12-nat-exit.stderr.log", "RENDEZVOUS_REGISTRATIONS", 15*time.Second, func(event map[string]any) bool {
		active, _ := event["active"].(float64)
		return active == 1
	})
	if servers := fmt.Sprint(registration["servers"]); !strings.Contains(servers, r2Info.PeerID) || strings.Contains(servers, r1Info.PeerID) {
		t.Fatalf("NATed Exit registration escaped configured owner: %v", registration)
	}

	clientArgs := []string{
		"--data-dir", paths["client"], "--listen-port", "45204", "--bootstrap", bootstrap,
		"--runtime", "--connect", "DE", "--udp-port", strconv.Itoa(clientMuxPort), "--stun4", stunAddress.String(),
		"--tun-name", "hydra0", "--bypass-prefix", netip.PrefixFrom(topology.AData, 24).Masked().String(),
		"--lease-ttl", "8s", "--lease-sweep", "100ms", "--lease-renew-lead", "2s", "--lease-renew-retry", "200ms",
		"--rendezvous-v1", "--rendezvous-peer", r1Address,
		"--print-node-info", "--print-runtime-events",
	}
	clientProcess := lab.startChild("m12-nat-client", topology.ANamespace, irviod, clientArgs...)
	_ = waitNodeInfo(t, lab, clientProcess, "m12-nat-client.stdout.log", 1)
	connected := waitM10RuntimeStatus(t, lab, clientProcess, "m12-nat-client.stderr.log", 70*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == exitInfo.PeerID || status.State == irvioruntime.StateNoExit
	})
	if connected.State == irvioruntime.StateNoExit {
		t.Fatalf("brokered NAT-to-NAT candidate failed before promotion: %+v", connected)
	}
	wantPRFLX := netip.AddrPortFrom(topology.BExternal, m5c2BPeerPort)
	if connected.SelectedEndpoint != wantPRFLX.String() {
		t.Fatalf("M12 AWG endpoint=%s, want actual PRFLX %s instead of signaled SRFLX %s", connected.SelectedEndpoint,
			wantPRFLX, netip.AddrPortFrom(topology.BExternal, m5c2BStunPort))
	}
	if connected.MuxPort != clientMuxPort {
		t.Fatalf("M12 NAT Mux.Port=%d, want %d", connected.MuxPort, clientMuxPort)
	}
	if count, _ := namespaceUDPSockets(t, lab, topology.ANamespace); count != 1 {
		t.Fatalf("NATed Client has %d UDP4 sockets, want one IrvioSocketMux socket", count)
	}
	if count, _ := namespaceUDPSockets(t, lab, topology.BNamespace); count != 1 {
		t.Fatalf("NATed Exit has %d UDP4 sockets, want one IrvioSocketMux socket", count)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ANamespace, "http", netip.AddrPortFrom(internetAddress, 18080)),
		topology.BExternal, netip.MustParseAddr("10.77.0.2"))
	assertSTUNOracle(t, stun.observationPath,
		netip.AddrPortFrom(topology.AExternal, m5c2AStunPort),
		netip.AddrPortFrom(topology.BExternal, m5c2BStunPort))
	assertNATPacketOracle(t, lab, topology, scenario)
	for namespace, comment := range map[string]string{
		topology.ARouter: "m12-client-exit-direct-tcp",
		topology.BRouter: "m12-exit-client-direct-tcp",
	} {
		rules := lab.inNamespace(namespace, "nft", "-j", "list", "table", "ip", "hydra_m5c2")
		if leaked := requireCounter(t, rules, comment, 0); leaked != 0 {
			t.Fatalf("unexpected external Client/Exit TCP signaling via %s: %d packets", namespace, leaked)
		}
	}
	if remapCycles > 0 {
		assertM12NATRemapRecovery(t, lab, topology, scenario, stun.observationPath,
			internetObservations, clientProcess, exitProcess, connected, exitInfo.PeerID, internetAddress, conntrackHelper, remapCycles)
	} else if exitRemap {
		assertM12ExitNATRemapRecovery(t, lab, topology, scenario, stun.observationPath,
			internetObservations, clientProcess, connected, exitInfo.PeerID, internetAddress, conntrackHelper)
	}

	for _, item := range []struct {
		label   string
		process *childProcess
	}{
		{"Client", clientProcess}, {"Exit", exitProcess}, {"R1", r1}, {"R2", r2},
	} {
		if err := stopM10Child(item.process, 12*time.Second); err != nil {
			t.Fatalf("stop M12 NAT %s: %v", item.label, err)
		}
	}
	for index, verifier := range verifiers {
		if err := stopM10Child(verifier, 12*time.Second); err != nil {
			t.Fatalf("stop M12 NAT verifier %d: %v", index+1, err)
		}
	}
	if err := stopM10Child(bootstrapProcess, 12*time.Second); err != nil {
		t.Fatalf("stop M12 NAT bootstrap: %v", err)
	}
	if err := touch(internetRelease); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := internet.wait(ctx); err != nil {
		t.Fatalf("stop deterministic Internet: %v", err)
	}
	lab.inNamespace(topology.ANamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.BNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
}

func assertM12NATRemapRecovery(t *testing.T, lab *lab, topology iceNATTopology, scenario iceNATScenario,
	stunObservations, internetObservations string, clientProcess, exitProcess *childProcess, before m10RuntimeStatus, exitPeerID string, internetAddress netip.Addr,
	conntrackHelper string, cycles int,
) {
	t.Helper()
	initialFDs := processFDCount(t, clientProcess)
	current := before
	for cycle := 0; cycle < cycles; cycle++ {
		newPort := 53011 + cycle
		newEndpoint := netip.AddrPortFrom(topology.AExternal, uint16(newPort))
		beforeLeak := countM8Observations(t, internetObservations)
		leak, checkpoint, continueFile, leakResult := startM8LeakProbe(t, lab, topology.ANamespace,
			fmt.Sprintf("m12-nat-remap-leak-%d", cycle+1), topology.ADataIf, netip.AddrPortFrom(internetAddress, 18081))
		waitM8File(t, leak, checkpoint)
		lab.inNamespace(topology.ARouter, "nft", "delete", "table", "ip", "hydra_m5c2")
		lab.installNFT(topology.ARouter, "m12-client-remapped-nat", iceNATRules(
			topology.AData, topology.AExternal, topology.ServerA1, topology.BExternal,
			topology.AInsideIf, topology.AOutsideIf, newPort, newPort,
			m5c2BStunPort, m5c2BPeerPort, scenario.aMapping, scenario.aFiltering))
		allowM12NATControl(lab, topology.ARouter, topology.AInsideIf, topology.AOutsideIf,
			topology.AData, topology.AExternal, topology.BExternal)
		// Allow APDF checks to the client's newly signaled mapping.
		lab.inNamespace(topology.BRouter, "nft", "delete", "table", "ip", "hydra_m5c2")
		lab.installNFT(topology.BRouter, "m12-exit-after-client-remap", iceNATRules(
			topology.BData, topology.BExternal, topology.ServerA1, topology.AExternal,
			topology.BInsideIf, topology.BOutsideIf, m5c2BStunPort, m5c2BPeerPort,
			newPort, newPort, scenario.bMapping, scenario.bFiltering))
		allowM12NATControl(lab, topology.BRouter, topology.BInsideIf, topology.BOutsideIf,
			topology.BData, topology.BExternal, topology.AExternal)
		lab.inNamespace(topology.ARouter, conntrackHelper, "--flush")
		lab.inNamespace(topology.BRouter, conntrackHelper, "--flush")
		remappedAt := time.Now()
		if err := touch(continueFile); err != nil {
			t.Fatal(err)
		}
		waitM8Child(t, leak)
		assertM9LeakProbe(t, leakResult)
		if afterLeak := countM8Observations(t, internetObservations); afterLeak != beforeLeak {
			t.Fatalf("cycle %d ordinary physical-WAN traffic leaked during NAT remap: before=%d after=%d", cycle, beforeLeak, afterLeak)
		}

		recovered := waitM10RuntimeStatus(t, lab, clientProcess, "m12-nat-client.stderr.log", 90*time.Second,
			func(status m10RuntimeStatus) bool {
				return status.State == irvioruntime.StateConnected && status.SessionGeneration > current.SessionGeneration &&
					status.ActivePeer == exitPeerID
			})
		connectedAfter := time.Since(remappedAt)
		if recovered.NetworkGeneration != current.NetworkGeneration || recovered.TransportGeneration != current.TransportGeneration {
			t.Fatalf("cycle %d NAT remap unexpectedly rebuilt network/transport: before=%+v after=%+v", cycle, current, recovered)
		}
		if recovered.MuxPort != current.MuxPort || recovered.TunnelLeaseID == "" {
			t.Fatalf("cycle %d NAT remap lost mux/lease state: before=%+v after=%+v", cycle, current, recovered)
		}
		assertSTUNOracle(t, stunObservations, newEndpoint)
		clientLog, err := os.ReadFile(filepath.Join(lab.artifactDir, "m12-nat-client.stderr.log"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(clientLog), "mapped_endpoint="+newEndpoint.String()) &&
			!strings.Contains(string(clientLog), `"mapped_endpoint":"`+newEndpoint.String()+`"`) {
			t.Fatalf("cycle %d fresh mapped endpoint %s missing from client ICE refresh log", cycle, newEndpoint)
		}
		if !strings.Contains(string(clientLog), "local_candidates=") || !strings.Contains(string(clientLog), newEndpoint.String()) {
			t.Fatalf("cycle %d fresh mapped endpoint %s missing from brokered client candidates", cycle, newEndpoint)
		}
		exitLog, err := os.ReadFile(filepath.Join(lab.artifactDir, "m12-nat-exit.stderr.log"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(exitLog), "remote_endpoint="+newEndpoint.String()) &&
			!strings.Contains(string(exitLog), `"remote_endpoint":"`+newEndpoint.String()+`"`) {
			t.Fatalf("cycle %d Exit ICE did not select the actual remapped client endpoint %s", cycle, newEndpoint)
		}
		if count, _ := namespaceUDPSockets(t, lab, topology.ANamespace); count != 1 {
			t.Fatalf("cycle %d NAT remap created %d client UDP4 sockets, want one", cycle, count)
		}
		assertM7Probe(t, runM7Probe(t, lab, topology.ANamespace, "http", netip.AddrPortFrom(internetAddress, 18080)),
			topology.BExternal, netip.MustParseAddr("10.77.0.2"))
		t.Logf("cycle %d remap-to-connected=%s remap-to-first-user-probe=%s", cycle+1,
			connectedAfter.Round(time.Millisecond), time.Since(remappedAt).Round(time.Millisecond))
		current = recovered
	}
	finalFDs := processFDCount(t, clientProcess)
	t.Logf("client FD baseline=%d final=%d after %d remaps", initialFDs, finalFDs, cycles)
	if finalFDs > initialFDs+2 {
		t.Fatalf("client FDs grew across repeated NAT remaps: baseline=%d final=%d", initialFDs, finalFDs)
	}
}

func assertM12ExitNATRemapRecovery(t *testing.T, lab *lab, topology iceNATTopology, scenario iceNATScenario,
	stunObservations, internetObservations string, clientProcess *childProcess, before m10RuntimeStatus, exitPeerID string, internetAddress netip.Addr,
	conntrackHelper string,
) {
	t.Helper()
	const (
		newStunPort = 54011
		newPeerPort = 54012
	)
	newSRFLX := netip.AddrPortFrom(topology.BExternal, newStunPort)
	newPRFLX := netip.AddrPortFrom(topology.BExternal, newPeerPort)
	beforeLeak := countM8Observations(t, internetObservations)
	leak, checkpoint, continueFile, leakResult := startM8LeakProbe(t, lab, topology.ANamespace,
		"m12-exit-nat-remap-leak", topology.ADataIf, netip.AddrPortFrom(internetAddress, 18081))
	waitM8File(t, leak, checkpoint)
	lab.inNamespace(topology.BRouter, "nft", "delete", "table", "ip", "hydra_m5c2")
	lab.installNFT(topology.BRouter, "m12-exit-remapped-nat", iceNATRules(
		topology.BData, topology.BExternal, topology.ServerA1, topology.AExternal,
		topology.BInsideIf, topology.BOutsideIf, newStunPort, newPeerPort,
		m5c2AStunPort, m5c2AStunPort, scenario.bMapping, scenario.bFiltering))
	allowM12NATControl(lab, topology.BRouter, topology.BInsideIf, topology.BOutsideIf,
		topology.BData, topology.BExternal, topology.AExternal)
	// Allow the controlled router to reach the Exit's refreshed SRFLX/PRFLX ports.
	lab.inNamespace(topology.ARouter, "nft", "delete", "table", "ip", "hydra_m5c2")
	lab.installNFT(topology.ARouter, "m12-client-after-exit-remap", iceNATRules(
		topology.AData, topology.AExternal, topology.ServerA1, topology.BExternal,
		topology.AInsideIf, topology.AOutsideIf, m5c2AStunPort, m5c2APeerPort,
		newStunPort, newPeerPort, scenario.aMapping, scenario.aFiltering))
	allowM12NATControl(lab, topology.ARouter, topology.AInsideIf, topology.AOutsideIf,
		topology.AData, topology.AExternal, topology.BExternal)
	lab.inNamespace(topology.ARouter, conntrackHelper, "--flush")
	lab.inNamespace(topology.BRouter, conntrackHelper, "--flush")
	if err := touch(continueFile); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, leak)
	assertM9LeakProbe(t, leakResult)
	if afterLeak := countM8Observations(t, internetObservations); afterLeak != beforeLeak {
		t.Fatalf("ordinary physical-WAN traffic leaked during Exit NAT remap: before=%d after=%d", beforeLeak, afterLeak)
	}

	recovered := waitM10RuntimeStatus(t, lab, clientProcess, "m12-nat-client.stderr.log", 90*time.Second,
		func(status m10RuntimeStatus) bool {
			return status.State == irvioruntime.StateConnected && status.SessionGeneration > before.SessionGeneration &&
				status.ActivePeer == exitPeerID
		})
	if recovered.NetworkGeneration != before.NetworkGeneration || recovered.TransportGeneration != before.TransportGeneration ||
		recovered.MuxPort != before.MuxPort || recovered.TunnelLeaseID == "" {
		t.Fatalf("Exit NAT remap broke transport ownership: before=%+v after=%+v", before, recovered)
	}
	if recovered.SelectedEndpoint != newPRFLX.String() {
		t.Fatalf("client AWG endpoint=%s, want remapped Exit PRFLX %s", recovered.SelectedEndpoint, newPRFLX)
	}
	assertSTUNOracle(t, stunObservations, newSRFLX)
	clientLog, err := os.ReadFile(filepath.Join(lab.artifactDir, "m12-nat-client.stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clientLog), newSRFLX.String()) ||
		!strings.Contains(string(clientLog), "remote_endpoint="+newPRFLX.String()) {
		t.Fatalf("client transcript lacks refreshed Exit SRFLX %s or selected PRFLX %s", newSRFLX, newPRFLX)
	}
	if count, _ := namespaceUDPSockets(t, lab, topology.BNamespace); count != 1 {
		t.Fatalf("Exit NAT remap created %d Exit UDP4 sockets, want one", count)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ANamespace, "http", netip.AddrPortFrom(internetAddress, 18080)),
		topology.BExternal, netip.MustParseAddr("10.77.0.2"))
}

func processFDCount(t *testing.T, process *childProcess) int {
	t.Helper()
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", process.command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func allowM12NATControl(lab *lab, router, insideIf, outsideIf string, private, external, remoteExternal netip.Addr) {
	lab.t.Helper()
	lab.inNamespace(router, "nft", "insert", "rule", "ip", "hydra_m5c2", "forward",
		"iifname", insideIf, "ip", "saddr", private.String(), "ip", "daddr", remoteExternal.String(),
		"meta", "l4proto", "tcp", "counter", "drop", "comment", "m12-"+m12NATSide(private)+"-direct-tcp")
	lab.inNamespace(router, "nft", "add", "rule", "ip", "hydra_m5c2", "forward",
		"iifname", insideIf, "oifname", outsideIf, "ip", "saddr", private.String(),
		"meta", "l4proto", "tcp", "counter", "accept", "comment", "m12-control-tcp")
	lab.inNamespace(router, "nft", "add", "rule", "ip", "hydra_m5c2", "postrouting",
		"ip", "saddr", private.String(), "meta", "l4proto", "tcp", "counter", "snat", "to", external.String(),
		"comment", "m12-control-snat")
}

func m12NATSide(private netip.Addr) string {
	if private == netip.MustParseAddr("10.51.1.2") {
		return "client-exit"
	}
	return "exit-client"
}
