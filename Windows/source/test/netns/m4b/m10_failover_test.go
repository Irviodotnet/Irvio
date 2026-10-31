// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/underlay"
)

type m10FailoverTopology struct {
	ClientNamespace, RouterNamespace, ExitANamespace, ExitBNamespace, InternetNamespace string
	ClientIf, RouterClientIf, ExitADataIf, RouterAIf, ExitBDataIf, RouterBIf            string
	ExitAWANIf, InternetAIf, ExitBWANIf, InternetBIf                                    string
	Client4, RouterClient4, ExitAData4, RouterA4, ExitBData4, RouterB4                  netip.Addr
	ExitAWAN4, InternetA4, ExitBWAN4, InternetB4, Internet4                             netip.Addr
}

func TestM10_IrviodAutomaticExitFailoverRemainsProtected(t *testing.T) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")

	lab := newLab(t, "m10-runtime-failover")
	topology := buildM10FailoverTopology(lab)
	internetTopology := m7Topology{InternetNamespace: topology.InternetNamespace, Internet4: topology.Internet4}
	internet, internetRelease, observations := startM7InternetServer(t, lab, internetTopology)

	runtimeDir := lab.tempDir("m10-failover")
	bootstrapDir := filepath.Join(runtimeDir, "bootstrap")
	exitADir := filepath.Join(runtimeDir, "exit-a")
	exitBDir := filepath.Join(runtimeDir, "exit-b")
	clientDir := filepath.Join(runtimeDir, "client")
	for _, directory := range []string{bootstrapDir, exitADir, exitBDir, clientDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	bootstrapProcess := lab.startChild("m10-failover-bootstrap", topology.RouterNamespace, irviod,
		"--data-dir", bootstrapDir, "--listen-port", "43000", "--print-node-info")
	bootstrapInfo := waitNodeInfo(t, lab, bootstrapProcess, "m10-failover-bootstrap.stdout.log", 1)
	bootstrap := selectMultiaddr(t, bootstrapInfo, "ip4", topology.RouterClient4)

	exitA := lab.startChild("m10-failover-exit-a", topology.ExitANamespace, irviod,
		m10FailoverExitArgs(exitADir, bootstrap, "43002", "41102", topology.ExitAWANIf, topology.ExitAWAN4)...)
	exitAInfo := waitNodeInfo(t, lab, exitA, "m10-failover-exit-a.stdout.log", 1)
	exitB := lab.startChild("m10-failover-exit-b", topology.ExitBNamespace, irviod,
		m10FailoverExitArgs(exitBDir, bootstrap, "43003", "41103", topology.ExitBWANIf, topology.ExitBWAN4)...)
	exitBInfo := waitNodeInfo(t, lab, exitB, "m10-failover-exit-b.stdout.log", 1)
	if exitAInfo.PeerID == exitBInfo.PeerID {
		t.Fatal("independent production Exits share a PeerID")
	}

	client := lab.startChild("m10-failover-client", topology.ClientNamespace, irviod,
		"--data-dir", clientDir,
		"--listen-port", "43001",
		"--bootstrap", bootstrap,
		"--runtime", "--connect", "DE",
		"--udp-port", "41101",
		"--tun-name", "hydra0",
		"--bypass-prefix", netip.PrefixFrom(topology.Client4, 24).Masked().String(),
		"--lease-ttl", "15s", "--lease-sweep", "100ms",
		"--lease-renew-lead", "5s", "--lease-renew-retry", "1s",
		"--print-node-info", "--print-runtime-events")
	_ = waitNodeInfo(t, lab, client, "m10-failover-client.stdout.log", 1)
	initial := waitM10RuntimeStatus(t, lab, client, "m10-failover-client.stderr.log", 35*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer != "" && status.ReservePeer != ""
	})
	if initial.ActivePeer == initial.ReservePeer {
		t.Fatalf("ACTIVE and RESERVE are not distinct: %+v", initial)
	}
	exits := map[string]*childProcess{exitAInfo.PeerID: exitA, exitBInfo.PeerID: exitB}
	sources := map[string]netip.Addr{exitAInfo.PeerID: topology.ExitAWAN4, exitBInfo.PeerID: topology.ExitBWAN4}
	activeProcess := exits[initial.ActivePeer]
	initialSource := sources[initial.ActivePeer]
	if activeProcess == nil || !initialSource.IsValid() {
		t.Fatalf("selector chose an unknown production Exit: %+v", initial)
	}
	target := netip.AddrPortFrom(topology.Internet4, 18080)
	initialProbe := runM7Probe(t, lab, topology.ClientNamespace, "http", target)
	assertM7Probe(t, initialProbe, initialSource, netip.MustParseAddr("10.77.0.2"))

	lab.installNFT(topology.ClientNamespace, "m10-underlay-blackhole", fmt.Sprintf(`table inet hydra_m10_bh {
  chain output {
    type filter hook output priority -300; policy accept;
    meta mark 0x%x counter drop
  }
}
`, m7OuterMark))
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", target, topology.ClientIf, 0, false)
	blackholed := waitM10RuntimeStatus(t, lab, client, "m10-failover-client.stderr.log", 15*time.Second, func(status m10RuntimeStatus) bool {
		return status.ActivePeer == initial.ActivePeer && status.UnderlayIPv4 == underlay.Unreachable
	})
	if blackholed.NetworkGeneration != initial.NetworkGeneration || blackholed.ReservePeer != initial.ReservePeer ||
		blackholed.ProtectedMode == 0 {
		t.Fatalf("marked underlay blackhole consumed peer state: before=%+v blackholed=%+v", initial, blackholed)
	}
	lab.inNamespace(topology.ClientNamespace, "ip", "route", "show", "default")
	lab.inNamespace(topology.ClientNamespace, "nft", "delete", "table", "inet", "hydra_m10_bh")
	recovered := waitM10RuntimeStatus(t, lab, client, "m10-failover-client.stderr.log", 20*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == initial.ActivePeer &&
			status.UnderlayIPv4 == underlay.Reachable && status.SessionGeneration >= initial.SessionGeneration
	})
	if recovered.ReservePeer != initial.ReservePeer {
		t.Fatalf("underlay recovery consumed RESERVE: before=%+v after=%+v", initial, recovered)
	}
	initial = recovered
	waitM10InternetProbe(t, lab, topology.ClientNamespace, target, initialSource, 15*time.Second)

	beforeLeak := countM8Observations(t, observations)
	leak, checkpoint, continueFile, leakResult := startM8LeakProbe(
		t, lab, topology.ClientNamespace, "m10-failover-leak", topology.ClientIf,
		netip.AddrPortFrom(topology.Internet4, 18081),
	)
	waitM8File(t, leak, checkpoint)
	killM10Child(t, activeProcess)
	if err := touch(continueFile); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, leak)
	assertM9LeakProbe(t, leakResult)
	if afterLeak := countM8Observations(t, observations); afterLeak != beforeLeak {
		t.Fatalf("ordinary physical-WAN traffic leaked during process failover: before=%d after=%d", beforeLeak, afterLeak)
	}

	failover := waitM10RuntimeStatus(t, lab, client, "m10-failover-client.stderr.log", 45*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer != "" &&
			status.ActivePeer != initial.ActivePeer && status.SessionGeneration > initial.SessionGeneration
	})
	newSource := sources[failover.ActivePeer]
	if !newSource.IsValid() || failover.ActivePeer != initial.ReservePeer {
		t.Fatalf("runtime did not promote the cached RESERVE: before=%+v after=%+v", initial, failover)
	}
	finalProbe := runM7Probe(t, lab, topology.ClientNamespace, "http", target)
	assertM7Probe(t, finalProbe, newSource, netip.MustParseAddr("10.77.0.2"))
	if initialProbe.ObservedSource == finalProbe.ObservedSource {
		t.Fatalf("Internet source did not change after automatic failover: %s", initialProbe.ObservedSource)
	}

	if err := stopM10Child(client, 12*time.Second); err != nil {
		t.Fatalf("stop failover Client hydrad: %v", err)
	}
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", target, topology.ClientIf, 0, true)
	for peerID, process := range exits {
		if peerID == initial.ActivePeer {
			continue
		}
		if err := stopM10Child(process, 12*time.Second); err != nil {
			t.Fatalf("stop remaining Exit hydrad: %v", err)
		}
	}
	if err := stopM10Child(bootstrapProcess, 12*time.Second); err != nil {
		t.Fatalf("stop failover bootstrap hydrad: %v", err)
	}
	if err := touch(internetRelease); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := internet.wait(ctx); err != nil {
		t.Fatalf("stop failover Internet: %v", err)
	}
	lab.inNamespace(topology.ClientNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ExitANamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ExitBNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
}

