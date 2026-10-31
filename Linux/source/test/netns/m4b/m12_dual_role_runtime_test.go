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

func TestM12_IrviodDualRoleMaintainsIndependentTunnelControl(t *testing.T) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")

	lab := newLab(t, "m12-dual-role")
	topology := buildM10FailoverTopology(lab)
	lab.inNamespace(topology.InternetNamespace, "ip", "route", "add",
		netip.PrefixFrom(topology.ExitAData4, 24).Masked().String(),
		"via", topology.ExitAWAN4.String(), "dev", topology.InternetAIf)
	lab.inNamespace(topology.InternetNamespace, "ip", "route", "add",
		netip.PrefixFrom(topology.ExitBData4, 24).Masked().String(),
		"via", topology.ExitBWAN4.String(), "dev", topology.InternetBIf)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.InternetA4.String()+"/32",
		"via", topology.ExitAData4.String(), "dev", topology.RouterAIf)
	lab.inNamespace(topology.RouterNamespace, "ip", "route", "add", topology.InternetB4.String()+"/32",
		"via", topology.ExitBData4.String(), "dev", topology.RouterBIf)
	lab.inNamespace(topology.InternetNamespace, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1")
	lab.installNFT(topology.ClientNamespace, "m12-dual-no-direct", fmt.Sprintf(`table inet m12_no_direct {
	chain output { type filter hook output priority -250; policy accept;
	 ip daddr { %s, %s } meta l4proto tcp counter drop comment "b-to-a-direct-tcp"
	}
}`, topology.ExitAData4, topology.ExitAWAN4))
	lab.installNFT(topology.ExitANamespace, "m12-dual-a-no-direct", fmt.Sprintf(`table inet m12_no_direct {
	chain output { type filter hook output priority -250; policy accept;
	 ip daddr { %s, %s, %s } meta l4proto tcp counter drop comment "a-direct-control"
	}
}`, topology.ExitBData4, topology.ExitBWAN4, topology.Client4))
	lab.installNFT(topology.ExitBNamespace, "m12-dual-c-no-direct", fmt.Sprintf(`table inet m12_no_direct {
	chain output { type filter hook output priority -250; policy accept;
	 ip daddr { %s, %s } meta l4proto tcp counter drop comment "c-to-a-direct-tcp"
	}
}`, topology.ExitAData4, topology.ExitAWAN4))
	internet, internetRelease, _ := startM7InternetServer(t, lab, m7Topology{
		InternetNamespace: topology.InternetNamespace,
		Internet4:         topology.Internet4,
	})

	runtimeDir := lab.tempDir("m12-dual-role")
	paths := map[string]string{}
	for _, name := range []string{
		"bootstrap", "verifier-1", "verifier-2", "verifier-3",
		"r2", "exit-c", "node-a", "client-b",
	} {
		paths[name] = filepath.Join(runtimeDir, name)
		if err := os.Mkdir(paths[name], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	geoIP := filepath.Join(runtimeDir, "country.mmdb")
	writeM12CountryDatabaseRecords(t, geoIP, map[string]string{
		"203.0.113.0/24":  "DE",
		"198.51.100.0/24": "NL",
	})

	bootstrapProcess := lab.startChild("m12-dual-bootstrap", topology.RouterNamespace, irviod,
		"--data-dir", paths["bootstrap"], "--listen-port", "45400", "--print-node-info")
	bootstrapInfo := waitNodeInfo(t, lab, bootstrapProcess, "m12-dual-bootstrap.stdout.log", 1)
	bootstrap := selectMultiaddr(t, bootstrapInfo, "ip4", topology.RouterClient4)

	verifierAddresses := make([]string, 0, 3)
	verifiers := make([]*childProcess, 0, 3)
	for index := 0; index < 3; index++ {
		label := fmt.Sprintf("m12-dual-verifier-%d", index+1)
		process := lab.startChild(label, topology.InternetNamespace, irviod,
			"--data-dir", paths[fmt.Sprintf("verifier-%d", index+1)],
			"--listen-port", fmt.Sprintf("%d", 45410+index),
			"--allow-test-public-ip", "--print-node-info")
		info := waitNodeInfo(t, lab, process, label+".stdout.log", 1)
		verifierAddresses = append(verifierAddresses, selectMultiaddr(t, info, "ip4", topology.Internet4))
		verifiers = append(verifiers, process)
	}
	r2 := lab.startChild("m12-dual-r2", topology.InternetNamespace, irviod,
		m12RendezvousArgs(paths["r2"], bootstrap, "45401", geoIP, verifierAddresses)...)
	r2Info := waitNodeInfo(t, lab, r2, "m12-dual-r2.stdout.log", 1)
	_ = waitM11Log(t, lab, r2, "m12-dual-r2.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 15*time.Second, nil)
	r2Address := selectMultiaddr(t, r2Info, "ip4", topology.Internet4)

	exitCArgs := m11ExitArgs(paths["exit-c"], bootstrap, "NL", "45403", "41503", topology.ExitBWANIf,
		topology.ExitBWAN4, "10.77.0.1/24", "10.77.0.0/24")
	exitCArgs = append(exitCArgs,
		"--rendezvous-v1", "--rendezvous-peer", r2Address,
		"--lease-ttl", "3s", "--lease-renew-lead", "1s", "--lease-renew-retry", "200ms")
	exitC := lab.startChild("m12-dual-exit-c", topology.ExitBNamespace, irviod, exitCArgs...)
	exitCInfo := waitNodeInfo(t, lab, exitC, "m12-dual-exit-c.stdout.log", 1)
	waitM12RegistrationOwner(t, lab, exitC, "m12-dual-exit-c.stderr.log", r2Info.PeerID)

	nodeAArgs := m11ExitArgs(paths["node-a"], bootstrap, "DE", "45402", "41502", topology.ExitAWANIf,
		topology.ExitAWAN4, "10.88.0.1/24", "10.88.0.0/24")
	nodeAArgs = append(nodeAArgs,
		"--connect", "NL", "--protected-mode",
		"--rendezvous-v1", "--rendezvous-peer", r2Address,
		"--lease-ttl", "3s", "--lease-renew-lead", "1s", "--lease-renew-retry", "200ms")
	for _, prefix := range []netip.Prefix{
		netip.PrefixFrom(topology.Client4, 24).Masked(),
		netip.PrefixFrom(topology.ExitAData4, 24).Masked(),
		netip.PrefixFrom(topology.ExitBData4, 24).Masked(),
	} {
		nodeAArgs = append(nodeAArgs, "--bypass-prefix", prefix.String())
	}
	nodeA := lab.startChild("m12-dual-node-a", topology.ExitANamespace, irviod, nodeAArgs...)
	nodeAInfo := waitNodeInfo(t, lab, nodeA, "m12-dual-node-a.stdout.log", 1)
	aStatus := waitM10RuntimeStatus(t, lab, nodeA, "m12-dual-node-a.stderr.log", 60*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == exitCInfo.PeerID && status.TunnelLeaseID != ""
	})
	waitM12RegistrationOwner(t, lab, nodeA, "m12-dual-node-a.stderr.log", r2Info.PeerID)

	clientB := lab.startChild("m12-dual-client-b", topology.ClientNamespace, irviod,
		"--data-dir", paths["client-b"], "--listen-port", "45404", "--bootstrap", bootstrap,
		"--runtime", "--connect", "DE", "--udp-port", "41504", "--tun-name", "hydra0",
		"--bypass-prefix", netip.PrefixFrom(topology.Client4, 24).Masked().String(),
		"--lease-ttl", "3s", "--lease-sweep", "100ms", "--lease-renew-lead", "1s", "--lease-renew-retry", "200ms",
		"--rendezvous-v1", "--rendezvous-peer", r2Address,
		"--print-node-info", "--print-runtime-events")
	_ = waitNodeInfo(t, lab, clientB, "m12-dual-client-b.stdout.log", 1)
	bStatus := waitM10RuntimeStatus(t, lab, clientB, "m12-dual-client-b.stderr.log", 60*time.Second, func(status m10RuntimeStatus) bool {
		return status.State == irvioruntime.StateConnected && status.ActivePeer == nodeAInfo.PeerID && status.TunnelLeaseID != "" ||
			status.State == irvioruntime.StateNoExit
	})
	if bStatus.State == irvioruntime.StateNoExit {
		aFirewall := lab.inNamespace(topology.ExitANamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
		aAddresses := lab.inNamespace(topology.ExitANamespace, "ip", "-j", "address", "show", "dev", "hydra0")
		aRoutes := lab.inNamespace(topology.ExitANamespace, "ip", "route", "show", "table", "51822")
		t.Fatalf("downstream M12 BIND failed: accept_control=%d drop_exit=%d A_routes=%q A_addresses=%s status=%+v",
			requireCounter(t, aFirewall, "accept-session-control-00", 0),
			requireCounter(t, aFirewall, "drop-hydra-to-exit", 0), aRoutes, aAddresses, bStatus)
	}
	if aStatus.TunnelLeaseID == bStatus.TunnelLeaseID {
		t.Fatalf("dual-role upstream and downstream leases alias: A=%+v B=%+v", aStatus, bStatus)
	}

	lab.installNFT(topology.ExitANamespace, "m12-dual-control", fmt.Sprintf(`table inet m12_dual_control {
	chain output { type filter hook output priority 100; policy accept;
	 meta mark 0x48595443 oifname "hydra0" counter comment "dual-control-hydra"
	 meta mark 0x48595443 oifname "%s" counter comment "dual-control-wan"
	}
}`, topology.ExitAWANIf))
	routes := lab.inNamespace(topology.ExitANamespace, "ip", "route", "show", "table", "51822")
	if !strings.Contains(routes, "10.77.0.1") || !strings.Contains(routes, "10.88.0.2") ||
		!strings.Contains(routes, "unreachable default") {
		t.Fatalf("dual-role TUNNEL_ONLY routes do not own both leases: %s", routes)
	}

	target := netip.AddrPortFrom(topology.Internet4, 18080)
	aProbe := runM7Probe(t, lab, topology.ExitANamespace, "http", target)
	assertM7Probe(t, aProbe, topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))
	cBefore := lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	cNATBefore := requireCounter(t, cBefore, "masquerade-ipv4", 1)
	bProbe := runM7Probe(t, lab, topology.ClientNamespace, "http", target)
	assertM7Probe(t, bProbe, topology.ExitAWAN4, netip.MustParseAddr("10.88.0.2"))
	cAfter := lab.inNamespace(topology.ExitBNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	if after := requireCounter(t, cAfter, "masquerade-ipv4", 1); after != cNATBefore {
		t.Fatalf("downstream B traffic entered A's upstream Exit C: before=%d after=%d", cNATBefore, after)
	}
	aExitRules := lab.inNamespace(topology.ExitANamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	requireCounter(t, aExitRules, "masquerade-ipv4", 1)

	aExpiry := mustM12Expiry(t, aStatus.TunnelLeaseExpiry)
	bExpiry := mustM12Expiry(t, bStatus.TunnelLeaseExpiry)
	aRenewed := waitM10RuntimeStatus(t, lab, nodeA, "m12-dual-node-a.stderr.log", 12*time.Second, func(status m10RuntimeStatus) bool {
		expiry, err := time.Parse(time.RFC3339Nano, status.TunnelLeaseExpiry)
		return status.State == irvioruntime.StateConnected && status.TunnelLeaseID == aStatus.TunnelLeaseID &&
			err == nil && expiry.After(aExpiry.Add(3*time.Second))
	})
	bRenewed := waitM10RuntimeStatus(t, lab, clientB, "m12-dual-client-b.stderr.log", 12*time.Second, func(status m10RuntimeStatus) bool {
		expiry, err := time.Parse(time.RFC3339Nano, status.TunnelLeaseExpiry)
		return status.State == irvioruntime.StateConnected && status.TunnelLeaseID == bStatus.TunnelLeaseID &&
			err == nil && expiry.After(bExpiry.Add(3*time.Second))
	})
	if aRenewed.ActivePeer != exitCInfo.PeerID || bRenewed.ActivePeer != nodeAInfo.PeerID {
		t.Fatalf("dual-role lease maintenance changed peer ownership: A=%+v B=%+v", aRenewed, bRenewed)
	}
	controlCounters := lab.inNamespace(topology.ExitANamespace, "nft", "-j", "list", "table", "inet", "m12_dual_control")
	requireCounter(t, controlCounters, "dual-control-hydra", 1)
	if leaked := requireCounter(t, controlCounters, "dual-control-wan", 0); leaked != 0 {
		t.Fatalf("dual-role maintenance escaped to A physical WAN: %d packets", leaked)
	}

	if err := stopM10Child(clientB, 12*time.Second); err != nil {
		t.Fatalf("stop downstream M12 Client B: %v", err)
	}
	waitM12RouteAbsent(t, lab, topology.ExitANamespace, "10.88.0.2", 5*time.Second)
	remainingRoutes := lab.inNamespace(topology.ExitANamespace, "ip", "route", "show", "table", "51822")
	if !strings.Contains(remainingRoutes, "10.77.0.1") {
		t.Fatalf("downstream teardown removed A's upstream control route: %s", remainingRoutes)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ExitANamespace, "http", target),
		topology.ExitBWAN4, netip.MustParseAddr("10.77.0.2"))

	if err := stopM10Child(nodeA, 12*time.Second); err != nil {
		t.Fatalf("stop M12 dual-role Node A: %v", err)
	}
	if err := stopM10Child(exitC, 12*time.Second); err != nil {
		t.Fatalf("stop upstream M12 Exit C: %v", err)
	}
	if err := stopM10Child(r2, 12*time.Second); err != nil {
		t.Fatalf("stop M12 dual-role Rendezvous: %v", err)
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
	lab.inNamespace(topology.ExitBNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ClientNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
}

func mustM12Expiry(t *testing.T, value string) time.Time {
	t.Helper()
	expiry, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse M12 lease expiry %q: %v", value, err)
	}
	return expiry
}

func waitM12RouteAbsent(t *testing.T, lab *lab, namespace, address string, maximum time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		routes := lab.inNamespace(namespace, "ip", "route", "show", "table", "51822")
		if !strings.Contains(routes, address) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("route %s remained after downstream lease teardown: %s", address, routes)
		case <-ticker.C:
		}
	}
}
