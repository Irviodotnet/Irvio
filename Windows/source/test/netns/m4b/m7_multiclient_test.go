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
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

type m7MultiClient struct {
	namespace string
	clientIf  string
	exitIf    string
	outer     netip.Addr
	gateway   netip.Addr
	inner     netip.Addr
	keySeed   int
	process   connectChild
	ready     iceSignalReport
}

type m7MultiConfigFile struct {
	Peers []m7MultiConfigPeer `json:"peers"`
}

type m7MultiConfigPeer struct {
	PeerID     string `json:"peer_id"`
	KeySeed    int    `json:"key_seed"`
	AllowedIPs string `json:"allowed_ips"`
}

func TestM7_ThreeClientsOneExitInternetIsolation(t *testing.T) {
	runM7MultiClientExit(t, 3, false)
}

func TestM7_FiveClientsOneExitSafetyIsolation(t *testing.T) {
	runM7MultiClientExit(t, 5, true)
}

func runM7MultiClientExit(t *testing.T, clientCount int, safety bool) {
	t.Helper()
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	label := "m7-three-client"
	if safety {
		label = "m7-five-safety"
	}
	lab := newLab(t, label)
	exitNamespace := lab.namespace("exit")
	internetNamespace := lab.namespace("internet")
	exitControl := netip.MustParseAddr("198.18.80.1")
	clients := make([]m7MultiClient, clientCount)
	for index := range clients {
		clients[index] = m7MultiClient{
			namespace: lab.namespace(fmt.Sprintf("client%d", index+1)),
			clientIf:  lab.interfaceName(byte('a' + index)),
			exitIf:    lab.interfaceName(byte('d' + index)),
			outer:     netip.MustParseAddr(fmt.Sprintf("198.18.%d.2", 81+index)),
			gateway:   netip.MustParseAddr(fmt.Sprintf("198.18.%d.1", 81+index)),
			inner:     netip.MustParseAddr(fmt.Sprintf("10.79.0.%d", index+2)),
			keySeed:   111 + index*10,
		}
	}
	exitWANIf := lab.interfaceName('w')
	internetIf := lab.interfaceName('i')
	exitWAN := netip.MustParseAddr("203.0.114.1")
	internet := netip.MustParseAddr("203.0.114.2")
	lab.inNamespace(exitNamespace, "ip", "link", "set", "lo", "up")
	lab.inNamespace(internetNamespace, "ip", "link", "set", "lo", "up")
	lab.inNamespace(exitNamespace, "ip", "addr", "add", exitControl.String()+"/32", "dev", "lo")
	for index := range clients {
		client := &clients[index]
		lab.createVeth(client.namespace, client.clientIf, exitNamespace, client.exitIf)
		lab.inNamespace(client.namespace, "ip", "link", "set", "lo", "up")
		lab.inNamespace(client.namespace, "ip", "link", "set", client.clientIf, "up")
		lab.inNamespace(exitNamespace, "ip", "link", "set", client.exitIf, "up")
		lab.inNamespace(client.namespace, "ip", "addr", "add", client.outer.String()+"/30", "dev", client.clientIf)
		lab.inNamespace(exitNamespace, "ip", "addr", "add", client.gateway.String()+"/30", "dev", client.exitIf)
		lab.inNamespace(client.namespace, "ip", "route", "add", exitControl.String()+"/32", "via", client.gateway.String(), "dev", client.clientIf)
	}
	lab.createVeth(exitNamespace, exitWANIf, internetNamespace, internetIf)
	lab.inNamespace(exitNamespace, "ip", "link", "set", exitWANIf, "up")
	lab.inNamespace(internetNamespace, "ip", "link", "set", internetIf, "up")
	lab.inNamespace(exitNamespace, "ip", "addr", "add", exitWAN.String()+"/24", "dev", exitWANIf)
	lab.inNamespace(internetNamespace, "ip", "addr", "add", internet.String()+"/24", "dev", internetIf)
	internetReady := filepath.Join(lab.artifactDir, "m7-multi-internet-ready")
	internetRelease := filepath.Join(lab.artifactDir, "m7-multi-internet-release")
	internetObservations := filepath.Join(lab.artifactDir, "m7-multi-internet-observations.jsonl")
	internetArgs := []string{"--mode", "server", "--listen4", internet.String(), "--ready", internetReady,
		"--release", internetRelease, "--observations", internetObservations}
	if safety {
		internetArgs = append(internetArgs, "--dns-port", "53", "--tcp-ports", "25,443", "--udp-ports", "1900")
	}
	internetChild := lab.startChild("m7-multi-internet", internetNamespace, os.Getenv("HYDRA_M7_INTERNET"), internetArgs...)
	internetContext, internetCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer internetCancel()
	if err := waitForChildFile(internetContext, internetChild, internetReady); err != nil {
		t.Fatal(err)
	}
	lab.recordTopology(struct {
		Exit     string          `json:"exit_namespace"`
		Internet string          `json:"internet_namespace"`
		ExitMux  netip.Addr      `json:"exit_mux_address"`
		ExitWAN  netip.Addr      `json:"exit_wan"`
		Clients  []m7MultiClient `json:"clients"`
	}{exitNamespace, internetNamespace, exitControl, exitWAN, clients})

	readyPath := filepath.Join(lab.artifactDir, "m7-multi-exit-ready.json")
	resultPath := filepath.Join(lab.artifactDir, "m7-multi-exit-result.json")
	releasePath := filepath.Join(lab.artifactDir, "m7-multi-exit-release")
	awgReadyPath := filepath.Join(lab.artifactDir, "m7-multi-exit-awg-ready.json")
	awgStartPath := filepath.Join(lab.artifactDir, "m7-multi-exit-awg-start")
	multiConfigPath := filepath.Join(lab.artifactDir, "m7-multi-peers.json")
	exitChild := lab.startChild("m7-multi-exit", exitNamespace, os.Getenv("HYDRA_M5B_SIGNALHELPER"),
		"--mode", "connect-multi-responder", "--bind4", exitControl.String(), "--tcp4", exitControl.String(),
		"--port", strconv.Itoa(clientMuxPort), "--ready-file", readyPath, "--result-file", resultPath,
		"--release-file", releasePath, "--timeout", "45s", "--awg", "--awg-real-tun",
		"--awg-tun-name", "hydra0", "--awg-tun-addresses", "10.79.0.1/24",
		"--awg-local-key-seed", "91", "--awg-ready-file", awgReadyPath, "--awg-start-file", awgStartPath,
		"--m7-role", "exit", "--m7-wan-name", exitWANIf, "--m7-client-pool4", "10.79.0.0/24",
		"--m7-management", exitWAN.String()+"/32", "--m7-multi-count", strconv.Itoa(clientCount), "--m7-multi-config", multiConfigPath,
	)
	waitM7ChildJSON(t, exitChild, readyPath, new(iceSignalReport))
	var exitReady iceSignalReport
	if err := readJSON(readyPath, &exitReady); err != nil {
		t.Fatal(err)
	}
	remote := selectSignalAddress(t, exitReady.ListenAddresses, "ipv4")
	exitPeer, err := peer.Decode(exitReady.PeerID)
	if err != nil {
		t.Fatal(err)
	}
	leaseConfig := tunnellease.DefaultConfig(exitPeer)
	leaseConfig.IPv4Pool = netip.MustParsePrefix("10.79.0.0/24")
	leaseConfig.Reserved = []netip.Addr{netip.MustParseAddr("10.79.0.1")}
	leaseAllocator, err := tunnellease.New(leaseConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer leaseAllocator.Close()
	leases := make([]tunnellease.Lease, len(clients))
	clientPeers := make([]peer.ID, len(clients))
	peers := m7MultiConfigFile{Peers: make([]m7MultiConfigPeer, 0, len(clients))}
	for index := range clients {
		clients[index].process, clients[index].ready = startM7MultiClient(t, lab, clients[index], remote)
		clientPeers[index], err = peer.Decode(clients[index].ready.PeerID)
		if err != nil {
			t.Fatal(err)
		}
		leases[index], err = leaseAllocator.Allocate(clientPeers[index])
		if err != nil {
			t.Fatal(err)
		}
		if leases[index].IPv4.Addr() != clients[index].inner {
			t.Fatalf("client %d lease address=%s want=%s", index, leases[index].IPv4, clients[index].inner)
		}
		peers.Peers = append(peers.Peers, m7MultiConfigPeer{
			PeerID: clients[index].ready.PeerID, KeySeed: clients[index].keySeed,
			AllowedIPs: netip.PrefixFrom(clients[index].inner, 32).String(),
		})
	}
	encoded, err := json.Marshal(peers)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(multiConfigPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, exitChild, awgReadyPath); err != nil {
		t.Fatal(err)
	}
	for index := range clients {
		if err := waitForChildFile(ctx, clients[index].process.child, clients[index].process.awgReadyPath); err != nil {
			t.Fatal(err)
		}
	}
	if err := touch(awgStartPath); err != nil {
		t.Fatal(err)
	}
	for index := range clients {
		if err := touch(clients[index].process.awgStartPath); err != nil {
			t.Fatal(err)
		}
	}
	var exitResult iceSignalReport
	waitM7ChildJSON(t, exitChild, resultPath, &exitResult)
	if !exitResult.Connected || !exitResult.AWGHandshake || exitResult.MultiPeers != clientCount || exitResult.MuxPort != clientMuxPort || exitResult.AWGSocketInode != exitResult.UDP4SocketInodeReady {
		t.Fatalf("invalid shared Exit result: %+v", exitResult)
	}
	for index := range clients {
		result := waitConnectReport(t, clients[index].process)
		if !result.Connected || !result.AWGHandshake || result.AWGSelectedRemote != result.SelectedRemote {
			t.Fatalf("client %d result=%+v", index, result)
		}
		probe := runM7Probe(t, lab, clients[index].namespace, "http", endpointValue(internet, 18080))
		assertM7Probe(t, probe, exitWAN, clients[index].inner)
	}
	if safety {
		assertM7Probe(t, runM7Probe(t, lab, clients[0].namespace, "tcp", endpointValue(internet, 443)), exitWAN, clients[0].inner)
		if _, err := tryM7Probe(t, lab, clients[1].namespace, "tcp", endpointValue(internet, 25), "--probe-timeout", "350ms"); err == nil {
			t.Fatal("multi-client TCP/25 bypassed Exit safety policy")
		}
		if _, err := tryM7Probe(t, lab, clients[2].namespace, "udp", endpointValue(internet, 1900), "--probe-timeout", "350ms"); err == nil {
			t.Fatal("multi-client UDP/1900 bypassed Exit safety policy")
		}
		dns := runM7Probe(t, lab, clients[3].namespace, "dns-udp", endpointValue(internet, 53))
		if !dns.Success || dns.DNSAnswer != "198.51.100.77" {
			t.Fatalf("multi-client DNS result=%+v", dns)
		}
		rules := lab.inNamespace(exitNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
		requireCounter(t, rules, "drop-safety-v0-tcp", 1)
		requireCounter(t, rules, "drop-safety-v0-udp", 1)
	}
	if err := touch(clients[0].process.releasePath); err != nil {
		t.Fatal(err)
	}
	if err := clients[0].process.child.wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := leaseAllocator.Release(clientPeers[0], leases[0].ID); err != nil {
		t.Fatal(err)
	}
	if active := leaseAllocator.Active(); len(active) != len(clients)-1 {
		t.Fatalf("active leases after closing client A = %d", len(active))
	}
	for index := 1; index < len(clients); index++ {
		probe := runM7Probe(t, lab, clients[index].namespace, "http", endpointValue(internet, 18080))
		assertM7Probe(t, probe, exitWAN, clients[index].inner)
	}
	exitRules := lab.inNamespace(exitNamespace, "nft", "-j", "list", "table", "inet", "hydra_exit")
	requireCounter(t, exitRules, "masquerade-ipv4", 5)
	for index := 1; index < len(clients); index++ {
		if err := touch(clients[index].process.releasePath); err != nil {
			t.Fatal(err)
		}
	}
	if err := touch(releasePath); err != nil {
		t.Fatal(err)
	}
	if err := touch(internetRelease); err != nil {
		t.Fatal(err)
	}
	for index := 1; index < len(clients); index++ {
		if err := clients[index].process.child.wait(ctx); err != nil {
			t.Fatal(err)
		}
		if err := leaseAllocator.Release(clientPeers[index], leases[index].ID); err != nil {
			t.Fatal(err)
		}
	}
	if active := leaseAllocator.Active(); len(active) != 0 {
		t.Fatalf("active leases after all clients closed = %d", len(active))
	}
	if err := exitChild.wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := internetChild.wait(ctx); err != nil {
		t.Fatal(err)
	}
	lab.inNamespace(exitNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	for index := range clients {
		lab.inNamespace(clients[index].namespace, "test", "!", "-e", "/sys/class/net/hydra0")
	}
}

func startM7MultiClient(t *testing.T, lab *lab, client m7MultiClient, remote string) (connectChild, iceSignalReport) {
	t.Helper()
	label := "m7-multi-" + filepath.Base(client.namespace)
	ready := filepath.Join(lab.artifactDir, label+"-ready.json")
	result := filepath.Join(lab.artifactDir, label+"-result.json")
	release := filepath.Join(lab.artifactDir, label+"-release")
	awgReady := filepath.Join(lab.artifactDir, label+"-awg-ready.json")
	awgStart := filepath.Join(lab.artifactDir, label+"-awg-start")
	child := lab.startChild(label, client.namespace, os.Getenv("HYDRA_M5B_SIGNALHELPER"),
		"--mode", "connect-initiator", "--bind4", client.outer.String(), "--tcp4", client.outer.String(),
		"--remote", remote, "--port", strconv.Itoa(clientMuxPort), "--ready-file", ready,
		"--result-file", result, "--release-file", release, "--timeout", "45s",
		"--awg", "--awg-real-tun", "--awg-tun-name", "hydra0",
		"--awg-tun-addresses", netip.PrefixFrom(client.inner, 32).String(), "--awg-allowed-ips", "0.0.0.0/0",
		"--awg-local-key-seed", strconv.Itoa(client.keySeed), "--awg-remote-key-seed", "91",
		"--awg-ready-file", awgReady, "--awg-start-file", awgStart,
		"--m7-role", "client", "--m7-wan-name", client.clientIf, "--m7-protect4",
		"--m7-wan4-source", client.outer.String(), "--m7-wan4-gateway", client.gateway.String(),
		"--m7-wan4-connected", netip.PrefixFrom(client.outer, 30).Masked().String(), "--m7-bypass", "198.18.80.1/32",
	)
	var readyReport iceSignalReport
	waitM7ChildJSON(t, child, ready, &readyReport)
	return connectChild{child: child, resultPath: result, releasePath: release, awgReadyPath: awgReady, awgStartPath: awgStart}, readyReport
}

func waitM7ChildJSON(t *testing.T, child *childProcess, path string, destination any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, path); err != nil {
		t.Fatal(err)
	}
	if err := readJSON(path, destination); err != nil {
		t.Fatal(err)
	}
}
