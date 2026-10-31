// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
)

// TestProductServerPresetInRealNetNS uses independent verifiers and real STUN with synthetic address/MMDB overrides.
func TestProductServerPresetInRealNetNS(t *testing.T) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	assertNormalUserExecutable(t, "HYDRA_M4B_RFC5780")
	assertNormalUserExecutable(t, "HYDRA_M4B_NATPROBE")
	lab := newLab(t, "product-server-profile")
	nodeNamespaces, nodeIPs := buildProductRoutedTopology(lab, 4)
	serverNamespace, serverIP := nodeNamespaces[3], nodeIPs[3]

	base := lab.tempDir("product-server")
	geoIP := filepath.Join(base, "country.mmdb")
	writeM12CountryDatabase(t, geoIP, "198.18.54.0/24", "NL")
	var addresses []string
	var verifiers []*childProcess
	var verifierDirs []string
	verifierIPs := nodeIPs[:3]
	verifierNamespaces := nodeNamespaces[:3]
	for index, ip := range verifierIPs {
		label := fmt.Sprintf("product-verifier-%d", index+1)
		dataDir := filepath.Join(base, label)
		if err := os.Mkdir(dataDir, 0o700); err != nil {
			t.Fatal(err)
		}
		process := lab.startChild(label, verifierNamespaces[index], irviod,
			"--data-dir", dataDir, "--listen-port", "4000", "--allow-test-public-ip", "--print-node-info",
			"--public-proof-ttl", "4s", "--public-reverify-lead", "2s")
		verifiers = append(verifiers, process)
		verifierDirs = append(verifierDirs, dataDir)
		info := waitNodeInfo(t, lab, process, label+".stdout.log", 1)
		addresses = append(addresses, selectMultiaddr(t, info, "ip4", ip))
		alternate := netip.AddrFrom4([4]byte{198, 18, byte(51 + index), 11})
		lab.inNamespace(verifierNamespaces[index], "ip", "addr", "add", alternate.String()+"/32", "dev", "lo")
		lab.startResponder(fmt.Sprintf("product-stun-%d", index+1), verifierNamespaces[index], ip, alternate)
	}
	bundle, err := productbootstrap.FromMultiaddrs(addresses, 0)
	if err != nil {
		t.Fatal(err)
	}
	code, err := productbootstrap.Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	serverDir := filepath.Join(base, "server")
	if err := os.Mkdir(serverDir, 0o700); err != nil {
		t.Fatal(err)
	}
	server := lab.startChild("product-server", serverNamespace, irviod,
		"--role", "server", "--bootstrap", code, "--data-dir", serverDir,
		"--geoip-db", geoIP, "--allow-test-public-ip",
		"--public-proof-ttl", "4s", "--public-reverify-lead", "2s", "--public-retry", "1s")
	_ = waitM11Log(t, lab, server, "product-server.stderr.log", "PUBLIC_STUN_READY", 15*time.Second, nil)
	_ = waitM11Log(t, lab, server, "product-server.stderr.log", "PRODUCT_RUNTIME_STARTED", 20*time.Second, nil)
	udpSockets := lab.inNamespace(serverNamespace, "ss", "-H", "-lunp")
	if !bytes.Contains([]byte(udpSockets), []byte(":51820")) {
		t.Fatalf("server preset did not bind fixed UDP/51820: %s", udpSockets)
	}
	_ = waitM11Log(t, lab, server, "product-server.stderr.log", "PUBLIC_REACHABILITY_VERIFIED", 30*time.Second, nil)
	_ = waitM11Log(t, lab, server, "product-server.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 30*time.Second, nil)
	firstExit := waitM11Log(t, lab, server, "product-server.stderr.log", "EXIT_CAPABILITY_READY", 30*time.Second, nil)
	startupLog, err := os.ReadFile(filepath.Join(lab.artifactDir, "product-server.stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	verifiedAt := bytes.Index(startupLog, []byte(`"msg":"PUBLIC_REACHABILITY_VERIFIED"`))
	rendezvousAt := bytes.Index(startupLog, []byte(`"msg":"RENDEZVOUS_SERVICE_ACTIVE"`))
	exitAt := bytes.Index(startupLog, []byte(`"msg":"EXIT_CAPABILITY_READY"`))
	if verifiedAt < 0 || rendezvousAt < 0 || exitAt < 0 || exitAt <= verifiedAt || exitAt <= rendezvousAt {
		t.Fatalf("Exit advertised before M11 and Rendezvous readiness: verified=%d rendezvous=%d exit=%d", verifiedAt, rendezvousAt, exitAt)
	}
	// A same-address network generation must not escape the installed server firewall ports.
	lab.inNamespace(serverNamespace, "ip", "link", "set", lab.interfaceName('d'), "mtu", "1400")
	migrated := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		data, _ := os.ReadFile(filepath.Join(lab.artifactDir, "product-server.stderr.log"))
		starts := 0
		for _, line := range bytes.Split(data, []byte("\n")) {
			var event struct {
				Message string `json:"msg"`
				Port    int    `json:"port"`
			}
			if json.Unmarshal(line, &event) == nil && event.Message == "SOCKETMUX_STARTED" {
				starts++
				if event.Port != 51820 {
					t.Fatalf("server migrated outside fixed port: %s", line)
				}
			}
		}
		if starts >= 2 {
			migrated = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !migrated {
		t.Fatal("server network-generation rebind was not exercised")
	}
	_ = waitM11Log(t, lab, server, "product-server.stderr.log", "PUBLIC_REACHABILITY_VERIFIED", 30*time.Second,
		func(event map[string]any) bool { return event["network_generation"].(float64) >= 2 })
	firstExit = waitM11Log(t, lab, server, "product-server.stderr.log", "EXIT_CAPABILITY_READY", 30*time.Second,
		func(event map[string]any) bool { return event["sequence"].(float64) > firstExit["sequence"].(float64) })
	filter := lab.inNamespace(serverNamespace, "nft", "-j", "list", "ruleset")
	if !bytes.Contains([]byte(filter), []byte("drop-safety-v0-tcp")) ||
		!bytes.Contains([]byte(filter), []byte("drop-safety-v0-udp")) {
		t.Fatal("product server Exit is missing the Safety Policy v0 forwarding rules")
	}
	probe, err := lab.runProbe("product-server-stun", verifierNamespaces[0], probeOptions{
		Mode: "basic", BindIPv4: verifierIPs[0], ServerIPv4: netip.AddrPortFrom(serverIP, 3478),
	}, nil)
	if err != nil || probe.BasicIPv4 == nil || probe.BasicIPv4.Mapped.Addr() != verifierIPs[0] {
		t.Fatalf("product server STUN responder: report=%+v err=%v", probe, err)
	}
	for _, verifier := range verifiers[:2] {
		if err := stopM10Child(verifier, 12*time.Second); err != nil {
			t.Fatalf("stop M11 verifier: %v", err)
		}
	}
	suspended := waitM11Log(t, lab, server, "product-server.stderr.log", "EXIT_CAPABILITY_SUSPENDED", 20*time.Second,
		func(event map[string]any) bool { return event["sequence"].(float64) > firstExit["sequence"].(float64) })
	for index := 0; index < 2; index++ {
		label := fmt.Sprintf("product-verifier-%d-restored", index+1)
		process := lab.startChild(label, verifierNamespaces[index], irviod,
			"--data-dir", verifierDirs[index], "--listen-port", "4000", "--allow-test-public-ip", "--print-node-info",
			"--public-proof-ttl", "4s", "--public-reverify-lead", "2s")
		_ = waitNodeInfo(t, lab, process, label+".stdout.log", 1)
	}
	_ = waitM11Log(t, lab, server, "product-server.stderr.log", "EXIT_CAPABILITY_READY", 30*time.Second,
		func(event map[string]any) bool { return event["sequence"].(float64) > suspended["sequence"].(float64) })
	if err := stopM10Child(server, 12*time.Second); err != nil {
		t.Fatalf("stop product server: %v", err)
	}
	unknownDir := filepath.Join(base, "server-no-mmdb")
	if err := os.Mkdir(unknownDir, 0o700); err != nil {
		t.Fatal(err)
	}
	unknown := lab.startChild("product-server-no-mmdb", serverNamespace, irviod,
		"--role", "server", "--bootstrap", code, "--data-dir", unknownDir,
		"--geoip-db", filepath.Join(base, "missing.mmdb"), "--allow-test-public-ip")
	_ = waitM11Log(t, lab, unknown, "product-server-no-mmdb.stderr.log", "EXIT_COUNTRY_UNKNOWN", 15*time.Second, nil)
	_ = waitM11Log(t, lab, unknown, "product-server-no-mmdb.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 30*time.Second, nil)
	unknownLog, err := os.ReadFile(filepath.Join(lab.artifactDir, "product-server-no-mmdb.stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(unknownLog, []byte(`"msg":"EXIT_CAPABILITY_READY"`)) ||
		bytes.Contains(unknownLog, []byte(`"msg":"SERVICE_ADVERTISE","peer_id"`)) {
		t.Fatal("server advertised a country Exit without a working MMDB")
	}
	if err := stopM10Child(unknown, 12*time.Second); err != nil {
		t.Fatalf("stop unknown-country product server: %v", err)
	}
}

func buildProductRoutedTopology(lab *lab, count int) ([]string, []netip.Addr) {
	lab.t.Helper()
	router := lab.namespace("router")
	lab.inNamespace(router, "ip", "link", "set", "lo", "up")
	lab.inNamespace(router, "ip", "link", "add", "br0", "type", "bridge")
	lab.inNamespace(router, "ip", "link", "set", "br0", "up")
	lab.inNamespace(router, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1")
	var nodeNamespaces []string
	var nodeIPs []netip.Addr
	for index := 0; index < count; index++ {
		namespace := lab.namespace(fmt.Sprintf("n%d", index+1))
		ip := netip.AddrFrom4([4]byte{198, 18, byte(51 + index), 2})
		gateway := netip.AddrFrom4([4]byte{198, 18, byte(51 + index), 254})
		nodeIf, routerIf := lab.interfaceName(byte('a'+index)), lab.interfaceName(byte('1'+index))
		lab.createVeth(namespace, nodeIf, router, routerIf)
		lab.inNamespace(namespace, "ip", "link", "set", "lo", "up")
		lab.inNamespace(namespace, "ip", "link", "set", nodeIf, "up")
		lab.inNamespace(namespace, "ip", "addr", "add", ip.String()+"/24", "dev", nodeIf)
		lab.inNamespace(namespace, "ip", "route", "add", "default", "via", gateway.String(), "dev", nodeIf)
		lab.inNamespace(router, "ip", "link", "set", routerIf, "master", "br0")
		lab.inNamespace(router, "ip", "link", "set", routerIf, "up")
		lab.inNamespace(router, "ip", "addr", "add", gateway.String()+"/24", "dev", "br0")
		nodeNamespaces = append(nodeNamespaces, namespace)
		nodeIPs = append(nodeIPs, ip)
	}
	return nodeNamespaces, nodeIPs
}