func buildM10FailoverTopology(lab *lab) m10FailoverTopology {
	lab.t.Helper()
	topology := m10FailoverTopology{
		ClientNamespace: lab.namespace("client"), RouterNamespace: lab.namespace("router"),
		ExitANamespace: lab.namespace("exit-a"), ExitBNamespace: lab.namespace("exit-b"),
		InternetNamespace: lab.namespace("internet"),
		ClientIf:          lab.interfaceName('c'), RouterClientIf: lab.interfaceName('r'),
		ExitADataIf: lab.interfaceName('a'), RouterAIf: lab.interfaceName('x'),
		ExitBDataIf: lab.interfaceName('b'), RouterBIf: lab.interfaceName('y'),
		ExitAWANIf: lab.interfaceName('w'), InternetAIf: lab.interfaceName('i'),
		ExitBWANIf: lab.interfaceName('v'), InternetBIf: lab.interfaceName('j'),
		Client4: netip.MustParseAddr("198.18.90.1"), RouterClient4: netip.MustParseAddr("198.18.90.254"),
		ExitAData4: netip.MustParseAddr("198.18.91.1"), RouterA4: netip.MustParseAddr("198.18.91.254"),
		ExitBData4: netip.MustParseAddr("198.18.92.1"), RouterB4: netip.MustParseAddr("198.18.92.254"),
		ExitAWAN4: netip.MustParseAddr("203.0.113.1"), InternetA4: netip.MustParseAddr("203.0.113.2"),
		ExitBWAN4: netip.MustParseAddr("198.51.100.1"), InternetB4: netip.MustParseAddr("198.51.100.2"),
		Internet4: netip.MustParseAddr("192.0.2.200"),
	}
	lab.createVeth(topology.ClientNamespace, topology.ClientIf, topology.RouterNamespace, topology.RouterClientIf)
	lab.createVeth(topology.ExitANamespace, topology.ExitADataIf, topology.RouterNamespace, topology.RouterAIf)
	lab.createVeth(topology.ExitBNamespace, topology.ExitBDataIf, topology.RouterNamespace, topology.RouterBIf)
	lab.createVeth(topology.ExitANamespace, topology.ExitAWANIf, topology.InternetNamespace, topology.InternetAIf)
	lab.createVeth(topology.ExitBNamespace, topology.ExitBWANIf, topology.InternetNamespace, topology.InternetBIf)
	for _, namespace := range []string{
		topology.ClientNamespace, topology.RouterNamespace, topology.ExitANamespace,
		topology.ExitBNamespace, topology.InternetNamespace,
	} {
		lab.inNamespace(namespace, "ip", "link", "set", "lo", "up")
	}
	for _, item := range []struct {
		namespace, iface string
		address          netip.Addr
	}{
		{topology.ClientNamespace, topology.ClientIf, topology.Client4},
		{topology.RouterNamespace, topology.RouterClientIf, topology.RouterClient4},
		{topology.ExitANamespace, topology.ExitADataIf, topology.ExitAData4},
		{topology.RouterNamespace, topology.RouterAIf, topology.RouterA4},
		{topology.ExitBNamespace, topology.ExitBDataIf, topology.ExitBData4},
		{topology.RouterNamespace, topology.RouterBIf, topology.RouterB4},
		{topology.ExitANamespace, topology.ExitAWANIf, topology.ExitAWAN4},
		{topology.InternetNamespace, topology.InternetAIf, topology.InternetA4},
		{topology.ExitBNamespace, topology.ExitBWANIf, topology.ExitBWAN4},
		{topology.InternetNamespace, topology.InternetBIf, topology.InternetB4},
	} {
		lab.inNamespace(item.namespace, "ip", "link", "set", item.iface, "up")
		lab.inNamespace(item.namespace, "ip", "addr", "add", item.address.String()+"/24", "dev", item.iface)
	}
	lab.inNamespace(topology.InternetNamespace, "ip", "addr", "add", topology.Internet4.String()+"/32", "dev", "lo")
	for _, namespace := range []string{topology.RouterNamespace, topology.ExitANamespace, topology.ExitBNamespace} {
		lab.inNamespace(namespace, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1")
	}
	lab.inNamespace(topology.ClientNamespace, "ip", "route", "add", "default", "via", topology.RouterClient4.String(), "dev", topology.ClientIf)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.ExitAWAN4.String()+"/32", "via", topology.ExitAData4.String(), "dev", topology.RouterAIf)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.ExitBWAN4.String()+"/32", "via", topology.ExitBData4.String(), "dev", topology.RouterBIf)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.Internet4.String()+"/32", "via", topology.ExitAData4.String(), "dev", topology.RouterAIf)
	clientPrefix := netip.PrefixFrom(topology.Client4, 24).Masked().String()
	lab.inNamespace(topology.ExitANamespace, "ip", "route", "add", clientPrefix, "via", topology.RouterA4.String(), "dev", topology.ExitADataIf)
	lab.inNamespace(topology.ExitBNamespace, "ip", "route", "add", clientPrefix, "via", topology.RouterB4.String(), "dev", topology.ExitBDataIf)
	lab.inNamespace(topology.ExitANamespace, "ip", "route", "add", "default", "via", topology.InternetA4.String(), "dev", topology.ExitAWANIf)
	lab.inNamespace(topology.ExitBNamespace, "ip", "route", "add", "default", "via", topology.InternetB4.String(), "dev", topology.ExitBWANIf)
	lab.inNamespace(topology.InternetNamespace, "ip", "route", "add", clientPrefix, "via", topology.ExitAWAN4.String(), "dev", topology.InternetAIf)
	lab.recordTopology(topology)
	return topology
}

