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
)

type iceSignalCandidateReport struct {
	Type           string `json:"type"`
	Network        string `json:"network"`
	Address        string `json:"address"`
	Port           uint16 `json:"port"`
	RelatedAddress string `json:"related_address"`
	RelatedPort    uint16 `json:"related_port"`
	Foundation     string `json:"foundation"`
	Priority       uint32 `json:"priority"`
	Component      uint16 `json:"component"`
}

type iceSignalReport struct {
	Type                      string                     `json:"type"`
	Mode                      string                     `json:"mode"`
	PID                       int                        `json:"pid"`
	PeerID                    string                     `json:"peer_id"`
	ListenAddresses           []string                   `json:"listen_addresses"`
	MuxPort                   uint16                     `json:"mux_port"`
	LocalIPv4                 string                     `json:"local_ipv4"`
	LocalIPv6                 string                     `json:"local_ipv6"`
	SessionID                 string                     `json:"session_id"`
	RemotePeerID              string                     `json:"remote_peer_id"`
	LocalUfrag                string                     `json:"local_ufrag"`
	RemoteUfrag               string                     `json:"remote_ufrag"`
	ObservedSrflxIPv4         string                     `json:"observed_srflx_ipv4"`
	UDP4SocketInodeBeforeSTUN string                     `json:"udp4_socket_inode_before_stun"`
	UDP4SocketInodeReady      string                     `json:"udp4_socket_inode_ready"`
	UDP4SocketInodeAfter      string                     `json:"udp4_socket_inode_after"`
	UDP6SocketInodeBeforeSTUN string                     `json:"udp6_socket_inode_before_stun"`
	UDP6SocketInodeReady      string                     `json:"udp6_socket_inode_ready"`
	UDP6SocketInodeAfter      string                     `json:"udp6_socket_inode_after"`
	LocalCandidates           []iceSignalCandidateReport `json:"local_candidates"`
	RemoteCandidates          []iceSignalCandidateReport `json:"remote_candidates"`
	LearnedCandidates         []iceSignalCandidateReport `json:"learned_candidates"`
	ConnectivityRequests      uint64                     `json:"connectivity_requests"`
	Connected                 bool                       `json:"connected"`
	Role                      uint8                      `json:"role"`
	SelectedNetwork           uint8                      `json:"selected_network"`
	SelectedLocal             string                     `json:"selected_local"`
	SelectedRemote            string                     `json:"selected_remote"`
	SelectedLocalType         uint8                      `json:"selected_local_type"`
	SelectedRemoteType        uint8                      `json:"selected_remote_type"`
	Nominated                 bool                       `json:"nominated"`
	PairID                    uint64                     `json:"pair_id"`
	RequestsSent              uint64                     `json:"requests_sent"`
	ResponsesReceived         uint64                     `json:"responses_received"`
	ApplicationPackets        uint32                     `json:"application_packets"`
	ApplicationBytes          uint64                     `json:"application_bytes"`
	AdapterBefore             uint64                     `json:"adapter_before"`
	AdapterAfter              uint64                     `json:"adapter_after"`
	PostCloseAdapterClean     bool                       `json:"post_close_adapter_clean"`
	PostCloseDataWrite        bool                       `json:"post_close_data_write"`
	PostCloseSTUNLease        bool                       `json:"post_close_stun_lease"`
	NetworkGeneration         uint64                     `json:"network_generation"`
	SessionGeneration         uint64                     `json:"session_generation"`
	PreviousSessionID         string                     `json:"previous_session_id"`
	PreviousLocalUfrag        string                     `json:"previous_local_ufrag"`
	CredentialsRotated        bool                       `json:"credentials_rotated"`
	PionAgentReplaced         bool                       `json:"pion_agent_replaced"`
	DisconnectedObserved      bool                       `json:"disconnected_observed"`
	FailedObserved            bool                       `json:"failed_observed"`
	AWGHandshake              bool                       `json:"awg_handshake"`
	AWGInnerPacket            bool                       `json:"awg_inner_packet"`
	AWGSelectedRemote         string                     `json:"awg_selected_remote"`
	AWGSocketInode            string                     `json:"awg_socket_inode"`
	AWGSendPackets            uint64                     `json:"awg_send_packets"`
	AWGReceivePackets         uint64                     `json:"awg_receive_packets"`
	AWGPublicFingerprint      string                     `json:"awg_public_fingerprint"`
	AWGTUNName                string                     `json:"awg_tun_name"`
	AWGTUNIfIndex             int                        `json:"awg_tun_ifindex"`
	AWGTUNAddresses           []string                   `json:"awg_tun_addresses"`
	AWGSocketMark             uint32                     `json:"awg_socket_mark"`
	RoutingOutTable           int                        `json:"routing_out_table"`
	RoutingWANTable           int                        `json:"routing_wan_table"`
	RoutingRules              int                        `json:"routing_rules"`
	RoutingRoutes             int                        `json:"routing_routes"`
	ExitFirewallTable         string                     `json:"exit_firewall_table"`
	ExitFirewallRules         int                        `json:"exit_firewall_rules"`
	ProtectedMode             bool                       `json:"protected_mode"`
	ProtectionAdopted         bool                       `json:"protection_adopted"`
	MultiPeers                int                        `json:"multi_peers"`
	Error                     string                     `json:"error"`
}

