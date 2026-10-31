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
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

// TestPrivateRuntimeEnrollmentTrafficAndRevocation uses real networking with lab-only DNS substitution.
func TestPrivateRuntimeEnrollmentTrafficAndRevocation(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{})
}

func TestPrivateRuntimeAccessInvitationTrafficAndRevoke(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{autoInvite: true, cycles: 3})
}

func TestPrivateRuntimeDisjointOwnersAndOwnerLoss(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{disjoint: true})
}

func TestPrivateRuntimeOwnerRestart(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{disjoint: true, ownerRestart: true})
}

func TestPrivateRuntimeRevokeDuringICE(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{revokeICE: true})
}

func TestPrivateRuntimeMultipleClientsSelectiveRevocation(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{multipleClients: true, selectiveRevocation: true})
}

func TestPrivateRuntimeMultipleClientsFullLeave(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{multipleClients: true})
}

func TestPrivateRuntimeLifecycle100(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{cycles: 100})
}

func TestPrivateRuntimeSameBootCrashRecovery(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{crash: true})
}

func TestPrivateRuntimeSystemdCrashRecovery(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{systemdCrash: true})
}

func TestPrivateRuntimeSocketActivatedCrashRecovery(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{systemdCrash: true, socketActivated: true})
}

func TestPrivateRuntimeGUICrash(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{guiCrash: true})
}

func TestPrivateRuntimeHostingGUICrash(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{hostingGUICrash: true})
}

func TestPrivateRuntimeCombinedGUIShutdown(t *testing.T) {
	if os.Getenv("IRVIO_PRIVATE_MANUAL_GUI") != "1" {
		t.Skip("requires explicit native GUI interaction")
	}
	testPrivateRuntime(t, privateRuntimeCase{combined: true, guiShutdown: true})
}

func TestPrivateRuntimeGUIOnboarding(t *testing.T) {
	if os.Getenv("IRVIO_PRIVATE_MANUAL_GUI") != "1" {
		t.Skip("requires explicit native GUI interaction")
	}
	testPrivateRuntime(t, privateRuntimeCase{guiOnboarding: true})
}

func TestPrivateRuntimeDisconnectRelease(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{cycles: 3})
}

func TestPrivateRuntimeClientEIMNAT(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{clientNAT: true})
}

func TestPrivateRuntimeHostEIMNAT(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{hostNAT: true})
}

func TestPrivateRuntimeSharedNATWithLAN(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{sharedNAT: true})
}

func TestPrivateRuntimeIsolatedSharedNATFailsClosed(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{sharedNAT: true, isolatedNAT: true})
}

func TestPrivateRuntimeHostEndpointDependentNAT(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{hostNAT: true, hostDependentNAT: true})
}

func TestPrivateRuntimeClientEndpointDependentNAT(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{clientNAT: true, clientDependentNAT: true})
}

func TestPrivateRuntimeClientNATRemap(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{clientNAT: true, remap: true})
}

func TestPrivateRuntimeCombinedHostingAndUpstream(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{combined: true})
}

func TestPrivateRuntimeActiveProfileDeletion(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{combined: true, profileDelete: true})
}

func TestPrivateRuntimeHostingSameBootCrashRecovery(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{hostCrash: true})
}

func TestPrivateRuntimeCombinedSameBootCrashRecovery(t *testing.T) {
	testPrivateRuntime(t, privateRuntimeCase{hostCrash: true, combined: true})
}

type privateRuntimeCase struct {
	autoInvite                                  bool
	profileDelete                               bool
	selectiveRevocation                         bool
	multipleClients                             bool
	hostDNS                                     bool
	guiShutdown                                 bool
	hostingGUICrash                             bool
	systemdCrash                                bool
	socketActivated                             bool
	realReboot                                  bool
	guiOnboarding                               bool
	isolatedNAT                                 bool
	sharedNAT                                   bool
	guiCrash                                    bool
	revokeICE                                   bool
	ownerRestart                                bool
	hostDependentNAT                            bool
	clientDependentNAT                          bool
	hostNAT                                     bool
	disjoint, crash, clientNAT, remap, combined bool
	hostCrash                                   bool
	cycles                                      int
}

