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

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

// TestProductDesktopConnectAndFullDisconnect uses production daemon/API with lab-only DNS substitution.
func TestProductDesktopConnectAndFullDisconnect(t *testing.T) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	launcher := assertNormalUserExecutable(t, "HYDRA_M4B_LAB_DNS_LAUNCHER")
	assertNormalUserExecutable(t, "HYDRA_M4B_RFC5780")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	lab := newLab(t, "product-desktop-profile")
	downstream := os.Getenv("HYDRA_PRODUCT_DESKTOP_DOWNSTREAM") == "2"
	nodeCount := 7
	if downstream {
		nodeCount += 2
	}
	namespaces, ips := buildProductRoutedTopology(lab, nodeCount)
	base := lab.tempDir("product-desktop")
	geoIP := filepath.Join(base, "country.mmdb")
	writeM12CountryDatabaseRecords(t, geoIP, map[string]string{
		"198.18.54.0/24": "NL", "198.18.55.0/24": "NL", "198.18.56.0/24": "RU",
		"198.18.58.0/24": "GB", "198.18.59.0/24": "US",
	})
	var seeds []string
	for index := 0; index < 3; index++ {
		label := fmt.Sprintf("product-desktop-verifier-%d", index+1)
		directory := filepath.Join(base, label)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		child := lab.startChild(label, namespaces[index], irviod,
			"--data-dir", directory, "--listen-port", "4000", "--allow-test-public-ip", "--print-node-info")
		info := waitNodeInfo(t, lab, child, label+".stdout.log", 1)
		seeds = append(seeds, selectMultiaddr(t, info, "ip4", ips[index]))
		alternate := netip.AddrFrom4([4]byte{198, 18, byte(51 + index), 11})
		lab.inNamespace(namespaces[index], "ip", "addr", "add", alternate.String()+"/32", "dev", "lo")
		lab.startResponder(label+"-stun", namespaces[index], ips[index], alternate)
	}
	seedBundle, err := productbootstrap.FromMultiaddrs(seeds, 0)
	if err != nil {
		t.Fatal(err)
	}
	seedCode, err := productbootstrap.Encode(seedBundle)
	if err != nil {
		t.Fatal(err)
	}
	for index := 3; index < 5; index++ {
		label := fmt.Sprintf("product-desktop-server-%d", index-2)
		directory := filepath.Join(base, label)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		child := lab.startChild(label, namespaces[index], irviod,
			"--role", "server", "--bootstrap", seedCode, "--data-dir", directory,
			"--geoip-db", geoIP, "--allow-test-public-ip")
		started := waitM11Log(t, lab, child, label+".stderr.log", "PRODUCT_RUNTIME_STARTED", 15*time.Second, nil)
		peerID, ok := started["peer_id"].(string)
		if !ok || peerID == "" {
			t.Fatalf("product server has no observable PeerID: %v", started)
		}
		seeds = append(seeds, fmt.Sprintf("/ip4/%s/tcp/4000/p2p/%s", ips[index], peerID))
		_ = waitM11Log(t, lab, child, label+".stderr.log", "EXIT_CAPABILITY_READY", 35*time.Second, nil)
	}
	// The post-CONNECT probe must traverse an Exit rather than the target's direct router path.
	internet, internetRelease, _ := startM7InternetServer(t, lab, m7Topology{
		InternetNamespace: namespaces[6], Internet4: ips[6],
	})
	defer func() {
		_ = os.WriteFile(internetRelease, []byte("stop\n"), 0o600)
		_ = stopM10Child(internet, 5*time.Second)
	}()
	clientDir := filepath.Join(base, "client")
	if err := os.Mkdir(clientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(clientDir, "control.sock")
	client := lab.startChild("product-desktop-client", namespaces[5], launcher, append([]string{irviod},
		"--role", "desktop", "--data-dir", clientDir, "--local-api-socket", socket,
		"--local-api-group", "root", "--geoip-db", geoIP, "--allow-test-public-ip")...)
	_ = waitM11Log(t, lab, client, "product-desktop-client.stderr.log", "PRODUCT_RUNTIME_READY", 15*time.Second, nil)
	cycles := 1
	if value := os.Getenv("HYDRA_PRODUCT_DESKTOP_CYCLES"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			t.Fatalf("invalid HYDRA_PRODUCT_DESKTOP_CYCLES=%q", value)
		}
		cycles = parsed
	}
	if downstream && cycles != 1 {
		t.Fatal("downstream lease proof requires one desktop lifecycle")
	}
	pause := time.Duration(0)
	if value := os.Getenv("HYDRA_PRODUCT_DESKTOP_PAUSE"); value != "" {
		parsed, parseErr := time.ParseDuration(value)
		if parseErr != nil || parsed < 0 || parsed > 15*time.Second {
			t.Fatalf("invalid HYDRA_PRODUCT_DESKTOP_PAUSE=%q", value)
		}
		pause = parsed
	}
	contextTimeout := time.Duration(cycles)*15*time.Second + 2*time.Minute
	if downstream {
		contextTimeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()
	api, err := localapi.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	status, err := api.GetStatus(ctx)
	if err != nil || status.GetState() != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
		t.Fatalf("desktop was not IDLE before bootstrap import: status=%v err=%v", status, err)
	}
	bootstrapState, err := api.GetBootstrapState(ctx)
	if err != nil || bootstrapState.GetPhase() != localv1.BootstrapPhase_BOOTSTRAP_PHASE_EMPTY {
		t.Fatalf("new desktop did not have empty bootstrap state: state=%v err=%v", bootstrapState, err)
	}
	bundle, err := productbootstrap.FromMultiaddrs(seeds, 0)
	if err != nil {
		t.Fatal(err)
	}
	code, err := productbootstrap.Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.ImportBootstrapBundle(ctx, code); err != nil {
		t.Fatalf("import desktop bootstrap bundle: %v", err)
	}
	baseline := productProcessResources(t, client.command.Process.Pid)
	maxIdleFD := baseline.FD
	var previousTrafficSession uint64
	for cycle := 1; cycle <= cycles; cycle++ {
		if err := api.Connect(ctx, "NL"); err != nil {
			t.Fatalf("cycle %d desktop CONNECT: %v", cycle, err)
		}
		deadline := time.Now().Add(75 * time.Second)
		for time.Now().Before(deadline) {
			status, err = api.GetStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if status.GetState() == localv1.ConnectionState_CONNECTION_STATE_CONNECTED {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if status.GetState() != localv1.ConnectionState_CONNECTION_STATE_CONNECTED || !status.GetContributionEnabled() {
			t.Fatalf("cycle %d desktop CLIENT+EXIT did not connect: status=%v", cycle, status)
		}
		// Exercise both counters in every session, including middle cycles.
		result := runM7Probe(t, lab, namespaces[5], "http", netip.AddrPortFrom(ips[6], 18080))
		observed, parseErr := netip.ParseAddrPort(result.ObservedSource)
		if parseErr != nil || !result.Success || observed.Addr() != ips[3] && observed.Addr() != ips[4] {
			t.Fatalf("cycle %d user-plane did not traverse an Exit: %+v, parse=%v", cycle, result, parseErr)
		}
		if downstream {
			for index := 7; index < 9; index++ {
				label := fmt.Sprintf("product-downstream-%d", index-6)
				directory := filepath.Join(base, label)
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				peerSocket := filepath.Join(directory, "control.sock")
				peer := lab.startChild(label, namespaces[index], launcher, append([]string{irviod},
					"--role", "desktop", "--data-dir", directory, "--local-api-socket", peerSocket,
					"--local-api-group", "root", "--geoip-db", geoIP, "--allow-test-public-ip")...)
				_ = waitM11Log(t, lab, peer, label+".stderr.log", "PRODUCT_RUNTIME_READY", 15*time.Second, nil)
				peerAPI, dialErr := localapi.Dial(ctx, peerSocket)
				if dialErr != nil {
					t.Fatal(dialErr)
				}
				defer peerAPI.Close()
				if _, err := peerAPI.ImportBootstrapBundle(ctx, code); err != nil {
					t.Fatalf("%s bootstrap import: %v", label, err)
				}
				if err := peerAPI.Connect(ctx, "RU"); err != nil {
					t.Fatalf("%s CONNECT RU: %v", label, err)
				}
				deadline := time.Now().Add(90 * time.Second)
				for time.Now().Before(deadline) {
					peerStatus, statusErr := peerAPI.GetStatus(ctx)
					if statusErr != nil {
						t.Fatal(statusErr)
					}
					if peerStatus.GetState() == localv1.ConnectionState_CONNECTION_STATE_CONNECTED {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				probe := runM7Probe(t, lab, namespaces[index], "http", netip.AddrPortFrom(ips[6], 18080))
				observed, parseErr := netip.ParseAddrPort(probe.ObservedSource)
				if parseErr != nil || !probe.Success || observed.Addr() != ips[5] {
					t.Fatalf("%s did not use desktop Exit: %+v, parse=%v", label, probe, parseErr)
				}
			}
			if count := productLogEventCount(t, filepath.Join(lab.artifactDir, "product-desktop-client.stderr.log"), "TUNNEL_LEASE_DATA_PLANE_ACTIVATED"); count < 2 {
				t.Fatalf("desktop did not activate two downstream leases: count=%d", count)
			}
		}
		// Verify separate own/shared bytes and two downstream clients without exposing PeerIDs.
		var traffic *localv1.TrafficStats
		statsDeadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(statsDeadline) {
			traffic, err = api.GetTrafficStats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if traffic.MyUploadBytes != nil && traffic.MyDownloadBytes != nil && traffic.GetMyDownloadBytes() > 0 && traffic.SharedUploadBytes != nil && traffic.Clients != nil &&
				(!downstream || traffic.GetClients() == 2 && traffic.GetSharedUploadBytes() > 0) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if traffic == nil || traffic.MyUploadBytes == nil || traffic.MyDownloadBytes == nil || traffic.GetMyDownloadBytes() == 0 || traffic.SharedUploadBytes == nil || traffic.Clients == nil ||
			!traffic.Active || !traffic.Complete || traffic.SessionId == previousTrafficSession {
			t.Fatalf("cycle %d invalid traffic snapshot: %v", cycle, traffic)
		}
		if downstream && (traffic.GetClients() != 2 || traffic.GetSharedUploadBytes() == 0) {
			t.Fatalf("missing downstream statistics: %v", traffic)
		}
		if !downstream && (traffic.GetClients() != 0 || traffic.GetSharedUploadBytes() != 0) {
			t.Fatalf("own traffic leaked into Exit accounting: %v", traffic)
		}
		previousTrafficSession = traffic.SessionId
		t.Logf("cycle=%d traffic before Disconnect: %v", cycle, traffic)
		if err := api.Disconnect(ctx); err != nil {
			t.Fatalf("cycle %d desktop DISCONNECT: %v", cycle, err)
		}
		status, err = api.GetStatus(ctx)
		if err != nil || status.GetState() != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED || status.GetContributionEnabled() {
			t.Fatalf("cycle %d desktop did not return to IDLE: status=%v err=%v", cycle, status, err)
		}
		finalTraffic, trafficErr := api.GetTrafficStats(ctx)
		if trafficErr != nil || finalTraffic.Active || finalTraffic.GetClients() != 0 || finalTraffic.MyUploadBytes == nil || finalTraffic.MyDownloadBytes == nil || finalTraffic.GetMyDownloadBytes() < traffic.GetMyDownloadBytes() || finalTraffic.SharedUploadBytes == nil ||
			finalTraffic.SessionId != traffic.SessionId || finalTraffic.GetMyUploadBytes() < traffic.GetMyUploadBytes() || finalTraffic.GetSharedUploadBytes() < traffic.GetSharedUploadBytes() {
			t.Fatalf("final traffic not retained: stats=%v err=%v", finalTraffic, trafficErr)
		}
		if got := lab.inNamespace(namespaces[5], "ip", "-j", "link", "show"); strings.Contains(got, `"ifname":"hydra0"`) {
			t.Fatalf("cycle %d desktop TUN remains after DISCONNECT: %s", cycle, got)
		}
		if downstream {
			if count := productLogEventCount(t, filepath.Join(lab.artifactDir, "product-desktop-client.stderr.log"), "TUNNEL_LEASE_DATA_PLANE_REVOKED"); count < 2 {
				t.Fatalf("desktop returned IDLE before revoking both downstream leases: count=%d", count)
			}
			for index := 7; index < 9; index++ {
				probe, probeErr := tryM7Probe(t, lab, namespaces[index], "http", netip.AddrPortFrom(ips[6], 18080))
				if probeErr == nil && probe.Success {
					t.Fatalf("downstream client %d reached WAN after its Exit disconnected: %+v", index-6, probe)
				}
			}
		}
		idle := productProcessResources(t, client.command.Process.Pid)
		maxIdleFD = max(maxIdleFD, idle.FD)
		// Allow Go thread warmup while rejecting generation-correlated resource growth.
		if idle.FD > baseline.FD+8 || idle.Pipes > baseline.Pipes+4 || idle.Threads > baseline.Threads+32 {
			t.Fatalf("cycle %d resource growth: baseline=%+v idle=%+v", cycle, baseline, idle)
		}
		if cycle%10 == 0 {
			t.Logf("cycle=%d/%d idle_resources=%+v", cycle, cycles, idle)
		}
		if cycle < cycles && pause > 0 {
			select {
			case <-time.After(pause):
			case <-ctx.Done():
				t.Fatalf("pause after cycle %d: %v", cycle, ctx.Err())
			}
		}
	}
	result := runM7Probe(t, lab, namespaces[5], "http", netip.AddrPortFrom(ips[6], 18080))
	observed, err := netip.ParseAddrPort(result.ObservedSource)
	if err != nil || !result.Success || observed.Addr() != ips[5] {
		t.Fatalf("ordinary WAN was not restored after DISCONNECT: %+v, parse=%v", result, err)
	}
	t.Logf("same-process desktop cycles=%d pause=%s baseline=%+v final=%+v max_idle_fd=%d", cycles, pause, baseline,
		productProcessResources(t, client.command.Process.Pid), maxIdleFD)
}

func productLogEventCount(t *testing.T, path, event string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), `"msg":"`+event+`"`)
}

type productResources struct{ FD, Pipes, Threads int }

func productProcessResources(t *testing.T, pid int) productResources {
	t.Helper()
	root := filepath.Join("/proc", strconv.Itoa(pid))
	entries, err := os.ReadDir(filepath.Join(root, "fd"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := os.ReadDir(filepath.Join(root, "task"))
	if err != nil {
		t.Fatal(err)
	}
	result := productResources{FD: len(entries), Threads: len(tasks)}
	for _, entry := range entries {
		link, err := os.Readlink(filepath.Join(root, "fd", entry.Name()))
		if err == nil && strings.HasPrefix(link, "pipe:[") {
			result.Pipes++
		}
	}
	return result
}