func TestM5B_DualStackAuthenticatedSignaling(t *testing.T) {
	requirePrivileged(t)
	for _, family := range []string{"ipv4", "ipv6"} {
		t.Run(family, func(t *testing.T) {
			lab := newLab(t, "m5b-"+family)
			topology := lab.buildDirectDualStack()
			responder, ready := startSignalResponder(t, lab, topology.ServerNamespace,
				topology.ServerIPv4A1, topology.ServerIPv6A1, nil)
			remote := selectSignalAddress(t, ready.ListenAddresses, family)
			initiator := runSignalInitiator(t, lab, topology.ClientNamespace,
				topology.ClientIPv4, topology.ClientIPv6, remote, nil)
			responderResult := waitSignalResult(t, responder)
			assertSignalPair(t, initiator, responderResult)
			assertSignalCandidates(t, initiator.LocalCandidates, topology.ClientIPv4, topology.ClientIPv6, false, netip.AddrPort{})
			assertSignalCandidates(t, responderResult.LocalCandidates, topology.ServerIPv4A1, topology.ServerIPv6A1, false, netip.AddrPort{})
			lab.captureNamespace(topology.ClientNamespace, "client", false)
			lab.captureNamespace(topology.ServerNamespace, "server", false)
		})
	}
}

func TestM5B_NATDerivedServerReflexivePropagation(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "m5b-nat-srflx")
	topology := lab.buildNAT44()
	stunServer := lab.startResponder("m5b-stun", topology.ServerNamespace, topology.ServerA1, topology.ServerA2)
	probe, err := lab.runProbe("m5b-basic", topology.ClientNamespace, probeOptions{
		Mode: "basic", BindIPv4: topology.ClientIPv4, Port: clientMuxPort,
		ServerIPv4: endpointValue(topology.ServerA1, stunPrimaryPort),
		InitialRTO: 40 * time.Millisecond, MaxRTO: 80 * time.Millisecond, Attempts: 2,
	}, func(ready probeReady) {
		lab.installNFT(topology.RouterNamespace, "m5b-nat", controlledNATRules(topology, mappingEIM, filteringEIF, ready.Port))
		lab.inNamespace(topology.RouterNamespace, "nft", "add", "rule", "ip", "hydra_m4b", "postrouting",
			"ip", "saddr", topology.ClientIPv4.String(), "ip", "daddr", topology.ServerA1.String(),
			"meta", "l4proto", "tcp", "masquerade", "comment", "m5b-tcp-snat")
		lab.inNamespace(topology.RouterNamespace, "nft", "add", "rule", "ip", "hydra_m4b", "forward",
			"iifname", topology.InsideInterface, "ip", "saddr", topology.ClientIPv4.String(),
			"ip", "daddr", topology.ServerA1.String(), "meta", "l4proto", "tcp", "accept", "comment", "m5b-tcp-forward")
	})
	if err != nil {
		t.Fatal(err)
	}
	if probe.BasicIPv4 == nil {
		t.Fatal("missing real NAT Basic STUN observation")
	}
	observations := mustObservations(t, stunServer.observationPath)
	if len(observations) == 0 || observations[0].Source != probe.BasicIPv4.Mapped {
		t.Fatalf("STUN oracle source=%v mapped=%s", observations, probe.BasicIPv4.Mapped)
	}

	responder, ready := startSignalResponder(t, lab, topology.ServerNamespace,
		topology.ServerA1, netip.Addr{}, nil)
	remote := selectSignalAddress(t, ready.ListenAddresses, "ipv4")
	initiator := runSignalInitiator(t, lab, topology.ClientNamespace,
		topology.ClientIPv4, netip.Addr{}, remote,
		[]string{"--stun-server4", endpointValue(topology.ServerA1, stunPrimaryPort).String()})
	responderResult := waitSignalResult(t, responder)
	assertSignalPair(t, initiator, responderResult)
	if initiator.ObservedSrflxIPv4 != probe.BasicIPv4.Mapped.String() {
		t.Fatalf("same-mux STUN observation=%s independent oracle=%s", initiator.ObservedSrflxIPv4, probe.BasicIPv4.Mapped)
	}
	observations = mustObservations(t, stunServer.observationPath)
	if len(observations) < 2 || observations[len(observations)-1].Source != probe.BasicIPv4.Mapped {
		t.Fatalf("same-mux STUN server source=%v independent oracle=%s", observations, probe.BasicIPv4.Mapped)
	}
	assertSignalCandidates(t, initiator.LocalCandidates, topology.ClientIPv4, netip.Addr{}, true, probe.BasicIPv4.Mapped)
	assertSignalCandidates(t, responderResult.RemoteCandidates, topology.ClientIPv4, netip.Addr{}, true, probe.BasicIPv4.Mapped)
	lab.captureNamespace(topology.ClientNamespace, "client", false)
	lab.captureNamespace(topology.RouterNamespace, "router", true)
	lab.captureNamespace(topology.ServerNamespace, "server", false)
}

