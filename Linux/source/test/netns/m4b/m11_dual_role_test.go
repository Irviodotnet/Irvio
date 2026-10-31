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

	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
)

func TestM11_IrviodDualRolePublicControlAndNoImplicitMultiHop(t *testing.T) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "m11-dual-role")
	topology := buildM10FailoverTopology(lab)
	client2Namespace := lab.namespace("client-b2")
	client2If, routerClient2If := lab.interfaceName('d'), lab.interfaceName('z')
	client2, routerClient2 := netip.MustParseAddr("198.18.93.1"), netip.MustParseAddr("198.18.93.254")
	lab.createVeth(client2Namespace, client2If, topology.RouterNamespace, routerClient2If)
	lab.inNamespace(client2Namespace, "ip", "link", "set", "lo", "up")
	for _, item := range []struct{ namespace, iface, address string }{
		{client2Namespace, client2If, client2.String() + "/24"},
		{topology.RouterNamespace, routerClient2If, routerClient2.String() + "/24"},
	} {
		lab.inNamespace(item.namespace, "ip", "link", "set", item.iface, "up")
		lab.inNamespace(item.namespace, "ip", "addr", "add", item.address, "dev", item.iface)
	}
	lab.inNamespace(client2Namespace, "ip", "route", "add", "default", "via", routerClient2.String(), "dev", client2If)
	client2Prefix := netip.PrefixFrom(client2, 24).Masked().String()
	lab.inNamespace(topology.ExitANamespace, "ip", "route", "add", client2Prefix, "via", topology.RouterA4.String(), "dev", topology.ExitADataIf)
	lab.inNamespace(topology.ExitBNamespace, "ip", "route", "add", client2Prefix, "via", topology.RouterB4.String(), "dev", topology.ExitBDataIf)
	lab.inNamespace(topology.InternetNamespace, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1")
	lab.inNamespace(topology.InternetNamespace, "ip", "route", "add", netip.PrefixFrom(topology.ExitAData4, 24).Masked().String(), "via", topology.ExitAWAN4.String(), "dev", topology.InternetAIf)
	lab.inNamespace(topology.InternetNamespace, "ip", "route", "add", netip.PrefixFrom(topology.ExitBData4, 24).Masked().String(), "via", topology.ExitBWAN4.String(), "dev", topology.InternetBIf)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.InternetA4.String()+"/32", "via", topology.ExitAData4.String(), "dev", topology.RouterAIf)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.InternetB4.String()+"/32", "via", topology.ExitBData4.String(), "dev", topology.RouterBIf)
	lab.installNFT(topology.InternetNamespace, "m11-control-plane-isolation", fmt.Sprintf(`table inet m11_control_isolation {
 chain forward { type filter hook forward priority filter; policy accept;
  ip daddr %s tcp dport 44003 drop comment "block-exit-c-data-control"
 }
}`, topology.ExitBWAN4))
	internet, internetRelease, _ := startM7InternetServer(t, lab, m7Topology{
		InternetNamespace: topology.InternetNamespace, Internet4: topology.Internet4,
	}, "--tcp-ports", "25,443")

	runtimeDir := lab.tempDir("m11-dual-role")
	paths := map[string]string{}
	for _, name := range []string{"bootstrap", "exit-c", "node-a", "client-b", "client-b2", "dht-probe", "active-control", "outage-control", "verifier-1", "verifier-2", "verifier-3"} {
		paths[name] = filepath.Join(runtimeDir, name)
		if err := os.Mkdir(paths[name], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bootstrapProcess := lab.startChild("m11-bootstrap", topology.RouterNamespace, irviod,
		"--data-dir", paths["bootstrap"], "--listen-port", "44000", "--print-node-info")
	bootstrapInfo := waitNodeInfo(t, lab, bootstrapProcess, "m11-bootstrap.stdout.log", 1)
	bootstrap := selectMultiaddr(t, bootstrapInfo, "ip4", topology.RouterClient4)

	verifierAddresses := make([]string, 0, 3)
	verifiers := make([]*childProcess, 0, 3)
	for index := 0; index < 3; index++ {
		label := fmt.Sprintf("m11-dual-verifier-%d", index+1)
		process := lab.startChild(label, topology.InternetNamespace, irviod,
			"--data-dir", paths[fmt.Sprintf("verifier-%d", index+1)],
			"--listen-port", fmt.Sprintf("%d", 44010+index),
			"--public-proof-ttl", "1m", "--public-reverify-lead", "20s",
			"--allow-test-public-ip", "--print-node-info")
		info := waitNodeInfo(t, lab, process, label+".stdout.log", 1)
		verifierAddresses = append(verifierAddresses, selectMultiaddr(t, info, "ip4", topology.InternetA4))
		verifiers = append(verifiers, process)
	}

	exitC := lab.startChild("m11-exit-c", topology.ExitBNamespace, irviod,
		m11ExitArgs(paths["exit-c"], bootstrap, "NL", "44003", "41203", topology.ExitBWANIf,
			topology.ExitBWAN4, "10.77.0.1/24", "10.77.0.0/24")...)
	exitCInfo := waitNodeInfo(t, lab, exitC, "m11-exit-c.stdout.log", 1)

	newAWAN := netip.MustParseAddr("203.0.113.3")
	nodeAArgs := m11ExitArgs(paths["node-a"], bootstrap, "DE", "44002", "41202", topology.ExitAWANIf,
		topology.ExitAWAN4, "10.88.0.1/24", "10.88.0.0/24")
	nodeAArgs = append(nodeAArgs,
		"--connect", "NL", "--protected-mode", "--public-node", "--public-control-port4", "44002",
		"--public-proof-ttl", "1m", "--public-reverify-lead", "20s", "--public-retry", "1s",
		"--allow-test-public-ip", "--exit-management-prefix", newAWAN.String()+"/32",
		"--underlay-target", selectMultiaddr(t, exitCInfo, "ip4", topology.ExitBData4))
	for _, prefix := range []netip.Prefix{
		netip.PrefixFrom(topology.Client4, 24).Masked(),
		netip.PrefixFrom(topology.ExitAData4, 24).Masked(),
		netip.PrefixFrom(topology.ExitBData4, 24).Masked(),
		netip.PrefixFrom(client2, 24).Masked(),
	} {
		nodeAArgs = append(nodeAArgs, "--bypass-prefix", prefix.String())
	}
	for _, address := range verifierAddresses {
		nodeAArgs = append(nodeAArgs, "--reachability-verifier", address)
	}
	nodeA := lab.startChild("m11-node-a", topology.ExitANamespace, irviod, nodeAArgs...)
	nodeAInfo := waitNodeInfo(t, lab, nodeA, "m11-node-a.stdout.log", 1)
	aStatus := waitM10RuntimeStatus(t, lab, nodeA, "m11-node-a.stderr.log", 45*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == exitCInfo.PeerID
	})
	_ = waitM11Log(t, lab, nodeA, "m11-node-a.stderr.log", "PUBLIC_REACHABILITY_VERIFIED", 15*time.Second, nil)
	dhtProbe := lab.startChild("m11-public-dht-probe", topology.InternetNamespace, irviod,
		"--data-dir", paths["dht-probe"], "--listen-port", "44020",
		"--bootstrap", selectMultiaddr(t, nodeAInfo, "ip4", topology.ExitAWAN4),
		"--find-exits", "DE", "--find-once")
	dhtContext, cancelDHT := context.WithTimeout(context.Background(), 15*time.Second)
	if err := dhtProbe.wait(dhtContext); err != nil {
		cancelDHT()
		t.Fatalf("fresh inbound public DHT/provider request failed: %v", err)
	}
	cancelDHT()
	dhtOutput, err := os.ReadFile(filepath.Join(lab.artifactDir, "m11-public-dht-probe.stdout.log"))
	if err != nil || !strings.Contains(string(dhtOutput), nodeAInfo.PeerID) {
		t.Fatalf("public DHT/provider response did not authenticate Node A: bytes=%d err=%v", len(dhtOutput), err)
	}

	clientB := lab.startChild("m11-client-b", topology.ClientNamespace, irviod,
		"--data-dir", paths["client-b"], "--listen-port", "44001", "--bootstrap", bootstrap,
		"--runtime", "--connect", "DE", "--udp-port", "41201", "--tun-name", "hydra0",
		"--bypass-prefix", netip.PrefixFrom(topology.Client4, 24).Masked().String(),
		"--lease-ttl", "15s", "--lease-sweep", "100ms", "--lease-renew-lead", "5s", "--lease-renew-retry", "1s",
		"--print-node-info", "--print-runtime-events")
	_ = waitNodeInfo(t, lab, clientB, "m11-client-b.stdout.log", 1)
	bStatus := waitM10RuntimeStatus(t, lab, clientB, "m11-client-b.stderr.log", 45*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == nodeAInfo.PeerID
	})
	clientB2 := lab.startChild("m11-client-b2", client2Namespace, irviod,
		"--data-dir", paths["client-b2"], "--listen-port", "44004", "--bootstrap", bootstrap,
		"--runtime", "--connect", "DE", "--udp-port", "41204", "--tun-name", "hydra0",
		"--bypass-prefix", client2Prefix,
		"--lease-ttl", "15s", "--lease-sweep", "100ms", "--lease-renew-lead", "5s", "--lease-renew-retry", "1s",
		"--print-node-info", "--print-runtime-events")
	_ = waitNodeInfo(t, lab, clientB2, "m11-client-b2.stdout.log", 1)
	b2Status := waitM10RuntimeStatus(t, lab, clientB2, "m11-client-b2.stderr.log", 45*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == nodeAInfo.PeerID
	})
	if aStatus.TunnelLeaseID == "" || bStatus.TunnelLeaseID == "" || aStatus.TunnelLeaseID == bStatus.TunnelLeaseID {
		t.Fatalf("dual-role leases are incomplete or aliased: A=%+v B=%+v", aStatus, bStatus)
	}
	if b2Status.TunnelLeaseID == "" || b2Status.TunnelLeaseID == bStatus.TunnelLeaseID || b2Status.TunnelLeaseID == aStatus.TunnelLeaseID {
		t.Fatalf("second downstream lease is incomplete or aliased: A=%+v B1=%+v B2=%+v", aStatus, bStatus, b2Status)
	}

	target := netip.AddrPortFrom(topology.Internet4, 18080)
	aRoute := lab.inNamespace(topology.ExitANamespace, "ip", "route", "get", topology.Internet4.String())
	if !strings.Contains(aRoute, "dev hydra0") || !strings.Contains(aRoute, "src 10.77.0.2") {
		t.Fatalf("Node A own traffic lacks upstream tunnel/lease source: %s", aRoute)
	}
	lab.installNFT(topology.ExitANamespace, "m11-node-a-awg-wire", fmt.Sprintf(`table inet m11_awg_wire {
 chain output { type filter hook output priority filter; policy accept;
  udp sport 41202 udp dport 41203 counter comment "a-to-c-awg"
 }
 chain input { type filter hook input priority filter; policy accept;
  udp sport { 41201, 41204 } udp dport 41202 counter comment "downstream-awg-input"
 }
 chain forward { type filter hook forward priority filter; policy accept;
  iifname "hydra0" oifname "%s" counter comment "downstream-to-wan"
  iifname "%s" oifname "hydra0" counter comment "wan-to-downstream"
 }
}`, topology.ExitAWANIf, topology.ExitAWANIf))
	lab.installNFT(topology.ExitBNamespace, "m11-exit-c-awg-wire", fmt.Sprintf(`table inet m11_awg_wire {
 chain input { type filter hook input priority filter; policy accept;
  udp sport 41202 udp dport 41203 counter comment "a-to-c-awg"
 }
}`))
	aProbe, err := tryM7Probe(t, lab, topology.ExitANamespace, "http", target)
	if err != nil {
		t.Fatalf("Node A upstream HTTP: %v; A wire=%s; C wire=%s; A nft=%s; C nft=%s", err,
			lab.inNamespace(topology.ExitANamespace, "nft", "-j", "list", "table", "inet", "m11_awg_wire"),
			lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "table", "inet", "m11_awg_wire"),
			lab.inNamespace(topology.ExitANamespace, "nft", "-j", "list", "ruleset"),
			lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "ruleset"))
	}
	assertM7Probe(t, aProbe, topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))
	assertM7Probe(t, runM7Probe(t, lab, topology.ExitANamespace, "tcp",
		netip.AddrPortFrom(topology.Internet4, 443)), topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))
	if _, err := tryM7Probe(t, lab, topology.ExitANamespace, "tcp",
		netip.AddrPortFrom(topology.Internet4, 25), "--probe-timeout", "350ms"); err == nil {
		t.Fatal("dual-role Node A own TCP/25 bypassed its client safety policy")
	}
	aClientPolicy := lab.inNamespace(topology.ExitANamespace, "nft", "-j", "list", "table", "inet", "hydra_protect")
	requireCounter(t, aClientPolicy, "reject-safety-v0-tcp", 1)
	cRulesBefore := lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	cNATBefore := requireCounter(t, cRulesBefore, "masquerade-ipv4", 1)
	runM11PublicDHTProbe(t, lab, irviod, topology.InternetNamespace, "m11-active-control", paths["active-control"],
		selectMultiaddr(t, nodeAInfo, "ip4", topology.ExitAWAN4), nodeAInfo.PeerID)
	cRulesAfterControl := lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	if after := requireCounter(t, cRulesAfterControl, "masquerade-ipv4", 1); after != cNATBefore {
		t.Fatalf("accepted public-control reply traversed upstream Exit C: before=%d after=%d", cNATBefore, after)
	}
	bProbe := runM7Probe(t, lab, topology.ClientNamespace, "http", target)
	assertM7Probe(t, bProbe, topology.ExitAWAN4, netip.MustParseAddr("10.88.0.2"))
	// Remove only client safety rules to prove the Exit's independent boundary over real TUN/AWG/leases.
	removeNFTCommentRules(t, lab, topology.ClientNamespace, "inet", "hydra_protect", "output_v1",
		"reject-safety-v0-tcp", "reject-safety-v0-udp")
	if _, err := tryM7Probe(t, lab, topology.ClientNamespace, "tcp",
		netip.AddrPortFrom(topology.Internet4, 25), "--probe-timeout", "350ms"); err == nil {
		t.Fatal("downstream modified-client TCP/25 bypassed Node A Exit safety policy")
	}
	aExitPolicy := lab.inNamespace(topology.ExitANamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	requireCounter(t, aExitPolicy, "drop-safety-v0-tcp", 1)
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "tcp",
		netip.AddrPortFrom(topology.Internet4, 443)), topology.ExitAWAN4, netip.MustParseAddr("10.88.0.2"))
	b2Probe := runM7Probe(t, lab, client2Namespace, "http", target)
	assertM7Probe(t, b2Probe, topology.ExitAWAN4, netip.MustParseAddr("10.88.0.3"))
	cRulesAfter := lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	if cNATAfter := requireCounter(t, cRulesAfter, "masquerade-ipv4", 1); cNATAfter != cNATBefore {
		t.Fatalf("B traffic entered A's upstream Exit C: before=%d after=%d", cNATBefore, cNATAfter)
	}
	aRules := lab.inNamespace(topology.ExitANamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	requireCounter(t, aRules, "masquerade-ipv4", 1)
	policy := lab.inNamespace(topology.ExitANamespace, "ip", "rule", "show")
	if !strings.Contains(policy, "iif hydra0 lookup 51821") {
		t.Fatalf("dual-role forwarded physical-WAN rule missing: %s", policy)
	}

	tunIfIndex := m10InterfaceIndex(t, lab, topology.ExitANamespace, "hydra0")
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", newAWAN.String()+"/32",
		"via", topology.ExitAData4.String(), "dev", topology.RouterAIf)
	runIPBatch(t, lab, topology.ExitANamespace, "m11-dual-role-migration", fmt.Sprintf(
		"addr add %s/24 dev %s\nroute replace default via %s dev %s src %s\n",
		newAWAN, topology.ExitAWANIf, topology.InternetA4, topology.ExitAWANIf, newAWAN))
	migratedA := waitM10MigratedSession(t, lab, nodeA, "m11-node-a.stderr.log", aStatus, 45*time.Second)
	if migratedA.ActivePeer != exitCInfo.PeerID || migratedA.TunnelLeaseID != aStatus.TunnelLeaseID ||
		m10InterfaceIndex(t, lab, topology.ExitANamespace, "hydra0") != tunIfIndex {
		t.Fatalf("dual-role migration changed logical upstream ownership: before=%+v after=%+v ifindex=%d",
			aStatus, migratedA, tunIfIndex)
	}
	migratedProof := waitM11Log(t, lab, nodeA, "m11-node-a.stderr.log", "PUBLIC_REACHABILITY_VERIFIED", 15*time.Second, func(event map[string]any) bool {
		return event["endpoint"] == netip.AddrPortFrom(newAWAN, 44002).String()
	})
	if migratedProof["network_generation"].(float64) <= float64(aStatus.NetworkGeneration) {
		t.Fatalf("public proof did not advance with dual-role NetworkGeneration: status=%+v proof=%v", aStatus, migratedProof)
	}
	bMigrated := waitM10RuntimeStatus(t, lab, clientB, "m11-client-b.stderr.log", 45*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == nodeAInfo.PeerID &&
			status.SessionGeneration > bStatus.SessionGeneration
	})
	b2Migrated := waitM10RuntimeStatus(t, lab, clientB2, "m11-client-b2.stderr.log", 45*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == nodeAInfo.PeerID &&
			status.SessionGeneration > b2Status.SessionGeneration
	})
	if bMigrated.TunnelLeaseID != bStatus.TunnelLeaseID || b2Migrated.TunnelLeaseID != b2Status.TunnelLeaseID {
		t.Fatalf("dual-role migration changed downstream logical leases: B1=%+v -> %+v B2=%+v -> %+v",
			bStatus, bMigrated, b2Status, b2Migrated)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ExitANamespace, "http", target),
		topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))
	cRulesBeforeMigratedDownstream := lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	cNATBeforeMigratedDownstream := requireCounter(t, cRulesBeforeMigratedDownstream, "masquerade-ipv4", 1)
	bMigratedProbe, err := tryM7Probe(t, lab, topology.ClientNamespace, "http", target)
	if err != nil {
		t.Fatalf("downstream B failed after Node A migration: %v; A rules=%s; A WAN routes=%s; A path counters=%s; B routes=%s",
			err,
			lab.inNamespace(topology.ExitANamespace, "ip", "rule", "show"),
			lab.inNamespace(topology.ExitANamespace, "ip", "route", "show", "table", "51821"),
			lab.inNamespace(topology.ExitANamespace, "nft", "-j", "list", "table", "inet", "m11_awg_wire"),
			lab.inNamespace(topology.ClientNamespace, "ip", "route", "show", "table", "all"))
	}
	assertM7Probe(t, bMigratedProbe, newAWAN, netip.MustParseAddr("10.88.0.2"))
	assertM7Probe(t, runM7Probe(t, lab, client2Namespace, "http", target), newAWAN, netip.MustParseAddr("10.88.0.3"))
	cRulesAfterMigratedDownstream := lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	if after := requireCounter(t, cRulesAfterMigratedDownstream, "masquerade-ipv4", 1); after != cNATBeforeMigratedDownstream {
		t.Fatalf("downstream traffic entered upstream Exit C after migration: before=%d after=%d",
			cNATBeforeMigratedDownstream, after)
	}

	killM10Child(t, exitC)
	runM8ProbeExpect(t, lab, topology.ExitANamespace, "http", target, topology.ExitAWANIf, 0, false)
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", target), newAWAN, netip.MustParseAddr("10.88.0.2"))
	runM11PublicDHTProbe(t, lab, irviod, topology.InternetNamespace, "m11-outage-control", paths["outage-control"],
		fmt.Sprintf("/ip4/%s/tcp/44002/p2p/%s", newAWAN, nodeAInfo.PeerID), nodeAInfo.PeerID)

	if err := stopM10Child(clientB, 12*time.Second); err != nil {
		t.Fatalf("stop downstream Client B: %v", err)
	}
	assertM7Probe(t, runM7Probe(t, lab, client2Namespace, "http", target), newAWAN, netip.MustParseAddr("10.88.0.3"))
	if status := waitM10RuntimeStatus(t, lab, clientB2, "m11-client-b2.stderr.log", 5*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == nodeAInfo.PeerID
	}); status.TunnelLeaseID != b2Status.TunnelLeaseID {
		t.Fatalf("closing B1 disturbed B2: before=%+v after=%+v", b2Status, status)
	}
	if err := stopM10Child(clientB2, 12*time.Second); err != nil {
		t.Fatalf("stop downstream Client B2: %v", err)
	}
	if err := stopM10Child(nodeA, 12*time.Second); err != nil {
		t.Fatalf("stop dual-role Node A: %v", err)
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
	lab.inNamespace(topology.ExitANamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ClientNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(client2Namespace, "test", "!", "-e", "/sys/class/net/hydra0")
}

func removeNFTCommentRules(t *testing.T, lab *lab, namespace, family, table, chain string, comments ...string) {
	t.Helper()
	rules := lab.inNamespace(namespace, "nft", "-a", "list", "chain", family, table, chain)
	for _, comment := range comments {
		removed := false
		for _, line := range strings.Split(rules, "\n") {
			if !strings.Contains(line, fmt.Sprintf(`comment "%s"`, comment)) {
				continue
			}
			fields := strings.Fields(line)
			for index := len(fields) - 2; index >= 0; index-- {
				if fields[index] != "handle" {
					continue
				}
				lab.inNamespace(namespace, "nft", "delete", "rule", family, table, chain, "handle", fields[index+1])
				removed = true
				break
			}
		}
		if !removed {
			t.Fatalf("nft rule with comment %q not found in %s", comment, rules)
		}
	}
}

func m11ExitArgs(dataDir, bootstrap, country, listenPort, udpPort, wan string, wanAddress netip.Addr, tunAddress, pool string) []string {
	return []string{
		"--data-dir", dataDir, "--listen-port", listenPort, "--bootstrap", bootstrap,
		"--runtime", "--exit", "--country", country, "--udp-port", udpPort,
		"--tun-name", "hydra0", "--exit-wan", wan, "--exit-tun-address", tunAddress,
		"--exit-ipv4-pool", pool, "--exit-management-prefix", wanAddress.String() + "/32",
		"--lease-ttl", "15s", "--lease-sweep", "100ms", "--lease-renew-lead", "5s", "--lease-renew-retry", "1s",
		"--print-node-info", "--print-runtime-events",
	}
}

func runM11PublicDHTProbe(t *testing.T, lab *lab, irviod, namespace, label, dataDir, target, expectedPeer string) {
	t.Helper()
	probe := lab.startChild(label, namespace, irviod,
		"--data-dir", dataDir, "--listen-port", "0", "--bootstrap", target,
		"--find-exits", "DE", "--find-once")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := probe.wait(ctx); err != nil {
		t.Fatalf("fresh inbound public DHT/provider request failed: %v", err)
	}
	output, err := os.ReadFile(filepath.Join(lab.artifactDir, label+".stdout.log"))
	if err != nil || !strings.Contains(string(output), expectedPeer) {
		t.Fatalf("public DHT/provider response did not authenticate Node A: bytes=%d err=%v", len(output), err)
	}
}

func countM11Logs(t *testing.T, lab *lab, file, message string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lab.artifactDir, file))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), `"msg":"`+message+`"`)
}

func waitM11LogCount(t *testing.T, lab *lab, child *childProcess, file, message string, minimum int, maximum time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if countM11Logs(t, lab, file, message) >= minimum {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %d %s messages: %v", minimum, message, ctx.Err())
		case <-child.done:
			child.mu.Lock()
			err := child.waitErr
			child.mu.Unlock()
			t.Fatalf("child exited before repeated %s: %v", message, err)
		case <-ticker.C:
		}
	}
}