func m10FailoverExitArgs(dataDir, bootstrap, listenPort, udpPort, wan string, wanAddress netip.Addr) []string {
	return []string{
		"--data-dir", dataDir, "--listen-port", listenPort, "--bootstrap", bootstrap,
		"--runtime", "--exit", "--country", "DE", "--udp-port", udpPort,
		"--tun-name", "hydra0", "--exit-wan", wan,
		"--exit-tun-address", "10.77.0.1/24", "--exit-ipv4-pool", "10.77.0.0/24",
		"--exit-management-prefix", wanAddress.String() + "/32",
		"--lease-ttl", "15s", "--lease-sweep", "100ms",
		"--print-node-info", "--print-runtime-events",
	}
}

func waitM10RuntimeStatus(
	t *testing.T,
	lab *lab,
	child *childProcess,
	file string,
	maximum time.Duration,
	ready func(m10RuntimeStatus) bool,
) m10RuntimeStatus {
	t.Helper()
	path := filepath.Join(lab.artifactDir, file)
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var latest m10RuntimeStatus
	for {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		for _, event := range decodeM10RuntimeEvents(data) {
			if event.isRuntimeEvent() {
				latest = event.m10RuntimeStatus
			}
		}
		if ready(latest) {
			return latest
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for hydrad runtime predicate: %v; latest=%+v", ctx.Err(), latest)
		case <-child.done:
			child.mu.Lock()
			err := child.waitErr
			child.mu.Unlock()
			t.Fatalf("hydrad exited before runtime predicate: %v; latest=%+v", err, latest)
		case <-ticker.C:
		}
	}
}