type signalChild struct {
	child      *childProcess
	resultPath string
}

func startSignalResponder(t *testing.T, lab *lab, namespace string, ipv4, ipv6 netip.Addr, extra []string) (signalChild, iceSignalReport) {
	t.Helper()
	readyPath := filepath.Join(lab.artifactDir, "m5b-responder-ready.json")
	resultPath := filepath.Join(lab.artifactDir, "m5b-responder-result.json")
	args := signalBaseArgs("responder", ipv4, ipv6, readyPath, resultPath)
	args = append(args, extra...)
	child := lab.startChild("m5b-responder", namespace, os.Getenv("HYDRA_M5B_SIGNALHELPER"), args...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, readyPath); err != nil {
		t.Fatal(err)
	}
	var ready iceSignalReport
	if err := readJSON(readyPath, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.Error != "" || ready.PeerID == "" || ready.MuxPort != clientMuxPort {
		t.Fatalf("invalid responder readiness: %+v", ready)
	}
	return signalChild{child: child, resultPath: resultPath}, ready
}

func runSignalInitiator(t *testing.T, lab *lab, namespace string, ipv4, ipv6 netip.Addr, remote string, extra []string) iceSignalReport {
	t.Helper()
	readyPath := filepath.Join(lab.artifactDir, "m5b-initiator-ready.json")
	resultPath := filepath.Join(lab.artifactDir, "m5b-initiator-result.json")
	args := signalBaseArgs("initiator", ipv4, ipv6, readyPath, resultPath)
	args = append(args, "--remote", remote)
	args = append(args, extra...)
	child := lab.startChild("m5b-initiator", namespace, os.Getenv("HYDRA_M5B_SIGNALHELPER"), args...)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := child.wait(ctx); err != nil {
		t.Fatal(err)
	}
	var result iceSignalReport
	if err := readJSON(resultPath, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("initiator error: %s", result.Error)
	}
	return result
}

func waitSignalResult(t *testing.T, child signalChild) iceSignalReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := child.child.wait(ctx); err != nil {
		t.Fatal(err)
	}
	var result iceSignalReport
	if err := readJSON(child.resultPath, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("responder error: %s", result.Error)
	}
	return result
}

