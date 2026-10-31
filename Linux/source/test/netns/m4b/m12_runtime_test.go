// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"

	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/localapi"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func TestM12_IrviodBrokeredInternetAndRendezvousIndependentRenewal(t *testing.T) {
	runIrviodBrokeredInternetAndRendezvousIndependentRenewal(t)
}

func TestP1_IrviodLocalAPIControlsFrozenM12Runtime(t *testing.T) {
	runIrviodBrokeredInternetAndRendezvousIndependentRenewal(t)
}

func TestP2_IrvioDartClientControlsFrozenM12Runtime(t *testing.T) {
	runIrviodBrokeredInternetAndRendezvousIndependentRenewal(t)
}

func runIrviodBrokeredInternetAndRendezvousIndependentRenewal(t *testing.T) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")

	lab := newLab(t, "m12-brokered-runtime")
	topology := buildM10FailoverTopology(lab)
	for _, namespace := range []string{
		topology.ClientNamespace, topology.RouterNamespace, topology.ExitANamespace,
		topology.ExitBNamespace, topology.InternetNamespace,
	} {
		lab.installNFT(namespace, "m12-no-dns", `table inet m12_no_dns {
	chain output { type filter hook output priority -300; policy accept;
	 udp dport 53 counter drop comment "dns-udp-attempt"
	 tcp dport 53 counter drop comment "dns-tcp-attempt"
	}
}`)
	}
	lab.inNamespace(topology.ClientNamespace, "sysctl", "-q", "-w", "net.ipv4.conf.all.rp_filter=2")
	lab.inNamespace(topology.ClientNamespace, "sysctl", "-q", "-w", "net.ipv4.conf."+topology.ClientIf+".rp_filter=2")
	lab.inNamespace(topology.ClientNamespace, "sysctl", "-q", "-w", "net.ipv4.conf.all.src_valid_mark=0")
	routerPrefix := netip.PrefixFrom(topology.RouterA4, 24).Masked()
	lab.inNamespace(topology.InternetNamespace, "ip", "route", "add", routerPrefix.String(),
		"via", topology.ExitAWAN4.String(), "dev", topology.InternetAIf)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.InternetA4.String()+"/32",
		"via", topology.ExitAData4.String(), "dev", topology.RouterAIf)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.InternetB4.String()+"/32",
		"via", topology.ExitBData4.String(), "dev", topology.RouterBIf)
	lab.installNFT(topology.RouterNamespace, "m12-ice-path", fmt.Sprintf(`table inet m12_ice_path {
 chain forward { type filter hook forward priority -300; policy accept;
  iifname "%s" oifname "%s" ip saddr %s ip daddr %s udp sport 41304 udp dport 41303 counter comment "ice-client-exit"
  iifname "%s" oifname "%s" ip saddr %s ip daddr %s udp sport 41303 udp dport 41304 counter comment "ice-exit-client"
 }
}`, topology.RouterClientIf, topology.RouterBIf, topology.Client4, topology.ExitBWAN4,
		topology.RouterBIf, topology.RouterClientIf, topology.ExitBWAN4, topology.Client4))
	lab.installNFT(topology.ClientNamespace, "m12-client-outer-ingress", fmt.Sprintf(`table inet m12_outer_ingress {
 chain prerouting { type filter hook prerouting priority -300; policy accept;
  iifname "%s" ip saddr %s ip daddr %s udp sport 41303 udp dport 41304 counter comment "outer-prerouting"
 }
 chain input { type filter hook input priority -300; policy accept;
  iifname "%s" ip saddr %s ip daddr %s udp sport 41303 udp dport 41304 counter comment "outer-input"
 }
}`, topology.ClientIf, topology.ExitBWAN4, topology.Client4,
		topology.ClientIf, topology.ExitBWAN4, topology.Client4))
	lab.installNFT(topology.ClientNamespace, "m12-block-direct-client", fmt.Sprintf(`table inet m12_no_direct {
 chain output { type filter hook output priority -250; policy accept;
  ip daddr { %s, %s } meta l4proto tcp counter drop comment "client-exit-direct-tcp"
 }
}`, topology.ExitBData4, topology.ExitBWAN4))
	lab.installNFT(topology.ExitBNamespace, "m12-block-direct-exit", fmt.Sprintf(`table inet m12_no_direct {
 chain output { type filter hook output priority -250; policy accept;
  ip daddr %s meta l4proto tcp counter drop comment "exit-client-direct-tcp"
 }
}`, topology.Client4))
	lab.installNFT(topology.ClientNamespace, "m12-client-tunnel-control", `table inet m12_tunnel_control {
	chain input { type filter hook input priority -300; policy accept;
	 iifname "hydra0" ip saddr 10.77.0.1 ip daddr 10.77.0.2 tcp sport 51822 counter comment "tunnel-control-in"
	}
	chain output { type filter hook output priority 100; policy accept;
	 meta mark 0x48595443 oifname "hydra0" counter comment "tunnel-control-hydra"
	 meta mark 0x48595443 oifname != "hydra0" counter comment "tunnel-control-wan"
	}
}`)
	lab.installNFT(topology.ExitBNamespace, "m12-exit-tunnel-control", `table inet m12_tunnel_control {
	chain input { type filter hook input priority -300; policy accept;
	 iifname "hydra0" ip saddr 10.77.0.2 ip daddr 10.77.0.1 tcp dport 51822 counter comment "tunnel-control-in"
	}
	chain output { type filter hook output priority 100; policy accept;
	 meta mark 0x48595443 oifname "hydra0" counter comment "tunnel-control-hydra"
	 meta mark 0x48595443 oifname != "hydra0" counter comment "tunnel-control-wan"
 }
}`)
	internet, internetRelease, _ := startM7InternetServer(t, lab, m7Topology{
		InternetNamespace: topology.InternetNamespace, Internet4: topology.Internet4,
	})

	runtimeDir := lab.tempDir("m12-runtime")
	paths := map[string]string{}
	for _, name := range []string{"bootstrap", "verifier-1", "verifier-2", "verifier-3", "r1", "r2", "exit", "client"} {
		paths[name] = filepath.Join(runtimeDir, name)
		if err := os.Mkdir(paths[name], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	geoIP := filepath.Join(runtimeDir, "country.mmdb")
	writeM12CountryDatabase(t, geoIP, "198.51.100.0/24", "DE")

	bootstrapProcess := lab.startChild("m12-bootstrap", topology.RouterNamespace, irviod,
		"--data-dir", paths["bootstrap"], "--listen-port", "45100", "--print-node-info")
	bootstrapInfo := waitNodeInfo(t, lab, bootstrapProcess, "m12-bootstrap.stdout.log", 1)
	bootstrap := selectMultiaddr(t, bootstrapInfo, "ip4", topology.RouterClient4)

	verifierAddresses := make([]string, 0, 3)
	verifiers := make([]*childProcess, 0, 3)
	for index := 0; index < 3; index++ {
		label := fmt.Sprintf("m12-verifier-%d", index+1)
		process := lab.startChild(label, topology.InternetNamespace, irviod,
			"--data-dir", paths[fmt.Sprintf("verifier-%d", index+1)],
			"--listen-port", fmt.Sprintf("%d", 45110+index),
			"--allow-test-public-ip", "--print-node-info")
		info := waitNodeInfo(t, lab, process, label+".stdout.log", 1)
		verifierAddresses = append(verifierAddresses, selectMultiaddr(t, info, "ip4", topology.Internet4))
		verifiers = append(verifiers, process)
	}

	r1Args := m12RendezvousArgs(paths["r1"], bootstrap, "45101", geoIP, verifierAddresses)
	r1Args = append(r1Args, "--rendezvous-descriptor-ttl", "6s", "--rendezvous-receipt-ttl", "6s")
	r1 := lab.startChild("m12-r1", topology.ExitANamespace, irviod, r1Args...)
	r1Info := waitNodeInfo(t, lab, r1, "m12-r1.stdout.log", 1)
	_ = waitM11Log(t, lab, r1, "m12-r1.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 15*time.Second, nil)
	r1Address := selectMultiaddr(t, r1Info, "ip4", topology.ExitAWAN4)

	r2Args := m12RendezvousArgs(paths["r2"], bootstrap, "45102", geoIP, verifierAddresses)
	r2Args = append(r2Args, "--rendezvous-descriptor-ttl", "6s", "--rendezvous-receipt-ttl", "6s")
	r2 := lab.startChild("m12-r2", topology.InternetNamespace, irviod, r2Args...)
	r2Info := waitNodeInfo(t, lab, r2, "m12-r2.stdout.log", 1)
	r2Registry := waitM11Log(t, lab, r2, "m12-r2.stderr.log", "RENDEZVOUS_REGISTRY_CREATED", 15*time.Second, nil)
	r2Service := waitM11Log(t, lab, r2, "m12-r2.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 15*time.Second, nil)
	r2Address := selectMultiaddr(t, r2Info, "ip4", topology.Internet4)

	exitArgs := m10FailoverExitArgs(paths["exit"], bootstrap, "45103", "41303", topology.ExitBWANIf, topology.ExitBWAN4)
	exitArgs = append(exitArgs,
		"--rendezvous-v1", "--rendezvous-peer", r2Address,
		"--rendezvous-descriptor-ttl", "6s", "--rendezvous-receipt-ttl", "6s",
		"--lease-ttl", "3s", "--lease-sweep", "100ms",
		"--lease-renew-lead", "1s", "--lease-renew-retry", "200ms")
	exitProcess := lab.startChild("m12-exit", topology.ExitBNamespace, irviod, exitArgs...)
	exitInfo := waitNodeInfo(t, lab, exitProcess, "m12-exit.stdout.log", 1)
	registration := waitM11Log(t, lab, exitProcess, "m12-exit.stderr.log", "RENDEZVOUS_REGISTRATIONS", 15*time.Second, func(event map[string]any) bool {
		active, _ := event["active"].(float64)
		return active == 1
	})
	if servers := fmt.Sprint(registration["servers"]); !strings.Contains(servers, r2Info.PeerID) || strings.Contains(servers, r1Info.PeerID) {
		t.Fatalf("Exit registration escaped configured owner: %v", registration)
	}

	clientArgs := []string{
		"--data-dir", paths["client"], "--listen-port", "45104", "--bootstrap", bootstrap,
		"--runtime", "--connect", "DE", "--udp-port", "41304", "--tun-name", "hydra0",
		"--bypass-prefix", netip.PrefixFrom(topology.Client4, 24).Masked().String(),
		"--lease-ttl", "3s", "--lease-sweep", "100ms", "--lease-renew-lead", "1s", "--lease-renew-retry", "200ms",
		"--rendezvous-v1", "--rendezvous-peer", r1Address,
		"--rendezvous-descriptor-ttl", "6s", "--rendezvous-receipt-ttl", "6s",
		"--local-api-socket", filepath.Join(paths["client"], "control.sock"), "--local-api-group", "root",
		"--print-node-info", "--print-runtime-events",
	}
	clientProcess := lab.startChild("m12-client", topology.ClientNamespace, irviod, clientArgs...)
	clientInfo := waitNodeInfo(t, lab, clientProcess, "m12-client.stdout.log", 1)
	connected := waitM10RuntimeStatus(t, lab, clientProcess, "m12-client.stderr.log", 60*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == exitInfo.PeerID && status.TunnelLeaseID != "" ||
			status.State == irvioruntime.StateNoExit
	})
	if connected.State == irvioruntime.StateNoExit {
		iceRules := lab.inNamespace(topology.RouterNamespace, "nft", "-j", "list", "table", "inet", "m12_ice_path")
		outerIngress := lab.inNamespace(topology.ClientNamespace, "nft", "-j", "list", "table", "inet", "m12_outer_ingress")
		clientControl := lab.inNamespace(topology.ClientNamespace, "nft", "-j", "list", "table", "inet", "m12_tunnel_control")
		exitControl := lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "table", "inet", "m12_tunnel_control")
		t.Fatalf("brokered candidate failed before promotion: client_to_exit=%d exit_to_client=%d outer_prerouting=%d outer_input=%d client_control_out=%d client_control_in=%d exit_control_out=%d exit_control_in=%d status=%+v",
			requireCounter(t, iceRules, "ice-client-exit", 0), requireCounter(t, iceRules, "ice-exit-client", 0),
			requireCounter(t, outerIngress, "outer-prerouting", 0), requireCounter(t, outerIngress, "outer-input", 0),
			requireCounter(t, clientControl, "tunnel-control-hydra", 0), requireCounter(t, clientControl, "tunnel-control-in", 0),
			requireCounter(t, exitControl, "tunnel-control-hydra", 0), requireCounter(t, exitControl, "tunnel-control-in", 0), connected)
	}
	if value := strings.TrimSpace(lab.inNamespace(topology.ClientNamespace, "sysctl", "-n", "net.ipv4.conf.all.src_valid_mark")); value != "1" {
		t.Fatalf("ProtectedMode did not enable marked reverse-path validation: %q", value)
	}
	protection := lab.inNamespace(topology.ClientNamespace, "nft", "-j", "list", "table", "inet", "hydra_protect")
	requireCounter(t, protection, "mark-inbound-bypass", 1)
	requireCounter(t, protection, "mark-inbound-tunnel-control", 1)
	if _, err := time.Parse(time.RFC3339Nano, connected.TunnelLeaseExpiry); err != nil {
		t.Fatalf("parse initial M12 lease expiry: %v", err)
	}
	probe := runM7Probe(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.Internet4, 18080))
	assertM7Probe(t, probe, topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))

	served := waitM11Log(t, lab, r1, "m12-r1.stderr.log", "RENDEZVOUS_CANDIDATES_SERVED", 5*time.Second, func(event map[string]any) bool {
		local, _ := event["local"].(float64)
		remote, _ := event["remote"].(float64)
		return local == 0 && remote >= 1
	})
	if returned, _ := served["returned"].(float64); returned < 1 {
		t.Fatalf("ingress Rendezvous returned no remotely sampled candidate: %v", served)
	}
	_ = waitM11Log(t, lab, r2, "m12-r2.stderr.log", "RENDEZVOUS_LOCAL_SAMPLE_SERVED", 5*time.Second, func(event map[string]any) bool {
		returned, _ := event["returned"].(float64)
		return returned >= 1
	})
	directClient := lab.inNamespace(topology.ClientNamespace, "nft", "-j", "list", "table", "inet", "m12_no_direct")
	directExit := lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "table", "inet", "m12_no_direct")
	_ = requireCounter(t, directClient, "client-exit-direct-tcp", 0)
	_ = requireCounter(t, directExit, "exit-client-direct-tcp", 0)
	routes := lab.inNamespace(topology.ClientNamespace, "ip", "route", "show", "table", "51822")
	rules := lab.inNamespace(topology.ClientNamespace, "ip", "rule", "show")
	if !strings.Contains(routes, "10.77.0.1") || !strings.Contains(routes, "unreachable default") ||
		!strings.Contains(rules, "fwmark 0x48595443 lookup 51822") {
		t.Fatalf("TUNNEL_ONLY route ownership incomplete: routes=%s rules=%s", routes, rules)
	}
	lab.installNFT(topology.ClientNamespace, "m12-route-fault", fmt.Sprintf(`table inet m12_route_fault {
	chain output { type filter hook output priority 100; policy accept;
	 meta mark 0x48595443 oifname "hydra0" counter comment "tunnel-control-hydra"
	 meta mark 0x48595443 oifname "%s" counter comment "tunnel-control-physical"
	}
}`, topology.ClientIf))
	controlEndpoint := netip.MustParseAddrPort("10.77.0.1:51822")
	runM12MarkedTCPConnect(t, lab, topology.ClientNamespace, controlEndpoint, true)
	lab.inNamespace(topology.ClientNamespace, "ip", "route", "del", "table", "51822", "10.77.0.1/32", "dev", "hydra0")
	runM12MarkedTCPConnect(t, lab, topology.ClientNamespace, controlEndpoint, false)
	routeFaultCounters := lab.inNamespace(topology.ClientNamespace, "nft", "-j", "list", "table", "inet", "m12_route_fault")
	if leaked := requireCounter(t, routeFaultCounters, "tunnel-control-physical", 0); leaked != 0 {
		t.Fatalf("TUNNEL_ONLY socket fell through to physical WAN after exact route removal: %d packets", leaked)
	}
	lab.inNamespace(topology.ClientNamespace, "ip", "route", "add", "table", "51822", "10.77.0.1/32", "dev", "hydra0")
	runM12MarkedTCPConnect(t, lab, topology.ClientNamespace, controlEndpoint, true)
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.Internet4, 18080)),
		topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))

	// SIGKILL must leave protection intact; restart adopts it before replacing the abandoned session.
	killM10Child(t, clientProcess)
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.Internet4, 18080), topology.ClientIf, 0, false)
	clientProcess = lab.startChild("m12-client-restarted", topology.ClientNamespace, irviod, clientArgs...)
	restartedClientInfo := waitNodeInfo(t, lab, clientProcess, "m12-client-restarted.stdout.log", 1)
	if restartedClientInfo.PeerID != clientInfo.PeerID {
		t.Fatalf("Client Irvio identity changed across crash adoption: before=%s after=%s", clientInfo.PeerID, restartedClientInfo.PeerID)
	}
	restartedClient := waitM10RuntimeStatus(t, lab, clientProcess, "m12-client-restarted.stderr.log", 60*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == exitInfo.PeerID &&
			status.TunnelLeaseID != "" && status.ProtectionAdopted
	})
	if restartedClient.TunnelLeaseID == connected.TunnelLeaseID {
		t.Fatalf("crash restart reused abandoned lease ownership: before=%+v after=%+v", connected, restartedClient)
	}
	connected = restartedClient
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.Internet4, 18080)),
		topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))

	localSocket := filepath.Join(paths["client"], "control.sock")
	if t.Name() == "TestP2_IrvioDartClientControlsFrozenM12Runtime" {
		uiClient := assertNormalUserExecutable(t, "HYDRA_P2_UI_CLIENT")
		oldUILease := connected.TunnelLeaseID
		output := lab.inNamespace(topology.ClientNamespace, uiClient,
			"--socket", localSocket, "cycle", "DE")
		if !strings.Contains(output, "cycle=ok state=connected country=DE") {
			t.Fatalf("P2 Dart client did not complete Local API cycle: %q", output)
		}
		connected = waitM10RuntimeStatus(t, lab, clientProcess, "m12-client-restarted.stderr.log", 10*time.Second, func(status m10RuntimeStatus) bool {
			return status.State == irvioruntime.StateConnected && status.ActivePeer == exitInfo.PeerID &&
				status.TunnelLeaseID != "" && status.TunnelLeaseID != oldUILease
		})
		assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.Internet4, 18080)),
			topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))
	}
	apiContext, apiCancel := context.WithTimeout(context.Background(), 90*time.Second)
	gui, err := localapi.Dial(apiContext, localSocket)
	if err != nil {
		apiCancel()
		t.Fatalf("connect P1 local client after stale-socket adoption: %v", err)
	}
	events, err := gui.SubscribeEvents(apiContext)
	if err != nil {
		apiCancel()
		t.Fatalf("subscribe P1 events: %v", err)
	}
	if status := waitP1ProductState(t, events, localv1.ConnectionState_CONNECTION_STATE_CONNECTED, 5*time.Second); status.CountryCode != "DE" {
		apiCancel()
		t.Fatalf("initial P1 status has country %q", status.CountryCode)
	}
	oldLease := connected.TunnelLeaseID
	if err := gui.Disconnect(apiContext); err != nil {
		apiCancel()
		t.Fatalf("P1 DISCONNECT: %v", err)
	}
	_ = waitP1ProductState(t, events, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTING, 5*time.Second)
	_ = waitP1ProductState(t, events, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED, 15*time.Second)
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.Internet4, 18080), topology.ClientIf, 0, true)
	if err := gui.Connect(apiContext, "DE"); err != nil {
		apiCancel()
		t.Fatalf("P1 CONNECT(DE): %v", err)
	}
	_ = waitP1ProductState(t, events, localv1.ConnectionState_CONNECTION_STATE_CONNECTING, 5*time.Second)
	productConnected := waitP1ProductState(t, events, localv1.ConnectionState_CONNECTION_STATE_CONNECTED, 60*time.Second)
	if productConnected.CountryCode != "DE" || productConnected.Connection.GetExitCountryCode() != "DE" {
		apiCancel()
		t.Fatalf("P1 CONNECT returned wrong product status: %v", productConnected)
	}
	connected = waitM10RuntimeStatus(t, lab, clientProcess, "m12-client-restarted.stderr.log", 10*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == exitInfo.PeerID &&
			status.TunnelLeaseID != "" && status.TunnelLeaseID != oldLease
	})
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.Internet4, 18080)),
		topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))
	if err := gui.Close(); err != nil {
		apiCancel()
		t.Fatalf("kill P1 GUI client: %v", err)
	}
	apiCancel()

	oldIncarnation, _ := r2Registry["server_incarnation"].(float64)
	initialLive, _ := r2Registry["live_registrations"].(float64)
	if oldIncarnation == 0 || initialLive != 0 {
		t.Fatalf("owner Rendezvous did not create an initially empty registry: %v service=%v", r2Registry, r2Service)
	}
	killM10Child(t, r2)
	_ = waitM11Log(t, lab, exitProcess, "m12-exit.stderr.log", "RENDEZVOUS_REGISTRATIONS", 20*time.Second, func(event map[string]any) bool {
		active, _ := event["active"].(float64)
		return active == 0
	})
	r2 = lab.startChild("m12-r2-restarted", topology.InternetNamespace, irviod, r2Args...)
	restartedR2Info := waitNodeInfo(t, lab, r2, "m12-r2-restarted.stdout.log", 1)
	if restartedR2Info.PeerID != r2Info.PeerID {
		t.Fatalf("owner Rendezvous identity changed across restart: before=%s after=%s", r2Info.PeerID, restartedR2Info.PeerID)
	}
	restartedRegistry := waitM11Log(t, lab, r2, "m12-r2-restarted.stderr.log", "RENDEZVOUS_REGISTRY_CREATED", 15*time.Second, nil)
	restartedService := waitM11Log(t, lab, r2, "m12-r2-restarted.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 15*time.Second, nil)
	newIncarnation, _ := restartedRegistry["server_incarnation"].(float64)
	liveAtStart, _ := restartedRegistry["live_registrations"].(float64)
	if newIncarnation == 0 || newIncarnation == oldIncarnation || liveAtStart != 0 {
		t.Fatalf("owner restart did not start a fresh empty registry incarnation: before=%v after=%v service=%v", r2Registry, restartedRegistry, restartedService)
	}
	_ = waitM11Log(t, lab, r2, "m12-r2-restarted.stderr.log", "RENDEZVOUS_REGISTRATION_ACCEPTED", 20*time.Second, func(event map[string]any) bool {
		fresh, _ := event["fresh_registrations"].(float64)
		return fresh == 1
	})
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.Internet4, 18080)),
		topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))
	reconnectContext, reconnectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	restartedGUI, err := localapi.Dial(reconnectContext, localSocket)
	if err != nil {
		reconnectCancel()
		t.Fatalf("restart P1 GUI client: %v", err)
	}
	restoredStatus, err := restartedGUI.GetStatus(reconnectContext)
	if err != nil || restoredStatus.State != localv1.ConnectionState_CONNECTION_STATE_CONNECTED || restoredStatus.CountryCode != "DE" {
		_ = restartedGUI.Close()
		reconnectCancel()
		t.Fatalf("P1 GET_STATUS after GUI restart: status=%v err=%v", restoredStatus, err)
	}
	_ = restartedGUI.Close()
	reconnectCancel()
	beforeRendezvousLoss := waitM10RuntimeStatus(t, lab, clientProcess, "m12-client-restarted.stderr.log", 5*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == exitInfo.PeerID &&
			status.TunnelLeaseID == connected.TunnelLeaseID
	})
	beforeLossExpiry, err := time.Parse(time.RFC3339Nano, beforeRendezvousLoss.TunnelLeaseExpiry)
	if err != nil {
		t.Fatalf("parse pre-Rendezvous-loss lease expiry: %v", err)
	}

	killM10Child(t, r1)
	killM10Child(t, r2)
	renewed := waitM10RuntimeStatus(t, lab, clientProcess, "m12-client-restarted.stderr.log", 20*time.Second, func(status m10RuntimeStatus) bool {
		expiry, parseErr := time.Parse(time.RFC3339Nano, status.TunnelLeaseExpiry)
		return parseErr == nil && status.State == irvioruntime.StateConnected && status.ActivePeer == exitInfo.PeerID &&
			status.TunnelLeaseID == connected.TunnelLeaseID && expiry.After(beforeLossExpiry.Add(12*time.Second))
	})
	if renewed.SessionGeneration != connected.SessionGeneration || renewed.AWGKeyEpoch != connected.AWGKeyEpoch {
		t.Fatalf("Rendezvous loss replaced healthy direct session: before=%+v after=%+v", connected, renewed)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.Internet4, 18080)),
		topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))
	for namespace, label := range map[string]string{
		topology.ClientNamespace: "client", topology.ExitBNamespace: "exit",
	} {
		counters := lab.inNamespace(namespace, "nft", "-j", "list", "table", "inet", "m12_tunnel_control")
		requireCounter(t, counters, "tunnel-control-hydra", 1)
		if leaked := requireCounter(t, counters, "tunnel-control-wan", 0); leaked != 0 {
			t.Fatalf("%s TUNNEL_ONLY traffic escaped to physical WAN: %d packets", label, leaked)
		}
	}

	// Test key rotation over tunnel Noise with Rendezvous dead and direct WAN control blocked.
	if err := syscall.Kill(-exitProcess.command.Process.Pid, syscall.SIGHUP); err != nil {
		t.Fatalf("request direct AWG rotation: %v", err)
	}
	waitM12RotationComplete(t, lab, exitProcess, clientProcess, renewed.AWGKeyEpoch+1, 20*time.Second)
	rotated := waitM10RuntimeStatus(t, lab, clientProcess, "m12-client-restarted.stderr.log", 15*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == exitInfo.PeerID &&
			status.TunnelLeaseID == renewed.TunnelLeaseID && status.AWGKeyEpoch == renewed.AWGKeyEpoch+1
	})
	if rotated.SessionGeneration != renewed.SessionGeneration || rotated.MuxPort != renewed.MuxPort {
		t.Fatalf("direct AWG rotation replaced ICE/Mux ownership: before=%+v after=%+v", renewed, rotated)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", netip.AddrPortFrom(topology.Internet4, 18080)),
		topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))

	if err := stopM10Child(clientProcess, 12*time.Second); err != nil {
		t.Fatalf("stop M12 Client: %v", err)
	}
	if value := strings.TrimSpace(lab.inNamespace(topology.ClientNamespace, "sysctl", "-n", "net.ipv4.conf.all.src_valid_mark")); value != "0" {
		t.Fatalf("ProtectedMode did not restore marked reverse-path validation: %q", value)
	}
	if err := stopM10Child(exitProcess, 12*time.Second); err != nil {
		t.Fatalf("stop M12 Exit: %v", err)
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
	for _, namespace := range []string{
		topology.ClientNamespace, topology.RouterNamespace, topology.ExitANamespace,
		topology.ExitBNamespace, topology.InternetNamespace,
	} {
		dnsCounters := lab.inNamespace(namespace, "nft", "-j", "list", "table", "inet", "m12_no_dns")
		if udp := requireCounter(t, dnsCounters, "dns-udp-attempt", 0); udp != 0 {
			t.Fatalf("Irvio attempted UDP DNS in literal-IP M12 lifecycle: namespace=%s packets=%d", namespace, udp)
		}
		if tcp := requireCounter(t, dnsCounters, "dns-tcp-attempt", 0); tcp != 0 {
			t.Fatalf("Irvio attempted TCP DNS in literal-IP M12 lifecycle: namespace=%s packets=%d", namespace, tcp)
		}
	}
	lab.inNamespace(topology.ClientNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ExitBNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
}

func waitP1ProductState(t *testing.T, events <-chan *localv1.Event, wanted localv1.ConnectionState, maximum time.Duration) *localv1.Status {
	t.Helper()
	timer := time.NewTimer(maximum)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatalf("wait for P1 product state %s", wanted)
		case event, ok := <-events:
			if !ok {
				t.Fatalf("P1 event stream closed before state %s", wanted)
			}
			status := event.GetStatusChanged().GetStatus()
			if status != nil && status.State == wanted {
				return status
			}
		}
	}
}