func killM10Child(t *testing.T, child *childProcess) {
	t.Helper()
	if err := syscall.Kill(-child.command.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill production hydrad: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := child.wait(ctx); err == nil {
		t.Fatal("SIGKILLed hydrad exited successfully")
	}
}

func waitM10InternetProbe(
	t *testing.T,
	lab *lab,
	namespace string,
	target netip.AddrPort,
	expectedNAT netip.Addr,
	maximum time.Duration,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		label := "m10-ready-probe-" + strconv.FormatUint(m7ProbeCounter.Add(1), 10)
		resultPath := filepath.Join(lab.artifactDir, label+".json")
		_, err := runM8Command(
			"ip", "netns", "exec", namespace, os.Getenv("HYDRA_M7_INTERNET"),
			"--mode", "probe", "--kind", "http", "--target", target.String(), "--result", resultPath,
		)
		if err == nil {
			var result m7ProbeResult
			if err = readJSON(resultPath, &result); err == nil {
				observed, observedErr := netip.ParseAddrPort(result.ObservedSource)
				local, localErr := netip.ParseAddrPort(result.LocalEndpoint)
				if observedErr == nil && localErr == nil && result.Success &&
					observed.Addr() == expectedNAT && local.Addr() == netip.MustParseAddr("10.77.0.2") {
					return
				}
				err = fmt.Errorf("unexpected Internet observation: %+v", result)
			}
		}
		lastErr = err
		select {
		case <-ctx.Done():
			t.Fatalf("Internet path did not recover before deadline: %v; last=%v", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}