func signalBaseArgs(mode string, ipv4, ipv6 netip.Addr, ready, result string) []string {
	args := []string{
		"--mode", mode, "--port", strconv.Itoa(clientMuxPort),
		"--ready-file", ready, "--result-file", result, "--timeout", "10s",
	}
	if ipv4.IsValid() {
		args = append(args, "--bind4", ipv4.String(), "--tcp4", ipv4.String())
	}
	if ipv6.IsValid() {
		args = append(args, "--bind6", ipv6.String(), "--tcp6", ipv6.String())
	}
	return args
}

func selectSignalAddress(t *testing.T, addresses []string, family string) string {
	t.Helper()
	prefix := "/ip4/"
	if family == "ipv6" {
		prefix = "/ip6/"
	}
	for _, address := range addresses {
		if strings.HasPrefix(address, prefix) {
			return address
		}
	}
	t.Fatalf("no %s signaling address in %v", family, addresses)
	return ""
}

func assertSignalPair(t *testing.T, initiator, responder iceSignalReport) {
	t.Helper()
	if initiator.SessionID == "" || initiator.SessionID != responder.SessionID ||
		initiator.RemotePeerID != responder.PeerID || responder.RemotePeerID != initiator.PeerID {
		t.Fatalf("session identity mismatch: initiator=%+v responder=%+v", initiator, responder)
	}
	if initiator.LocalUfrag == "" || responder.LocalUfrag == "" ||
		initiator.LocalUfrag != responder.RemoteUfrag || responder.LocalUfrag != initiator.RemoteUfrag {
		t.Fatalf("ufrag exchange mismatch: initiator=%+v responder=%+v", initiator, responder)
	}
	if initiator.ConnectivityRequests != 0 || responder.ConnectivityRequests != 0 {
		t.Fatalf("signaling started ICE connectivity checks: initiator=%d responder=%d",
			initiator.ConnectivityRequests, responder.ConnectivityRequests)
	}
}

func assertSignalCandidates(t *testing.T, candidates []iceSignalCandidateReport, ipv4, ipv6 netip.Addr, wantSrflx bool, mapped netip.AddrPort) {
	t.Helper()
	hostAddresses := make(map[string]bool)
	var srflx *iceSignalCandidateReport
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.Type == "1" {
			hostAddresses[candidate.Address] = true
		}
		if candidate.Type == "2" {
			srflx = candidate
		}
		if candidate.Component != 1 || candidate.Foundation == "" || candidate.Priority == 0 {
			t.Fatalf("invalid candidate metadata: %+v", candidate)
		}
	}
	for _, address := range []netip.Addr{ipv4, ipv6} {
		if address.IsValid() && !hostAddresses[address.String()] {
			t.Fatalf("missing host candidate %s: %+v", address, candidates)
		}
	}
	if !wantSrflx {
		if srflx != nil {
			t.Fatalf("unexpected srflx candidate: %+v", srflx)
		}
		return
	}
	if srflx == nil || srflx.Address != mapped.Addr().String() || srflx.Port != mapped.Port() ||
		srflx.RelatedAddress != ipv4.String() || srflx.RelatedPort != clientMuxPort {
		t.Fatalf("srflx candidate does not match STUN oracle %s: %+v", mapped, srflx)
	}
}

var _ = fmt.Sprintf
