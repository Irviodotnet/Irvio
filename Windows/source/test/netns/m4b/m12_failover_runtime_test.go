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
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func TestM12_IrviodBrokeredExitFailoverRemainsProtected(t *testing.T) {
	runIrviodBrokeredExitFailoverRemainsProtected(t)
}

func TestP1_CoreFailoverIsObservedWithoutLocalOwnership(t *testing.T) {
	runIrviodBrokeredExitFailoverRemainsProtected(t)
}

func TestP2_IrvioDartClientObservesCoreFailover(t *testing.T) {
	runIrviodBrokeredExitFailoverRemainsProtected(t)
}

func runIrviodBrokeredExitFailoverRemainsProtected(t *testing.T) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")

	lab := newLab(t, "m12-brokered-failover")
	topology := buildM10FailoverTopology(lab)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.InternetA4.String()+"/32",
		"via", topology.ExitAData4.String(), "dev", topology.RouterAIf)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.InternetB4.String()+"/32",
		"via", topology.ExitBData4.String(), "dev", topology.RouterBIf)
	internet, internetRelease, observations := startM7InternetServer(t, lab, m7Topology{
		InternetNamespace: topology.InternetNamespace,
		Internet4:         topology.Internet4,
	})

	runtimeDir := lab.tempDir("m12-failover")
	paths := map[string]string{}
	for _, name := range []string{
		"bootstrap", "verifier-1", "verifier-2", "verifier-3",
		"r1", "r2", "exit-a", "exit-b", "client",
	} {
		paths[name] = filepath.Join(runtimeDir, name)
		if err := os.Mkdir(paths[name], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	geoIP := filepath.Join(runtimeDir, "country.mmdb")
	writeM12CountryDatabase(t, geoIP, "0.0.0.0/0", "DE")

	bootstrapProcess := lab.startChild("m12-failover-bootstrap", topology.RouterNamespace, irviod,
		"--data-dir", paths["bootstrap"], "--listen-port", "45300", "--print-node-info")
	bootstrapInfo := waitNodeInfo(t, lab, bootstrapProcess, "m12-failover-bootstrap.stdout.log", 1)
	bootstrap := selectMultiaddr(t, bootstrapInfo, "ip4", topology.RouterClient4)

	verifierAddresses := make([]string, 0, 3)
	verifiers := make([]*childProcess, 0, 3)
	for index := 0; index < 3; index++ {
		label := fmt.Sprintf("m12-failover-verifier-%d", index+1)
		process := lab.startChild(label, topology.InternetNamespace, irviod,
			"--data-dir", paths[fmt.Sprintf("verifier-%d", index+1)],
			"--listen-port", fmt.Sprintf("%d", 45310+index),
			"--allow-test-public-ip", "--print-node-info")
		info := waitNodeInfo(t, lab, process, label+".stdout.log", 1)
		verifierAddresses = append(verifierAddresses, selectMultiaddr(t, info, "ip4", topology.Internet4))
		verifiers = append(verifiers, process)
	}

	r1 := lab.startChild("m12-failover-r1", topology.InternetNamespace, irviod,
		m12RendezvousArgs(paths["r1"], bootstrap, "45301", geoIP, verifierAddresses)...)
	r1Info := waitNodeInfo(t, lab, r1, "m12-failover-r1.stdout.log", 1)
	_ = waitM11Log(t, lab, r1, "m12-failover-r1.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 15*time.Second, nil)
	r1Address := selectMultiaddr(t, r1Info, "ip4", topology.Internet4)

	r2 := lab.startChild("m12-failover-r2", topology.InternetNamespace, irviod,
		m12RendezvousArgs(paths["r2"], bootstrap, "45302", geoIP, verifierAddresses)...)
	r2Info := waitNodeInfo(t, lab, r2, "m12-failover-r2.stdout.log", 1)
	_ = waitM11Log(t, lab, r2, "m12-failover-r2.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 15*time.Second, nil)
	r2Address := selectMultiaddr(t, r2Info, "ip4", topology.Internet4)

	exitAArgs := m10FailoverExitArgs(paths["exit-a"], bootstrap, "45303", "41403", topology.ExitAWANIf, topology.ExitAWAN4)
	exitAArgs = append(exitAArgs,
		"--rendezvous-v1", "--rendezvous-peer", r2Address,
		"--lease-ttl", "5s", "--lease-renew-lead", "2s", "--lease-renew-retry", "300ms")
	exitA := lab.startChild("m12-failover-exit-a", topology.ExitANamespace, irviod, exitAArgs...)
	exitAInfo := waitNodeInfo(t, lab, exitA, "m12-failover-exit-a.stdout.log", 1)
	waitM12RegistrationOwner(t, lab, exitA, "m12-failover-exit-a.stderr.log", r2Info.PeerID)

	exitBArgs := m10FailoverExitArgs(paths["exit-b"], bootstrap, "45304", "41404", topology.ExitBWANIf, topology.ExitBWAN4)
	exitBArgs = append(exitBArgs,
		"--rendezvous-v1", "--rendezvous-peer", r2Address,
		"--lease-ttl", "5s", "--lease-renew-lead", "2s", "--lease-renew-retry", "300ms")
	exitB := lab.startChild("m12-failover-exit-b", topology.ExitBNamespace, irviod, exitBArgs...)
	exitBInfo := waitNodeInfo(t, lab, exitB, "m12-failover-exit-b.stdout.log", 1)
	waitM12RegistrationOwner(t, lab, exitB, "m12-failover-exit-b.stderr.log", r2Info.PeerID)
	if exitAInfo.PeerID == exitBInfo.PeerID {
		t.Fatal("independent brokered Exits share a PeerID")
	}

	lab.installNFT(topology.ClientNamespace, "m12-failover-no-direct", fmt.Sprintf(`table inet m12_no_direct {
 chain output { type filter hook output priority -250; policy accept;
  ip daddr { %s, %s, %s, %s } meta l4proto tcp counter drop comment "client-exit-direct-tcp"
 }
}`, topology.ExitAData4, topology.ExitAWAN4, topology.ExitBData4, topology.ExitBWAN4))
	for _, item := range []struct {
		namespace string
		label     string
	}{
		{topology.ExitANamespace, "m12-failover-exit-a-no-direct"},
		{topology.ExitBNamespace, "m12-failover-exit-b-no-direct"},
	} {
		lab.installNFT(item.namespace, item.label, fmt.Sprintf(`table inet m12_no_direct {
 chain output { type filter hook output priority -250; policy accept;
  ip daddr %s meta l4proto tcp counter drop comment "exit-client-direct-tcp"
 }
}`, topology.Client4))
	}

	client := lab.startChild("m12-failover-client", topology.ClientNamespace, irviod,
		"--data-dir", paths["client"], "--listen-port", "45305", "--bootstrap", bootstrap,
		"--runtime", "--connect", "DE", "--udp-port", "41405", "--tun-name", "hydra0",
		"--bypass-prefix", netip.PrefixFrom(topology.Client4, 24).Masked().String(),
		"--lease-ttl", "5s", "--lease-sweep", "100ms",
		"--lease-renew-lead", "2s", "--lease-renew-retry", "300ms",
		"--rendezvous-v1", "--rendezvous-peer", r1Address,
		"--local-api-socket", filepath.Join(paths["client"], "control.sock"), "--local-api-group", "root",
		"--print-node-info", "--print-runtime-events")
	_ = waitNodeInfo(t, lab, client, "m12-failover-client.stdout.log", 1)
	initial := waitM10RuntimeStatus(t, lab, client, "m12-failover-client.stderr.log", 60*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer != "" && status.ReservePeer != ""
	})
	if initial.ActivePeer == initial.ReservePeer {
		t.Fatalf("brokered ACTIVE and RESERVE are not distinct: %+v", initial)
	}
	exits := map[string]*childProcess{exitAInfo.PeerID: exitA, exitBInfo.PeerID: exitB}
	sources := map[string]netip.Addr{exitAInfo.PeerID: topology.ExitAWAN4, exitBInfo.PeerID: topology.ExitBWAN4}
	activeProcess := exits[initial.ActivePeer]
	initialSource := sources[initial.ActivePeer]
	if activeProcess == nil || !initialSource.IsValid() {
		t.Fatalf("brokered selector chose an unknown Exit: %+v", initial)
	}
	target := netip.AddrPortFrom(topology.Internet4, 18080)
	initialProbe := runM7Probe(t, lab, topology.ClientNamespace, "http", target)
	assertM7Probe(t, initialProbe, initialSource, netip.MustParseAddr("10.77.0.2"))
	isP2 := t.Name() == "TestP2_IrvioDartClientObservesCoreFailover"
	var (
		gui        *localapi.Client
		events     <-chan *localv1.Event
		apiCancel  context.CancelFunc = func() {}
		p2Observer *childProcess
	)
	if isP2 {
		uiClient := assertNormalUserExecutable(t, "HYDRA_P2_UI_CLIENT")
		p2Observer = lab.startChild("p2-failover-observer", topology.ClientNamespace, uiClient,
			"--socket", filepath.Join(paths["client"], "control.sock"), "observe-failover", "DE")
		_ = waitM11Log(t, lab, p2Observer, "p2-failover-observer.stdout.log", "P2_OBSERVER_READY", 8*time.Second, nil)
	} else {
		apiContext, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		apiCancel = cancel
		var err error
		gui, err = localapi.Dial(apiContext, filepath.Join(paths["client"], "control.sock"))
		if err != nil {
			apiCancel()
			t.Fatalf("connect P1 failover observer: %v", err)
		}
		events, err = gui.SubscribeEvents(apiContext)
		if err != nil {
			_ = gui.Close()
			apiCancel()
			t.Fatalf("subscribe P1 failover observer: %v", err)
		}
		_ = waitP1ProductState(t, events, localv1.ConnectionState_CONNECTION_STATE_CONNECTED, 5*time.Second)
	}

	served := waitM11Log(t, lab, r1, "m12-failover-r1.stderr.log", "RENDEZVOUS_CANDIDATES_SERVED", 5*time.Second, func(event map[string]any) bool {
		local, _ := event["local"].(float64)
		remote, _ := event["remote"].(float64)
		returned, _ := event["returned"].(float64)
		return local == 0 && remote >= 2 && returned >= 2
	})
	if calls, _ := served["remote_calls"].(float64); calls > 3 {
		t.Fatalf("one GET exceeded remote SAMPLE fan-out: %v", served)
	}
	_ = waitM11Log(t, lab, r2, "m12-failover-r2.stderr.log", "RENDEZVOUS_LOCAL_SAMPLE_SERVED", 5*time.Second, func(event map[string]any) bool {
		returned, _ := event["returned"].(float64)
		return returned >= 2
	})

	beforeLeak := countM8Observations(t, observations)
	leak, checkpoint, continueFile, leakResult := startM8LeakProbe(
		t, lab, topology.ClientNamespace, "m12-failover-leak", topology.ClientIf,
		netip.AddrPortFrom(topology.Internet4, 18081),
	)
	waitM8File(t, leak, checkpoint)
	killM10Child(t, activeProcess)
	if err := touch(continueFile); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, leak)
	assertM9LeakProbe(t, leakResult)
	if isP2 {
		_ = waitM11Log(t, lab, p2Observer, "p2-failover-observer.stdout.log", "P2_OBSERVER_RECOVERING", 30*time.Second, nil)
	} else {
		_ = waitP1ProductState(t, events, localv1.ConnectionState_CONNECTION_STATE_RECOVERING, 30*time.Second)
	}
	if afterLeak := countM8Observations(t, observations); afterLeak != beforeLeak {
		t.Fatalf("ordinary physical-WAN traffic leaked during brokered failover: before=%d after=%d", beforeLeak, afterLeak)
	}

	failover := waitM10RuntimeStatus(t, lab, client, "m12-failover-client.stderr.log", 60*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer != "" &&
			status.ActivePeer != initial.ActivePeer && status.SessionGeneration > initial.SessionGeneration
	})
	newSource := sources[failover.ActivePeer]
	if failover.ActivePeer != initial.ReservePeer || !newSource.IsValid() {
		t.Fatalf("runtime did not promote brokered RESERVE: before=%+v after=%+v", initial, failover)
	}
	finalProbe := runM7Probe(t, lab, topology.ClientNamespace, "http", target)
	assertM7Probe(t, finalProbe, newSource, netip.MustParseAddr("10.77.0.2"))
	if isP2 {
		productRecovered := waitM11Log(t, lab, p2Observer, "p2-failover-observer.stdout.log", "P2_OBSERVER_RECOVERED", 8*time.Second, nil)
		if productRecovered["country"] != "DE" {
			t.Fatalf("P2 failover observer lost CountryIntent: %v", productRecovered)
		}
		observerContext, observerCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer observerCancel()
		if err := p2Observer.wait(observerContext); err != nil {
			t.Fatalf("P2 failover observer did not exit cleanly: %v", err)
		}
	} else {
		productRecovered := waitP1ProductState(t, events, localv1.ConnectionState_CONNECTION_STATE_CONNECTED, 5*time.Second)
		if productRecovered.CountryCode != "DE" {
			t.Fatalf("P1 failover observer lost CountryIntent: %v", productRecovered)
		}
		_ = gui.Close()
		apiCancel()
	}
	if initialProbe.ObservedSource == finalProbe.ObservedSource {
		t.Fatalf("Internet source did not change after brokered failover: %s", initialProbe.ObservedSource)
	}

	directClient := lab.inNamespace(topology.ClientNamespace, "nft", "-j", "list", "table", "inet", "m12_no_direct")
	_ = requireCounter(t, directClient, "client-exit-direct-tcp", 0)
	for _, namespace := range []string{topology.ExitANamespace, topology.ExitBNamespace} {
		directExit := lab.inNamespace(namespace, "nft", "-j", "list", "table", "inet", "m12_no_direct")
		_ = requireCounter(t, directExit, "exit-client-direct-tcp", 0)
	}

	if err := stopM10Child(client, 12*time.Second); err != nil {
		t.Fatalf("stop M12 failover Client: %v", err)
	}
	for peerID, process := range exits {
		if peerID == initial.ActivePeer {
			continue
		}
		if err := stopM10Child(process, 12*time.Second); err != nil {
			t.Fatalf("stop surviving M12 Exit: %v", err)
		}
	}
	for label, process := range map[string]*childProcess{"r1": r1, "r2": r2} {
		if err := stopM10Child(process, 12*time.Second); err != nil {
			t.Fatalf("stop %s: %v", label, err)
		}
	}
	for index, verifier := range verifiers {
		if err := stopM10Child(verifier, 12*time.Second); err != nil {
			t.Fatalf("stop verifier %d: %v", index+1, err)
		}
	}
	if err := stopM10Child(bootstrapProcess, 12*time.Second); err != nil {
		t.Fatalf("stop bootstrap: %v", err)
	}
	if err := touch(internetRelease); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := internet.wait(ctx); err != nil {
		t.Fatalf("stop deterministic Internet: %v", err)
	}
	lab.inNamespace(topology.ClientNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ExitANamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ExitBNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
}

func waitM12RegistrationOwner(t *testing.T, lab *lab, process *childProcess, file, ownerPeerID string) {
	t.Helper()
	event := waitM11Log(t, lab, process, file, "RENDEZVOUS_REGISTRATIONS", 15*time.Second, func(event map[string]any) bool {
		active, _ := event["active"].(float64)
		return active == 1
	})
	servers := fmt.Sprint(event["servers"])
	if !strings.Contains(servers, ownerPeerID) {
		t.Fatalf("Exit registration does not belong to configured owner %s: %v", ownerPeerID, event)
	}
}