func waitM12RotationComplete(t *testing.T, lab *lab, exit, client *childProcess, epoch uint64, maximum time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, item := range []struct {
			file    string
			success string
			failure string
		}{
			{file: "m12-exit.stderr.log", success: "AWG_DIRECT_ROTATION_COMPLETE", failure: "AWG_DIRECT_ROTATION_FAILED"},
			{file: "m12-client-restarted.stderr.log", success: "AWG_DIRECT_ROTATION_CLIENT_COMMITTED", failure: "AWG_DIRECT_ROTATION_CLIENT_FAILED"},
		} {
			data, _ := os.ReadFile(filepath.Join(lab.artifactDir, item.file))
			for _, line := range strings.Split(string(data), "\n") {
				var event map[string]any
				if json.Unmarshal([]byte(line), &event) != nil {
					continue
				}
				observed, _ := event["key_epoch"].(float64)
				if uint64(observed) != epoch {
					continue
				}
				if event["msg"] == item.failure {
					t.Fatalf("direct AWG rotation failed: %v", event)
				}
				if event["msg"] == item.success && item.success == "AWG_DIRECT_ROTATION_COMPLETE" {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for direct AWG rotation epoch %d: %v", epoch, ctx.Err())
		case <-exit.done:
			t.Fatal("Exit exited during direct AWG rotation")
		case <-client.done:
			t.Fatal("Client exited during direct AWG rotation")
		case <-ticker.C:
		}
	}
}

func m12RendezvousArgs(dataDir, bootstrap, port, geoIP string, verifiers []string) []string {
	args := []string{
		"--data-dir", dataDir, "--listen-port", port, "--bootstrap", bootstrap,
		"--public-node", "--public-control-port4", port, "--public-proof-ttl", "1m",
		"--public-reverify-lead", "20s", "--public-retry", "1s", "--allow-test-public-ip",
		"--rendezvous-v1", "--rendezvous-service", "--geoip-db", geoIP, "--print-node-info",
	}
	for _, verifier := range verifiers {
		args = append(args, "--reachability-verifier", verifier)
	}
	return args
}

func writeM12CountryDatabase(t *testing.T, path, prefix, country string) {
	writeM12CountryDatabaseRecords(t, path, map[string]string{prefix: country})
}

func writeM12CountryDatabaseRecords(t *testing.T, path string, records map[string]string) {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch: time.Now().Unix(), DatabaseType: "GeoIP2-Country-Irvio-M12-Test",
		IPVersion: 6, RecordSize: 24, IncludeReservedNetworks: true,
		Description: map[string]string{"en": "Synthetic Irvio M12 acceptance fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for prefix, country := range records {
		_, network, err := net.ParseCIDR(prefix)
		if err != nil {
			t.Fatal(err)
		}
		if err := tree.Insert(network, mmdbtype.Map{"country": mmdbtype.Map{"iso_code": mmdbtype.String(country)}}); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.WriteTo(file); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func runM12MarkedTCPConnect(t *testing.T, lab *lab, namespace string, remote netip.AddrPort, wantSuccess bool) {
	t.Helper()
	resultPath := filepath.Join(lab.artifactDir, "m12-marked-connect-"+strconv.FormatUint(m7ProbeCounter.Add(1), 10)+".json")
	_, err := runM8Command(
		"ip", "netns", "exec", namespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
		"--mode", "marked-tcp-connect", "--remote", remote.String(),
		"--mark", strconv.FormatUint(uint64(linuxroute.IrvioTunnelControlMark), 10), "--result", resultPath,
	)
	if !wantSuccess {
		if err == nil {
			t.Fatalf("marked TCP connect unexpectedly succeeded without exact TUNNEL_ONLY route: %s", remote)
		}
		return
	}
	if err != nil {
		t.Fatalf("marked TCP connect through exact TUNNEL_ONLY route: %v", err)
	}
	var result m9ProbeReport
	if err := readJSON(resultPath, &result); err != nil {
		t.Fatal(err)
	}
	if !result.ProbeReachable || result.Remote != remote.String() || result.SocketMark != linuxroute.IrvioTunnelControlMark {
		t.Fatalf("invalid marked TCP route oracle: %+v", result)
	}
}