func testPrivateRuntime(t *testing.T, scenario privateRuntimeCase) {
	t.Helper()
	disjoint, cycles, crash := scenario.disjoint, scenario.cycles, scenario.crash
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	launcher := assertNormalUserExecutable(t, "HYDRA_M4B_LAB_DNS_LAUNCHER")
	assertNormalUserExecutable(t, "HYDRA_M4B_RFC5780")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	var conntrackHelper string
	if scenario.remap {
		conntrackHelper = assertNormalUserExecutable(t, "HYDRA_M4B_CONNTRACK")
	}
	lab := newLab(t, "private-runtime")
	nodeCount := 8
	if scenario.combined || scenario.multipleClients {
		nodeCount = 9
	}
	serverIndices := []int{3, 4}
	if disjoint {
		nodeCount = 9
		serverIndices = append(serverIndices, 8)
	}
	namespaces, ips := buildProductRoutedTopology(lab, nodeCount)
	if scenario.hostDNS {
		attachPrivateDNSHost(t, lab, namespaces, ips)
	}
	clientWAN := ips[6]
	if scenario.clientNAT {
		clientWAN = installPrivateClientNAT(t, lab, ips[6])
		if scenario.clientDependentNAT {
			setPrivatePeerDependentMapping(lab, 6, ips[6], clientWAN, 0, ips[3:5])
		}
	}
	hostWAN := ips[5]
	if scenario.hostNAT {
		hostWAN = installPrivatePeerNAT(t, lab, ips[5], 5)
	}
	if scenario.sharedNAT {
		hostWAN = installPrivateSharedNAT(t, lab, ips)
		clientWAN = hostWAN
		if scenario.isolatedNAT {
			lab.inNamespace(lab.name+"-router", "nft", "insert", "rule", "ip", "private_shared_nat", "forward",
				"ip", "saddr", "{", ips[5].String(), ",", ips[6].String(), "}",
				"ip", "daddr", "{", ips[5].String(), ",", ips[6].String(), "}", "counter", "drop")
		}
	}
	base := lab.tempDir("private-runtime")
	geoIP := filepath.Join(base, "country.mmdb")
	geoRecords := map[string]string{
		"198.18.54.0/24": "NL", "198.18.55.0/24": "NL", "198.18.56.0/24": "RU",
		"198.18.59.0/24": "NL",
	}
	if scenario.combined {
		geoRecords["198.18.59.0/24"] = "DE"
	}
	if scenario.hostNAT {
		geoRecords["198.18.91.0/24"] = "RU"
	}
	writeM12CountryDatabaseRecords(t, geoIP, geoRecords)
	var seeds []string
	for index := 0; index < 3; index++ {
		label := fmt.Sprintf("private-verifier-%d", index)
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
	encodeSeeds := func(addresses []string) string {
		t.Helper()
		bundle, err := productbootstrap.FromMultiaddrs(addresses, 0)
		if err != nil {
			t.Fatal(err)
		}
		code, err := productbootstrap.Encode(bundle)
		if err != nil {
			t.Fatal(err)
		}
		return code
	}
	seedCode := encodeSeeds(seeds)
	servers := make(map[int]*childProcess)
	serverIDs := make(map[int]string)
	for _, index := range serverIndices {
		label := fmt.Sprintf("private-server-%d", index)
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
			t.Fatal("server PeerID is not observable")
		}
		servers[index], serverIDs[index] = child, peerID
		seeds = append(seeds, fmt.Sprintf("/ip4/%s/tcp/4000/p2p/%s", ips[index], peerID))
		waitM11Log(t, lab, child, label+".stderr.log", "EXIT_CAPABILITY_READY", 35*time.Second, nil)
	}
	dnsPort := uint16(1053)
	if scenario.hostDNS {
		dnsPort = 53
	}
	internet, release, _ := startM7InternetServer(t, lab, m7Topology{InternetNamespace: namespaces[7], Internet4: ips[7]}, "--dns-port", strconv.Itoa(int(dnsPort)))
	defer func() {
		_ = os.WriteFile(release, []byte("stop\n"), 0o600)
		_ = stopM10Child(internet, 5*time.Second)
	}()
	// Budget registration refill plus bounded remote RELEASE drain without changing product timers.
	maximum := 4*time.Minute + time.Duration(cycles)*20*time.Second
	if scenario.guiOnboarding || scenario.realReboot {
		maximum = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	// Verifier-only anchors are not product servers and do not serve underlay challenges.
	seeds = seeds[3:]
	hostCode, clientCode := encodeSeeds(seeds), encodeSeeds(seeds)
	if disjoint {
		clientCode = encodeSeeds(seeds[:1])
		lab.inNamespace(namespaces[5], "nft", "add", "table", "inet", "private_partition")
		lab.inNamespace(namespaces[5], "nft", "add", "chain", "inet", "private_partition", "out", "{ type filter hook output priority -100; policy accept; }")
		lab.inNamespace(namespaces[5], "nft", "add", "chain", "inet", "private_partition", "in", "{ type filter hook input priority -100; policy accept; }")
		// Keep one bootstrap-derived STUN service alive while forcing disjoint TCP registration owners.
		lab.inNamespace(namespaces[5], "nft", "add", "rule", "inet", "private_partition", "out", "ip", "daddr", ips[3].String(), "meta", "l4proto", "tcp", "reject", "with", "tcp", "reset")
		lab.inNamespace(namespaces[5], "nft", "add", "rule", "inet", "private_partition", "in", "ip", "saddr", ips[3].String(), "meta", "l4proto", "tcp", "reject", "with", "tcp", "reset")
	}
	desktops := make(map[int]*childProcess)
	desktopSockets := make(map[int]string)
	systemdUnit := "irvio-private-" + lab.name + ".service"
	systemdStopped := false
	startDesktop := func(index int, label, code string) *localapi.Client {
		t.Helper()
		directory := filepath.Join(base, label)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		socket := filepath.Join(directory, "control.sock")
		if scenario.socketActivated && index == 6 {
			socket = filepath.Join(base, label+"-control.sock")
		}
		desktopSockets[index] = socket
		runtimeRoot := filepath.Join(base, label+"-runtime")
		if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		// Match the service's RuntimeDirectory without sharing intent between lab nodes.
		if err := os.Mkdir(filepath.Join(runtimeRoot, "irvio"), 0o700); err != nil {
			t.Fatal(err)
		}
		arguments := []string{"--runtime-root", runtimeRoot, irviod,
			"--role", "desktop", "--data-dir", directory, "--local-api-socket", socket,
			"--local-api-group", "root", "--geoip-db", geoIP, "--allow-test-public-ip"}
		binary := launcher
		if scenario.hostDNS && index == 6 {
			binary = "nsenter"
			arguments = append([]string{"--target=1", "--net", "--mount", irviod}, arguments[3:]...)
		}
		if scenario.systemdCrash && index == 6 {
			unitArguments := []string{"--unit=" + systemdUnit, "--collect", "--service-type=exec",
				"--property=Restart=on-failure", "--property=RestartSec=2s", "--property=TimeoutStopSec=30s",
				fmt.Sprintf("--property=NetworkNamespacePath=/proc/%d/root/run/netns/%s", os.Getpid(), namespaces[index])}
			if scenario.socketActivated {
				unitArguments = append(unitArguments, "--socket-property=ListenStream="+socket,
					"--socket-property=SocketMode=0600", "--socket-property=RemoveOnStop=yes",
					"--property=StandardOutput=append:"+filepath.Join(lab.artifactDir, label+".stdout.log"),
					"--property=StandardError=append:"+filepath.Join(lab.artifactDir, label+".stderr.log"))
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
					defer stop()
					if output, err := exec.CommandContext(cleanup, "systemctl", "stop", strings.TrimSuffix(systemdUnit, ".service")+".socket").CombinedOutput(); err != nil {
						t.Errorf("stop lab activation socket: %v %s", err, output)
					}
				})
			} else {
				unitArguments = append(unitArguments, "--wait", "--pipe")
			}
			arguments = append(append(unitArguments, launcher), arguments...)
			binary = "systemd-run"
			t.Cleanup(func() {
				if systemdStopped {
					return
				}
				cleanup, stop := context.WithTimeout(context.Background(), 40*time.Second)
				defer stop()
				if output, err := exec.CommandContext(cleanup, "systemctl", "stop", systemdUnit).CombinedOutput(); err != nil {
					t.Errorf("stop private transient unit: %v %s", err, output)
				}
			})
		}
		child := lab.startChild(label, namespaces[index], binary, arguments...)
		desktops[index] = child
		if scenario.socketActivated && index == 6 {
			ready, stop := context.WithTimeout(ctx, 15*time.Second)
			defer stop()
			for {
				info, err := os.Lstat(socket)
				if err == nil && info.Mode()&os.ModeSocket != 0 {
					break
				}
				if ready.Err() != nil || err != nil && !os.IsNotExist(err) {
					t.Fatalf("activation socket did not appear: %v %v", err, ready.Err())
				}
				time.Sleep(20 * time.Millisecond)
			}
			if pid := strings.TrimSpace(lab.run("systemctl", "show", systemdUnit, "--property=MainPID", "--value")); pid != "0" {
				t.Fatal("socket daemon started before API access:", pid)
			}
		} else {
			waitM11Log(t, lab, child, label+".stderr.log", "PRODUCT_RUNTIME_READY", 15*time.Second, nil)
		}
		dialContext, dialCancel := context.WithTimeout(ctx, 15*time.Second)
		api, err := localapi.Dial(dialContext, socket)
		dialCancel()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = api.Close() })
		if scenario.hostDNS && index == 6 {
			registerPrivateHostDNSCleanup(t, lab, api, socket)
		}
		if !api.SupportsPrivateNetwork() {
			t.Fatal("production daemon did not negotiate private support")
		}
		if _, err := api.ImportBootstrapBundle(ctx, code); err != nil {
			t.Fatalf("%s bootstrap import: %v", label, err)
		}
		return api
	}
	host := startDesktop(5, "private-host", hostCode)
	client := startDesktop(6, "private-client", clientCode)
	lab.inNamespace(namespaces[6], "nft", "add", "table", "inet", "private_oracle")
	lab.inNamespace(namespaces[6], "nft", "add", "chain", "inet", "private_oracle", "egress", "{ type filter hook postrouting priority 300; policy accept; }")
	lab.inNamespace(namespaces[6], "nft", "add", "rule", "inet", "private_oracle", "egress",
		"ip", "saddr", ips[6].String(), "ip", "daddr", ips[7].String(), "counter", "comment", "private-user-wan-leak")
	if scenario.combined {
		lab.inNamespace(namespaces[5], "nft", "add", "table", "inet", "private_host_oracle")
		lab.inNamespace(namespaces[5], "nft", "add", "chain", "inet", "private_host_oracle", "egress", "{ type filter hook output priority 300; policy accept; }")
		lab.inNamespace(namespaces[5], "nft", "add", "rule", "inet", "private_host_oracle", "egress",
			"ip", "saddr", ips[5].String(), "ip", "daddr", ips[7].String(), "counter", "comment", "private-host-user-wan-leak")
	}
	initialNetwork := make(map[int]map[string]string)
	var hostDNSBaseline map[string]string
	if scenario.hostDNS {
		hostDNSBaseline = privateHostDNSState(t, lab)
	}
	if cycles > 0 || scenario.hostCrash || scenario.hostNAT || scenario.revokeICE || scenario.sharedNAT || scenario.systemdCrash || scenario.clientDependentNAT || scenario.multipleClients {
		for _, index := range []int{5, 6} {
			waitPrivateAddressReady(t, ctx, lab, namespaces[index])
			initialNetwork[index] = privateIdleNetwork(t, lab, namespaces[index])
		}
	}
	state := func(api *localapi.Client) *localv1.PrivateState {
		t.Helper()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		for {
			result, err := api.GetPrivateState(ctx, 0, 64)
			if err == nil {
				return result
			}
			var response *localapi.ResponseError
			if !errors.As(err, &response) || response.Code != localv1.ResultCode_RESULT_CODE_BUSY {
				t.Fatalf("read private state: %v", err)
			}
			// Snapshot reads deliberately return BUSY instead of waiting on the lifecycle lock.
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-deadline.C:
				t.Fatalf("private state stayed BUSY for 3s: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	command := func(api *localapi.Client, request *localv1.PrivateCommand) *localv1.PrivateCommandResult {
		t.Helper()
		result, err := api.PrivateCommand(ctx, request)
		if err != nil {
			t.Fatalf("private action %s: %v", request.Action, err)
		}
		return result
	}
	hostID, clientID := state(host).LocalPeerId, state(client).LocalPeerId
	enroll := func(host, client *localapi.Client) {
		t.Helper()
		hostID, clientID := state(host).LocalPeerId, state(client).LocalPeerId
		invitation := command(host, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CREATE_INVITATION, LifetimeSeconds: 3600, AutoApprove: scenario.autoInvite})
		command(client, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_IMPORT_INVITATION, InvitationLink: invitation.InvitationLink, Name: "lab host"})
		resume := func() *localv1.PrivateCommandResult {
			t.Helper()
			for attempt := 1; attempt <= 5; attempt++ {
				result, err := client.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_RESUME_ENROLLMENT, PeerId: hostID})
				if err == nil {
					return result
				}
				var response *localapi.ResponseError
				if !errors.As(err, &response) || response.Code != localv1.ResultCode_RESULT_CODE_NOT_READY || attempt == 5 {
					t.Fatalf("enrollment attempt %d: %v", attempt, err)
				}
				t.Logf("enrollment attempt %d: %s", attempt, response.Code)
				select {
				case <-time.After(2 * time.Second):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			panic("unreachable")
		}
		pending := resume()
		if scenario.autoInvite {
			if pending.EnrollmentState != "APPROVED" || len(state(client).Targets) != 1 {
				t.Fatal("access invitation did not produce an approved saved target")
			}
			members := state(host).Members
			if len(members) != 1 || members[0].PeerId != clientID || !members[0].Allowed || members[0].GetConnected() {
				t.Fatal("wrong member identity or false connected state before CONNECT")
			}
			return
		}
		if pending.EnrollmentState != "PENDING" || len(state(client).Targets) != 0 {
			t.Fatalf("unapproved enrollment unexpectedly granted access: state=%s", pending.EnrollmentState)
		}
		if _, err := client.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, PeerId: hostID}); err == nil {
			t.Fatal("unapproved imported target accepted CONNECT")
		}
		hostState := state(host)
		invitationID := ""
		for _, entry := range hostState.Invitations {
			if entry.ClientPeerId == clientID {
				if invitationID != "" {
					t.Fatal("ambiguous pending client invitation")
				}
				invitationID = entry.InvitationId
			}
		}
		if invitationID == "" {
			t.Fatal("owner pending approval does not bind the actual client")
		}
		command(host, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_APPROVE_INVITATION, InvitationId: invitationID, Name: "lab client"})
		approved := resume()
		if approved.EnrollmentState != "APPROVED" {
			t.Fatalf("approved enrollment did not complete: %s", approved.EnrollmentState)
		}
	}
	var upstream *localapi.Client
	var upstreamID string
	if scenario.combined {
		upstream = startDesktop(8, "private-upstream", hostCode)
		for _, index := range []int{5, 6, 8} {
			waitPrivateAddressReady(t, ctx, lab, namespaces[index])
			initialNetwork[index] = privateIdleNetwork(t, lab, namespaces[index])
		}
		upstreamID = state(upstream).LocalPeerId
		command(upstream, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, Hosting: true})
		waitPrivateRuntimeState(t, ctx, upstream, localv1.ConnectionState_CONNECTION_STATE_HOSTING)
		enroll(upstream, host)
		command(host, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, PeerId: upstreamID, Hosting: true})
		status := waitPrivateRuntimeState(t, ctx, host, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
		if status.PrivateTargetPeerId != upstreamID || !status.PrivateHostingWanted || status.CountryCode != "" {
			t.Fatal("combined intent lost private hosting or pinned upstream")
		}
	} else if !scenario.guiOnboarding {
		command(host, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, Hosting: true})
		waitPrivateRuntimeState(t, ctx, host, localv1.ConnectionState_CONNECTION_STATE_HOSTING)
	}
	if disjoint {
		waitM11Log(t, lab, desktops[5], "private-host.stderr.log", "RENDEZVOUS_REGISTRATIONS", 45*time.Second, func(event map[string]any) bool {
			values, ok := event["servers"].([]any)
			if !ok || len(values) != 2 {
				return false
			}
			seen := map[string]bool{}
			for _, value := range values {
				id, _ := value.(string)
				seen[id] = true
			}
			return seen[serverIDs[4]] && seen[serverIDs[8]] && !seen[serverIDs[3]]
		})
		t.Log("host registered on two owners disjoint from client's sole bootstrap")
	}
	if scenario.hostNAT {
		started := waitM11Log(t, lab, desktops[5], "private-host.stderr.log", "ICE_ADAPTER_STARTED", 10*time.Second, nil)
		port, ok := started["port"].(float64)
		if !ok || port < 1 || port > 65535 || port != float64(int(port)) {
			t.Fatal("NAT mapping lacks a valid observed host UDP port")
		}
		setPrivatePeerMapping(lab, 5, ips[5], hostWAN, int(port), 42000)
		if scenario.hostDependentNAT {
			setPrivatePeerDependentMapping(lab, 5, ips[5], hostWAN, int(port), ips[3:5])
		}
	}
	if scenario.guiOnboarding {
		runPrivateManualGUIOnboarding(t, ctx, lab, client, namespaces, base)
	} else {
		enroll(host, client)
	}
	t.Log("real routed enrollment approved; connecting pinned target")
	if scenario.revokeICE {
		lab.installNFT(namespaces[6], "hold-private-ice", fmt.Sprintf(`table inet private_ice_hold {
chain out { type filter hook output priority -200; policy accept; ip daddr %s meta l4proto udp drop; }
chain in { type filter hook input priority -200; policy accept; ip saddr %s meta l4proto udp drop; }
}
`, ips[5], ips[5]))
	}
	connectStarted := time.Now()
	if !scenario.guiOnboarding {
		command(client, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, PeerId: hostID})
	}
	if scenario.isolatedNAT {
		waitM11Log(t, lab, desktops[5], "private-host.stderr.log", "BROKERED_ICE_ACCEPT_STARTED", 30*time.Second, func(event map[string]any) bool {
			return event["peer_id"] == clientID
		})
		runPrivateUnreachableNAT(t, ctx, lab, host, client, namespaces, ips, base, hostID, hostWAN, initialNetwork, connectStarted)
		return
	}
	if scenario.revokeICE {
		waitM11Log(t, lab, desktops[5], "private-host.stderr.log", "BROKERED_ICE_ACCEPT_STARTED", 30*time.Second, func(event map[string]any) bool {
			return event["peer_id"] == clientID
		})
		started := time.Now()
		command(host, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_REVOKE_MEMBER, PeerId: clientID})
		if time.Since(started) > 12*time.Second {
			t.Fatal("in-flight revocation exceeded its bounded cleanup allowance")
		}
		lab.inNamespace(namespaces[6], "nft", "delete", "table", "inet", "private_ice_hold")
		until := time.Now().Add(15 * time.Second)
		for time.Now().Before(until) {
			status, err := client.GetStatus(ctx)
			if err != nil || status.GetState() == localv1.ConnectionState_CONNECTION_STATE_CONNECTED || status.GetCountryCode() != "" || status.GetPrivateTargetPeerId() != hostID {
				t.Fatalf("revoked ICE attempt connected or changed target: status=%v err=%v", status, err)
			}
			probe, probeErr := tryM7Probe(t, lab, namespaces[6], "http", netip.AddrPortFrom(ips[7], 18080))
			if probeErr == nil && probe.Success {
				t.Fatal("revoked in-flight attempt passed user traffic")
			}
		}
		data, err := os.ReadFile(filepath.Join(lab.artifactDir, "private-host.stderr.log"))
		if err != nil || strings.Contains(string(data), "TUNNEL_LEASE_DATA_PLANE_ACTIVATED") {
			t.Fatalf("revoked ICE attempt activated a lease or log unavailable: %v", err)
		}
		stats, err := host.GetTrafficStats(ctx)
		if err != nil || stats.GetClients() != 0 {
			t.Fatalf("revoked ICE retained downstream clients: %v %v", stats, err)
		}
		for _, member := range state(host).Members {
			if member.PeerId == clientID && member.Allowed {
				t.Fatal("revocation left membership authorized")
			}
		}
		rules := lab.inNamespace(namespaces[6], "nft", "-j", "list", "table", "inet", "private_oracle")
		if count, found := counterForComment(rules, "private-user-wan-leak"); !found || count != 0 {
			t.Fatalf("revoked ICE WAN leak counter: %d present=%t", count, found)
		}
		for _, item := range []struct {
			api   *localapi.Client
			index int
			label string
		}{{client, 6, "private-client"}, {host, 5, "private-host"}} {
			if err := item.api.Disconnect(ctx); err != nil {
				t.Fatal(err)
			}
			waitPrivateRuntimeState(t, ctx, item.api, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
			store := productintent.Store{Path: filepath.Join(base, item.label+"-runtime", "irvio", "session-state")}
			if _, present, err := store.Read(); err != nil || present {
				t.Fatalf("revoked ICE retained intent: %v", err)
			}
			if !reflect.DeepEqual(initialNetwork[item.index], privateIdleNetwork(t, lab, namespaces[item.index])) {
				t.Fatal("revoked ICE teardown retained network residue")
			}
			probe := runM7Probe(t, lab, namespaces[item.index], "http", netip.AddrPortFrom(ips[7], 18080))
			observed, err := netip.ParseAddrPort(probe.ObservedSource)
			if err != nil || !probe.Success || observed.Addr() != ips[item.index] {
				t.Fatal("ordinary WAN not restored after revoked ICE")
			}
		}
		t.Log("revocation during blocked ICE prevented activation after UDP restoration; full leave clean")
		return
	}
	clientUDPPort := 0
	if scenario.clientNAT {
		started := waitM11Log(t, lab, desktops[6], "private-client.stderr.log", "ICE_ADAPTER_STARTED", 10*time.Second, nil)
		port, ok := started["port"].(float64)
		if !ok || port < 1 || port > 65535 || port != float64(int(port)) {
			t.Fatal("NAT mapping lacks a valid observed client UDP port")
		}
		clientUDPPort = int(port)
		if scenario.clientDependentNAT {
			setPrivatePeerDependentMapping(lab, 6, ips[6], clientWAN, clientUDPPort, ips[3:5])
		} else {
			setPrivateClientMapping(lab, ips[6], clientWAN, clientUDPPort, 42000)
		}
	}
	connected := waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
	if scenario.autoInvite {
		members := state(host).Members
		if len(members) != 1 || members[0].PeerId != clientID || !members[0].GetConnected() {
			t.Fatal("connected private lease not visible in owner card")
		}
	}
	if connected.PrivateTargetPeerId != hostID || connected.CountryCode != "" {
		t.Fatal("private connection lost its exact target or acquired a public country")
	}
	if scenario.hostDNS {
		checkPrivateHostDNS(t, lab, netip.MustParseAddr("1.1.1.1"))
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	client, err := localapi.Dial(ctx, desktopSockets[6])
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
	countries, err := client.GetAvailableCountries(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(countries.CountryCodes) != 1 || countries.CountryCodes[0] != "NL" {
		t.Fatalf("public country view lost public supply or exposed private host: %v", countries)
	}
	target := netip.AddrPortFrom(ips[7], 18080)
	var upstreamPackets uint64
	if scenario.combined {
		for _, traffic := range []struct {
			kind string
			port uint16
		}{{"http", 18080}, {"udp", 18081}} {
			own := runM7Probe(t, lab, namespaces[5], traffic.kind, netip.AddrPortFrom(ips[7], traffic.port))
			source, err := netip.ParseAddrPort(own.ObservedSource)
			if err != nil || !own.Success || source.Addr() != ips[8] {
				t.Fatalf("combined host own traffic bypassed private upstream: %+v parse=%v", own, err)
			}
		}
		upstreamPackets = requireCounter(t, lab.inNamespace(namespaces[8], "nft", "-j", "list", "table", "inet", "hydra_exit"), "masquerade-ipv4", 1)
	}
	probe := runM7Probe(t, lab, namespaces[6], "http", target)
	source, err := netip.ParseAddrPort(probe.ObservedSource)
	if err != nil || !probe.Success || source.Addr() != hostWAN {
		t.Fatalf("private traffic did not leave through the authorized host: %+v parse=%v", probe, err)
	}
	if scenario.realReboot {
		holdPrivateForReboot(t, ctx, lab, client, base, hostID)
		return
	}
	if scenario.guiCrash {
		runPrivateGUICrash(t, ctx, lab, client, desktops[6], namespaces[6], base, hostID, hostWAN, target)
	}
	if scenario.hostingGUICrash {
		runPrivateHostingGUICrash(t, ctx, lab, host, client, desktops[5], namespaces, base, hostID, hostWAN, target)
	}
	if scenario.guiShutdown {
		runPrivateCombinedGUIShutdown(t, ctx, lab, host, client, upstream, desktops[5], namespaces, base, upstreamID, ips, target, initialNetwork)
		return
	}
	if scenario.profileDelete {
		runPrivateActiveProfileDeletion(t, ctx, lab, host, client, upstream, desktops[5], namespaces, base, ips, target, initialNetwork)
		return
	}
	if scenario.sharedNAT {
		for _, item := range []struct {
			index int
			label string
		}{{5, "private-host"}, {6, "private-client"}} {
			waitM11Log(t, lab, desktops[item.index], item.label+".stderr.log", "ICE_SRFLX_REFRESHED", 5*time.Second, func(event map[string]any) bool {
				value, _ := event["mapped_endpoint"].(string)
				mapped, err := netip.ParseAddrPort(value)
				return err == nil && mapped.Addr() == hostWAN
			})
		}
		requireCounter(t, lab.inNamespace(namespaces[5], "nft", "-j", "list", "table", "inet", "hydra_exit"), "masquerade-ipv4", 1)
		t.Log("both private peers observed the same public STUN IP; traffic traversed the authorized Exit")
	}
	if scenario.hostNAT {
		mappedPort := uint16(42000)
		if scenario.hostDependentNAT {
			mappedPort = 43000
			waitM11Log(t, lab, desktops[5], "private-host.stderr.log", "ICE_SRFLX_REFRESHED", 5*time.Second, func(event map[string]any) bool {
				return event["mapped_endpoint"] == netip.AddrPortFrom(hostWAN, 42000).String()
			})
			started := waitM11Log(t, lab, desktops[5], "private-host.stderr.log", "BROKERED_ICE_ACCEPT_STARTED", 5*time.Second, func(event map[string]any) bool {
				return event["peer_id"] == clientID
			})
			candidates, ok := started["local_candidates"].([]any)
			if !ok || len(candidates) == 0 {
				t.Fatal("host signaling candidates not observable")
			}
			seenSTUN := false
			for _, candidate := range candidates {
				if candidate == netip.AddrPortFrom(hostWAN, 43000).String() {
					t.Fatal("peer-specific NAT mapping was unexpectedly preadvertised")
				}
				seenSTUN = seenSTUN || candidate == netip.AddrPortFrom(hostWAN, 42000).String()
			}
			if !seenSTUN {
				t.Fatal("host signaling did not advertise observed STUN mapping")
			}
		}
		data, err := os.ReadFile(filepath.Join(lab.artifactDir, "private-client.stderr.log"))
		if err != nil {
			t.Fatal(err)
		}
		matched := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, " BROKERED_ICE_CONNECTED peer_id="+hostID+" ") &&
				strings.HasSuffix(line, " remote_endpoint="+netip.AddrPortFrom(hostWAN, mappedPort).String()) {
				matched = true
			}
		}
		if !matched {
			t.Fatal("client ICE evidence lacks the exact mapped endpoint of the private host")
		}
	}
	if scenario.clientNAT {
		mappedPort := uint16(42000)
		if scenario.clientDependentNAT {
			mappedPort = 43000
			waitM11Log(t, lab, desktops[6], "private-client.stderr.log", "ICE_SRFLX_REFRESHED", 5*time.Second, func(event map[string]any) bool {
				return event["mapped_endpoint"] == netip.AddrPortFrom(clientWAN, 42000).String()
			})
			data, err := os.ReadFile(filepath.Join(lab.artifactDir, "private-client.stderr.log"))
			if err != nil {
				t.Fatal(err)
			}
			advertised := false
			for _, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, " BROKERED_ICE_CONNECT_STARTED peer_id="+hostID+" ") {
					if strings.Contains(line, netip.AddrPortFrom(clientWAN, mappedPort).String()) {
						t.Fatal("client unexpectedly advertised its peer-specific mapping")
					}
					advertised = advertised || strings.Contains(line, netip.AddrPortFrom(clientWAN, 42000).String())
				}
			}
			if !advertised {
				t.Fatal("client signaling lacks its observed STUN mapping")
			}
		}
		waitM11Log(t, lab, desktops[5], "private-host.stderr.log", "BROKERED_ICE_ACCEPT_CONNECTED", 5*time.Second, func(event map[string]any) bool {
			endpoint, _ := event["remote_endpoint"].(string)
			return endpoint == netip.AddrPortFrom(clientWAN, mappedPort).String()
		})
	}
	probe = runM7Probe(t, lab, namespaces[6], "udp", netip.AddrPortFrom(ips[7], 18081))
	source, err = netip.ParseAddrPort(probe.ObservedSource)
	if err != nil || !probe.Success || source.Addr() != hostWAN {
		t.Fatalf("private UDP did not use the pinned host: %+v parse=%v", probe, err)
	}
	for _, kind := range []string{"dns-udp", "dns-tcp"} {
		probe = runM7Probe(t, lab, namespaces[6], kind, netip.AddrPortFrom(ips[7], dnsPort))
		if !probe.Success || probe.DNSAnswer != "198.51.100.77" {
			t.Fatalf("private %s failed: %+v", kind, probe)
		}
	}
	lab.inNamespace(namespaces[6], "ping", "-n", "-c", "2", "-W", "2", ips[7].String())
	if scenario.combined {
		after := requireCounter(t, lab.inNamespace(namespaces[8], "nft", "-j", "list", "table", "inet", "hydra_exit"), "masquerade-ipv4", 1)
		if after != upstreamPackets {
			t.Fatalf("downstream traffic reached upstream Exit: before=%d after=%d", upstreamPackets, after)
		}
		t.Log("combined host own traffic used upstream; downstream HTTP/UDP/DNS/ICMP bypassed upstream Exit")
	}
	if scenario.remap {
		streamRelease := filepath.Join(lab.artifactDir, "private-remap-stream-release")
		streamReady := filepath.Join(lab.artifactDir, "private-remap-stream-ready")
		streamResult := filepath.Join(lab.artifactDir, "private-remap-stream-result.json")
		stream := lab.startChild("private-remap-stream", namespaces[6], os.Getenv("HYDRA_M7_INTERNET"),
			"--mode", "stream-probe", "--target", netip.AddrPortFrom(ips[7], 18081).String(),
			"--expected-egress", ips[5].String(), "--probe-timeout", "200ms",
			"--ready", streamReady, "--release", streamRelease, "--result", streamResult)
		t.Cleanup(func() {
			_ = os.WriteFile(streamRelease, []byte("stop\n"), 0o600)
			stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = stream.wait(stopCtx)
		})
		waitM8File(t, stream, streamReady)
		remapped := netip.AddrPortFrom(clientWAN, 42001).String()
		setPrivateClientMapping(lab, ips[6], clientWAN, clientUDPPort, 42001)
		deleted := lab.inNamespace(lab.name+"-router", conntrackHelper, "--delete-udp-peer", ips[6].String())
		count, err := strconv.Atoi(strings.TrimSpace(deleted))
		if err != nil || count < 1 {
			t.Fatalf("remap did not invalidate client mappings: count=%q err=%v", deleted, err)
		}
		started := time.Now()
		waitM11Log(t, lab, desktops[6], "private-client.stderr.log", "ICE_SRFLX_REFRESHED", 120*time.Second, func(event map[string]any) bool {
			return event["mapped_endpoint"] == remapped
		})
		waitM11Log(t, lab, desktops[5], "private-host.stderr.log", "BROKERED_ICE_ACCEPT_CONNECTED", 45*time.Second, func(event map[string]any) bool {
			return event["remote_endpoint"] == remapped
		})
		recovered := waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
		if recovered.PrivateTargetPeerId != hostID || recovered.CountryCode != "" {
			t.Fatal("NAT remap changed pinned intent or used public fallback")
		}
		probe = runM7Probe(t, lab, namespaces[6], "http", target)
		source, err = netip.ParseAddrPort(probe.ObservedSource)
		if err != nil || !probe.Success || source.Addr() != ips[5] {
			t.Fatalf("remapped private user-plane failed: %+v parse=%v", probe, err)
		}
		if err := os.WriteFile(streamRelease, []byte("stop\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		waitM8Child(t, stream)
		var streamReport struct {
			Attempts, Successes, Failures, WrongEgress int
			Completed                                  bool
		}
		if err := readJSON(streamResult, &streamReport); err != nil || !streamReport.Completed || streamReport.Successes < 1 ||
			streamReport.Attempts < 2 || streamReport.WrongEgress != 0 || streamReport.Attempts != streamReport.Successes+streamReport.Failures {
			t.Fatalf("invalid remap stream proof: %+v err=%v", streamReport, err)
		}
		t.Logf("private NAT remap automatically recovered pinned traffic in %s; deleted %d UDP mappings", time.Since(started), count)
		t.Logf("ordinary UDP stream attempts=%d delivered=%d dropped_or_timed_out=%d wrong_egress=%d", streamReport.Attempts, streamReport.Successes, streamReport.Failures, streamReport.WrongEgress)
	}
	if crash {
		intentStore := productintent.Store{Path: filepath.Join(base, "private-client-runtime", "irvio", "session-state")}
		before, present, err := intentStore.Read()
		if err != nil || !present || !before.ConnectIntent || before.PrivateTarget != hostID || before.PrivateHosting || before.Country != "" {
			t.Fatalf("client lacks isolated pinned recovery intent: present=%t err=%v", present, err)
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		first := desktops[6]
		if err := syscall.Kill(-first.command.Process.Pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		select {
		case <-first.done:
		case <-time.After(8 * time.Second):
			t.Fatal("SIGKILL did not terminate private daemon")
		}
		waitM8ChildFailure(t, first)
		after, present, err := intentStore.Read()
		if err != nil || !present || before != after {
			t.Fatalf("same-boot crash lost or changed pinned intent: present=%t err=%v", present, err)
		}
		lab.inNamespace(namespaces[6], "nft", "list", "table", "inet", "hydra_protect")
		blocked, probeErr := tryM7Probe(t, lab, namespaces[6], "http", target)
		if probeErr == nil && blocked.Success {
			t.Fatal("user traffic succeeded while private daemon was dead")
		}
		rules := lab.inNamespace(namespaces[6], "nft", "-j", "list", "table", "inet", "private_oracle")
		if packets, exists := counterForComment(rules, "private-user-wan-leak"); !exists || packets != 0 {
			t.Fatalf("same-boot crash WAN leak packets=%d counter_present=%t", packets, exists)
		}
		directory := filepath.Join(base, "private-client")
		second := lab.startChild("private-client-restarted", namespaces[6], launcher,
			"--runtime-root", filepath.Join(base, "private-client-runtime"), irviod,
			"--role", "desktop", "--data-dir", directory, "--local-api-socket", filepath.Join(directory, "control.sock"),
			"--local-api-group", "root", "--geoip-db", geoIP, "--allow-test-public-ip")
		desktops[6] = second
		waitM11Log(t, lab, second, "private-client-restarted.stderr.log", "PRODUCT_RUNTIME_READY", 15*time.Second, nil)
		client, err = localapi.Dial(ctx, filepath.Join(directory, "control.sock"))
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		// No CONNECT or bootstrap import is sent after restarting the same profile/runtime.
		recovered := waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
		if recovered.PrivateTargetPeerId != hostID || recovered.CountryCode != "" || state(client).LocalPeerId != clientID {
			t.Fatal("same-boot recovery changed identity or private target")
		}
		probe = runM7Probe(t, lab, namespaces[6], "http", target)
		source, err = netip.ParseAddrPort(probe.ObservedSource)
		if err != nil || !probe.Success || source.Addr() != ips[5] {
			t.Fatalf("automatic pinned recovery failed user-plane proof: %+v parse=%v", probe, err)
		}
		t.Log("same-boot SIGKILL retained protection/intent and automatically restored pinned traffic")
	}
	if scenario.systemdCrash {
		client = runPrivateSystemdCrash(t, ctx, lab, client, namespaces[6], base, systemdUnit, desktopSockets[6], hostID, hostWAN, target)
		defer client.Close()
	}
	if scenario.hostCrash {
		intentStore := productintent.Store{Path: filepath.Join(base, "private-host-runtime", "irvio", "session-state")}
		before, present, err := intentStore.Read()
		if err != nil || !present || !before.PrivateHosting || before.ConnectIntent != scenario.combined || before.PrivateTarget != upstreamID || before.Country != "" {
			t.Fatalf("host lacks isolated recovery intent: present=%t err=%v", present, err)
		}
		if err := host.Close(); err != nil {
			t.Fatal(err)
		}
		first := desktops[5]
		started := time.Now()
		if err := syscall.Kill(-first.command.Process.Pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		select {
		case <-first.done:
		case <-time.After(8 * time.Second):
			t.Fatal("SIGKILL did not terminate private hosting daemon")
		}
		waitM8ChildFailure(t, first)
		after, present, err := intentStore.Read()
		if err != nil || !present || before != after {
			t.Fatalf("same-boot host crash changed hosting intent: present=%t err=%v", present, err)
		}
		blocked, probeErr := tryM7Probe(t, lab, namespaces[6], "http", target)
		if probeErr == nil && blocked.Success {
			t.Fatal("downstream traffic succeeded while private host was dead")
		}
		if scenario.combined {
			lab.inNamespace(namespaces[5], "nft", "list", "table", "inet", "hydra_protect")
			blocked, probeErr := tryM7Probe(t, lab, namespaces[5], "http", target)
			if probeErr == nil && blocked.Success {
				t.Fatal("combined owner's traffic escaped protection while its daemon was dead")
			}
		} else {
			ordinary := runM7Probe(t, lab, namespaces[5], "http", target)
			observed, err := netip.ParseAddrPort(ordinary.ObservedSource)
			if err != nil || !ordinary.Success || observed.Addr() != ips[5] {
				t.Fatalf("hosting-only crash blocked owner's ordinary WAN: %+v parse=%v", ordinary, err)
			}
		}
		directory := filepath.Join(base, "private-host")
		second := lab.startChild("private-host-restarted", namespaces[5], launcher,
			"--runtime-root", filepath.Join(base, "private-host-runtime"), irviod,
			"--role", "desktop", "--data-dir", directory, "--local-api-socket", filepath.Join(directory, "control.sock"),
			"--local-api-group", "root", "--geoip-db", geoIP, "--allow-test-public-ip")
		desktops[5] = second
		waitM11Log(t, lab, second, "private-host-restarted.stderr.log", "PRODUCT_RUNTIME_READY", 15*time.Second, nil)
		host, err = localapi.Dial(ctx, filepath.Join(directory, "control.sock"))
		if err != nil {
			t.Fatal(err)
		}
		defer host.Close()
		// Neither peer receives CONNECT, hosting enable or bootstrap import after the crash.
		if scenario.combined {
			recovered := waitPrivateRuntimeState(t, ctx, host, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
			if recovered.PrivateTargetPeerId != upstreamID || !recovered.PrivateHostingWanted || recovered.CountryCode != "" {
				t.Fatal("combined crash recovery changed private upstream/hosting intent")
			}
			own := runM7Probe(t, lab, namespaces[5], "http", target)
			observed, err := netip.ParseAddrPort(own.ObservedSource)
			if err != nil || !own.Success || observed.Addr() != ips[8] {
				t.Fatalf("combined crash recovery bypassed upstream: %+v parse=%v", own, err)
			}
			upstreamPackets = requireCounter(t, lab.inNamespace(namespaces[8], "nft", "-j", "list", "table", "inet", "hydra_exit"), "masquerade-ipv4", 1)
		} else {
			waitPrivateRuntimeState(t, ctx, host, localv1.ConnectionState_CONNECTION_STATE_HOSTING)
		}
		restored := state(host)
		if restored.LocalPeerId != hostID || !restored.HostingWanted || len(restored.Members) != 1 ||
			restored.Members[0].PeerId != clientID || !restored.Members[0].Allowed {
			t.Fatal("host restart lost identity, hosting intent or authenticated member ACL")
		}
		waitM11Log(t, lab, second, "private-host-restarted.stderr.log", "BROKERED_ICE_ACCEPT_CONNECTED", 120*time.Second, nil)
		recovered := waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
		if recovered.PrivateTargetPeerId != hostID || recovered.CountryCode != "" {
			t.Fatal("host crash recovery changed downstream target or fell back to public mode")
		}
		probe = runM7Probe(t, lab, namespaces[6], "http", target)
		source, err = netip.ParseAddrPort(probe.ObservedSource)
		if err != nil || !probe.Success || source.Addr() != ips[5] {
			t.Fatalf("restarted hosting failed pinned user-plane recovery: %+v parse=%v", probe, err)
		}
		if scenario.combined {
			after := requireCounter(t, lab.inNamespace(namespaces[8], "nft", "-j", "list", "table", "inet", "hydra_exit"), "masquerade-ipv4", 1)
			if after != upstreamPackets {
				t.Fatalf("host crash recovery introduced multihop: before=%d after=%d", upstreamPackets, after)
			}
		}
		t.Logf("host SIGKILL restored hosting/ACL and downstream pinned traffic in %s (combined=%t)", time.Since(started), scenario.combined)
	}
	if disjoint {
		if err := stopM10Child(servers[4], 10*time.Second); err != nil {
			t.Fatal(err)
		}
		probe = runM7Probe(t, lab, namespaces[6], "http", target)
		source, err = netip.ParseAddrPort(probe.ObservedSource)
		if err != nil || !probe.Success || source.Addr() != ips[5] {
			t.Fatalf("owner loss interrupted established private traffic: %+v parse=%v", probe, err)
		}
		if err := client.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
		command(client, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, PeerId: hostID})
		reconnected := waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
		if reconnected.PrivateTargetPeerId != hostID {
			t.Fatal("owner loss changed pinned target")
		}
		probe = runM7Probe(t, lab, namespaces[6], "http", target)
		source, err = netip.ParseAddrPort(probe.ObservedSource)
		if err != nil || !probe.Success || source.Addr() != ips[5] {
			t.Fatalf("fresh private connection through surviving owner failed: %+v parse=%v", probe, err)
		}
		t.Log("one owner stopped: direct traffic survived and fresh runtime rediscovered pinned host")
		if err := stopM10Child(servers[8], 10*time.Second); err != nil {
			t.Fatal(err)
		}
		expectedOwner := serverIDs[3]
		registrationWait := 90 * time.Second
		restartStarted := time.Now()
		if scenario.ownerRestart {
			// Existing discovery suppression is two minutes, followed by bounded reconciliation.
			registrationWait = 3 * time.Minute
			before := waitM11Log(t, lab, servers[4], "private-server-4.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", time.Second, nil)
			restarted := lab.startChild("private-owner-restarted", namespaces[4], irviod,
				"--role", "server", "--bootstrap", seedCode, "--data-dir", filepath.Join(base, "private-server-4"),
				"--geoip-db", geoIP, "--allow-test-public-ip")
			started := waitM11Log(t, lab, restarted, "private-owner-restarted.stderr.log", "PRODUCT_RUNTIME_STARTED", 15*time.Second, nil)
			if started["peer_id"] != serverIDs[4] {
				t.Fatal("owner restart did not preserve its PeerID")
			}
			after := waitM11Log(t, lab, restarted, "private-owner-restarted.stderr.log", "RENDEZVOUS_SERVICE_ACTIVE", 35*time.Second, nil)
			oldIncarnation, oldOK := before["server_incarnation"].(float64)
			newIncarnation, newOK := after["server_incarnation"].(float64)
			if !oldOK || !newOK || oldIncarnation == 0 || newIncarnation == 0 || oldIncarnation == newIncarnation {
				t.Fatal("owner restart lacks an observable new registry incarnation")
			}
			expectedOwner = serverIDs[4]
		} else {
			lab.inNamespace(namespaces[5], "nft", "delete", "table", "inet", "private_partition")
		}
		waitM11Log(t, lab, desktops[5], "private-host.stderr.log", "RENDEZVOUS_REGISTRATIONS", registrationWait, func(event map[string]any) bool {
			values, ok := event["servers"].([]any)
			return ok && len(values) == 1 && values[0] == expectedOwner
		})
		if scenario.ownerRestart {
			t.Logf("same-identity owner registration restored after %s", time.Since(restartStarted))
		}
		if err := client.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
		command(client, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, PeerId: hostID})
		reconnected = waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
		if reconnected.PrivateTargetPeerId != hostID {
			t.Fatal("owner migration changed pinned target")
		}
		probe = runM7Probe(t, lab, namespaces[6], "http", target)
		source, err = netip.ParseAddrPort(probe.ObservedSource)
		if err != nil || !probe.Success || source.Addr() != ips[5] {
			t.Fatalf("migrated owner failed to route a fresh private connection: %+v parse=%v", probe, err)
		}
		t.Logf("both old owner processes stopped: fresh pinned connection succeeded (same-identity restart=%t)", scenario.ownerRestart)
	}
	if cycles > 0 {
		runPrivateLifecycleCycles(t, ctx, lab, host, client, desktops, namespaces, ips, hostID, cycles, initialNetwork)
	}
	if scenario.multipleClients {
		second := startDesktop(8, "private-second", clientCode)
		runPrivateMultipleClients(t, ctx, lab, host, client, second, namespaces, ips, base, hostID, clientID, initialNetwork, scenario.selectiveRevocation, enroll)
		return
	}
	t.Log("private HTTP/UDP/DNS/ICMP verified after API reconnect; revoking membership")
	if scenario.autoInvite {
		var id string
		for _, invite := range state(host).Invitations {
			if invite.ClientPeerId == clientID {
				id = invite.InvitationId
			}
		}
		if id == "" {
			t.Fatal("missing consumed invitation")
		}
		command(host, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CANCEL_INVITATION, InvitationId: id})
	} else {
		command(host, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_REVOKE_MEMBER, PeerId: clientID})
	}
	if scenario.autoInvite {
		for _, member := range state(host).Members {
			if member.PeerId == clientID && (member.Allowed || member.GetConnected()) {
				t.Fatal("revoked private member still connected or allowed")
			}
		}
	}
	probe, probeErr := tryM7Probe(t, lab, namespaces[6], "http", target)
	if probeErr == nil && probe.Success {
		t.Fatalf("revoked client retained user-plane access or leaked to WAN: %+v", probe)
	}
	rules := lab.inNamespace(namespaces[6], "nft", "-j", "list", "table", "inet", "private_oracle")
	if packets, exists := counterForComment(rules, "private-user-wan-leak"); !exists || packets != 0 {
		t.Fatalf("private user WAN leak packets=%d counter_present=%t", packets, exists)
	}
	if scenario.combined {
		rules := lab.inNamespace(namespaces[5], "nft", "-j", "list", "table", "inet", "private_host_oracle")
		if packets, exists := counterForComment(rules, "private-host-user-wan-leak"); !exists || packets != 0 {
			t.Fatalf("combined host user WAN leak packets=%d counter_present=%t", packets, exists)
		}
		status := waitPrivateRuntimeState(t, ctx, host, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
		if status.PrivateTargetPeerId != upstreamID || !status.PrivateHostingWanted {
			t.Fatal("downstream revocation changed combined host intent")
		}
		own := runM7Probe(t, lab, namespaces[5], "http", target)
		observed, err := netip.ParseAddrPort(own.ObservedSource)
		if err != nil || !own.Success || observed.Addr() != ips[8] {
			t.Fatalf("downstream revocation disrupted upstream forwarding: %+v parse=%v", own, err)
		}
	}
	apis := []*localapi.Client{client, host}
	labels := []string{"private-client", "private-host"}
	if scenario.combined {
		apis = append(apis, upstream)
		labels = append(labels, "private-upstream")
	}
	for _, api := range apis {
		if err := api.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		idle := waitPrivateRuntimeState(t, ctx, api, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
		if idle.PrivateTargetPeerId != "" || idle.PrivateHostingWanted || idle.ContributionEnabled {
			t.Fatal("full leave retained private participation")
		}
		stats, err := api.GetTrafficStats(ctx)
		if err != nil || stats.GetClients() != 0 || stats.GetActive() {
			t.Fatalf("full leave retained traffic or downstream leases: stats=%v err=%v", stats, err)
		}
	}
	for _, label := range labels {
		store := productintent.Store{Path: filepath.Join(base, label+"-runtime", "irvio", "session-state")}
		if scenario.hostDNS && label == "private-client" {
			store.Path = "/run/irvio/session-state"
		}
		if _, present, err := store.Read(); err != nil || present {
			t.Fatalf("%s full leave retained recovery intent: present=%t err=%v", label, present, err)
		}
	}
	if scenario.hostDNS && !reflect.DeepEqual(hostDNSBaseline, privateHostDNSState(t, lab)) {
		t.Fatal("private DISCONNECT did not restore actual host DNS before lab cleanup")
	}
	probe = runM7Probe(t, lab, namespaces[6], "http", target)
	source, err = netip.ParseAddrPort(probe.ObservedSource)
	if err != nil || !probe.Success || source.Addr() != clientWAN {
		t.Fatalf("full leave failed to restore ordinary WAN: %+v parse=%v", probe, err)
	}
	if scenario.hostCrash || scenario.hostNAT || scenario.sharedNAT || scenario.systemdCrash || scenario.clientDependentNAT {
		for _, index := range []int{5, 6} {
			actual := privateIdleNetwork(t, lab, namespaces[index])
			if !reflect.DeepEqual(initialNetwork[index], actual) {
				t.Fatalf("private node %d retained network residue: before=%v after=%v", index, initialNetwork[index], actual)
			}
		}
	}
	if scenario.hostNAT || scenario.sharedNAT {
		ordinary := runM7Probe(t, lab, namespaces[5], "http", target)
		observed, err := netip.ParseAddrPort(ordinary.ObservedSource)
		if err != nil || !ordinary.Success || observed.Addr() != hostWAN {
			t.Fatalf("NAT host ordinary WAN not restored: %+v parse=%v", ordinary, err)
		}
	}
	if scenario.systemdCrash {
		if scenario.socketActivated {
			if err := client.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(15 * time.Second)
			for strings.TrimSpace(lab.run("systemctl", "show", systemdUnit, "--property=MainPID", "--value")) != "0" {
				if time.Now().After(deadline) {
					t.Fatal("socket-activated daemon did not stop")
				}
				time.Sleep(100 * time.Millisecond)
			}
			time.Sleep(3 * time.Second)
			if strings.TrimSpace(lab.run("systemctl", "show", systemdUnit, "--property=MainPID", "--value")) != "0" ||
				strings.TrimSpace(lab.run("systemctl", "show", systemdUnit, "--property=ExecMainStatus", "--value")) != "0" {
				t.Fatal("intentional socket daemon shutdown failed or restarted")
			}
			lab.run("systemctl", "is-active", strings.TrimSuffix(systemdUnit, ".service")+".socket")
		} else {
			runPrivateSystemdShutdown(t, ctx, lab, client, desktops[6], systemdUnit)
		}
		systemdStopped = true
	}
	if scenario.combined {
		for _, index := range []int{5, 6, 8} {
			actual := privateIdleNetwork(t, lab, namespaces[index])
			if !reflect.DeepEqual(initialNetwork[index], actual) {
				t.Fatalf("combined node %d retained network residue: before=%v after=%v", index, initialNetwork[index], actual)
			}
		}
		for _, index := range []int{5, 8} {
			ordinary := runM7Probe(t, lab, namespaces[index], "http", target)
			observed, err := netip.ParseAddrPort(ordinary.ObservedSource)
			if err != nil || !ordinary.Success || observed.Addr() != ips[index] {
				t.Fatalf("node %d ordinary WAN not restored: %+v parse=%v", index, ordinary, err)
			}
		}
	}
}

func installPrivateClientNAT(t *testing.T, lab *lab, inside netip.Addr) netip.Addr {
	t.Helper()
	return installPrivatePeerNAT(t, lab, inside, 6)
}

func installPrivatePeerNAT(t *testing.T, lab *lab, inside netip.Addr, index int) netip.Addr {
	t.Helper()
	outside := netip.AddrFrom4([4]byte{198, 18, byte(96 - index), 2})
	router := lab.name + "-router"
	// Put each NAT peer behind a routed port, not the shared public L2 bridge.
	insideIf := lab.interfaceName(byte('1' + index))
	gateway := fmt.Sprintf("198.18.%d.254/24", 51+index)
	lab.inNamespace(router, "ip", "link", "set", insideIf, "nomaster")
	lab.inNamespace(router, "ip", "addr", "del", gateway, "dev", "br0")
	lab.inNamespace(router, "ip", "addr", "add", gateway, "dev", insideIf)
	lab.inNamespace(router, "ip", "addr", "add", outside.String()+"/32", "dev", "lo")
	lab.installNFT(router, fmt.Sprintf("private-peer-%d-eim", index), fmt.Sprintf(`table ip private_peer_nat_%d {
 chain prerouting {
  type nat hook prerouting priority dstnat; policy accept;
 }
 chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
  ip saddr %s meta l4proto udp counter snat to %s:42000
  ip saddr %s counter snat to %s
 }
 chain forward {
  type filter hook forward priority filter; policy accept;
  ip daddr %s ct state established,related counter accept
  ip daddr %s counter drop comment "private-peer-no-direct-bypass"
 }
}`, index, inside, outside, inside, outside, inside, inside))
	tcpdump, err := exec.LookPath("tcpdump")
	if err != nil {
		t.Fatal("private NAT diagnostic requires tcpdump:", err)
	}
	lab.startChild(fmt.Sprintf("private-nat-%d-packets", index), router, tcpdump, "-nn", "-l", "-tt", "-i", "any",
		"udp", "and", "(", "host", inside.String(), "or", "host", outside.String(), ")")
	return outside
}

func setPrivateClientMapping(lab *lab, inside, outside netip.Addr, localPort, mappedPort int) {
	lab.t.Helper()
	setPrivatePeerMapping(lab, 6, inside, outside, localPort, mappedPort)
}

func setPrivatePeerMapping(lab *lab, index int, inside, outside netip.Addr, localPort, mappedPort int) {
	lab.t.Helper()
	table := fmt.Sprintf("private_peer_nat_%d", index)
	lab.installNFT(lab.name+"-router", fmt.Sprintf("private-mapping-%d-%d", index, mappedPort), fmt.Sprintf(`flush chain ip %[1]s prerouting
add rule ip %[1]s prerouting ip daddr %[2]s udp dport %[3]d counter dnat to %[4]s:%[5]d
flush chain ip %[1]s postrouting
add rule ip %[1]s postrouting ip saddr %[4]s meta l4proto udp counter snat to %[2]s:%[3]d
add rule ip %[1]s postrouting ip saddr %[4]s counter snat to %[2]s
`, table, outside, mappedPort, inside, localPort))
}

func setPrivatePeerDependentMapping(lab *lab, peerIndex int, inside, outside netip.Addr, localPort int, stunPeers []netip.Addr) {
	lab.t.Helper()
	addresses := make([]string, len(stunPeers))
	for index, address := range stunPeers {
		addresses[index] = address.String()
	}
	// Different address/port destination classes get distinct mappings; APDF filtering stays installed.
	table := fmt.Sprintf("private_peer_nat_%d", peerIndex)
	rules := fmt.Sprintf("flush chain ip %s prerouting\n", table)
	if localPort != 0 {
		rules += fmt.Sprintf(`add rule ip %[1]s prerouting ip saddr { %[2]s } udp sport 3478 ip daddr %[3]s udp dport 42000 counter dnat to %[4]s:%[5]d
add rule ip %[1]s prerouting ip daddr %[3]s udp dport 43000 counter dnat to %[4]s:%[5]d
`, table, strings.Join(addresses, ", "), outside, inside, localPort)
	}
	rules += fmt.Sprintf(`flush chain ip %[1]s postrouting
add rule ip %[1]s postrouting ip saddr %[2]s ip daddr { %[3]s } udp dport 3478 counter snat to %[4]s:42000
add rule ip %[1]s postrouting ip saddr %[2]s meta l4proto udp counter snat to %[4]s:43000
add rule ip %[1]s postrouting ip saddr %[2]s counter snat to %[4]s
`, table, inside, strings.Join(addresses, ", "), outside)
	lab.installNFT(lab.name+"-router", fmt.Sprintf("private-dependent-mapping-%d", peerIndex), rules)
}

func waitPrivateRuntimeState(t *testing.T, ctx context.Context, api *localapi.Client, want localv1.ConnectionState) *localv1.Status {
	t.Helper()
	deadline := time.NewTimer(75 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last *localv1.Status
	for {
		status, err := api.GetStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		last = status
		if status.State == want {
			return status
		}
		select {
		case <-ctx.Done():
			t.Fatalf("private state wait: %v", ctx.Err())
		case <-deadline.C:
			t.Fatalf("private state want=%s last=%v", want, last)
		case <-ticker.C:
		}
	}
}
