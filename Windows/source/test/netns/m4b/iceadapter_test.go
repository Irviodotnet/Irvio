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

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
)

type iceAdapterCandidate struct {
	Type    string `json:"type"`
	Network string `json:"network"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type iceAdapterReport struct {
	Type         string                `json:"type"`
	PID          int                   `json:"pid"`
	Port         uint16                `json:"port"`
	LocalIPv4    string                `json:"local_ipv4"`
	LocalIPv6    string                `json:"local_ipv6"`
	LocalUfrag   string                `json:"local_ufrag"`
	Candidates   []iceAdapterCandidate `json:"candidates"`
	OutboundIPv4 bool                  `json:"outbound_ipv4"`
	OutboundIPv6 bool                  `json:"outbound_ipv6"`
	DataAfterICE bool                  `json:"data_after_ice"`
	AdapterStats iceadapter.Stats      `json:"adapter_stats"`
	Error        string                `json:"error"`
}

func TestM5A_PionAgentUsesOnlyIrvioPhysicalSockets(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "m5a-ice-adapter")
	topology := lab.buildDirectDualStack()
	server4 := lab.startResponder("m5a-server-v4", topology.ServerNamespace, topology.ServerIPv4A1, topology.ServerIPv4A2)
	server6 := lab.startResponder("m5a-server-v6", topology.ServerNamespace, topology.ServerIPv6A1, topology.ServerIPv6A2)
	readyPath := filepath.Join(lab.artifactDir, "m5a-ready.json")
	startPath := filepath.Join(lab.artifactDir, "m5a-start")
	gatheredPath := filepath.Join(lab.artifactDir, "m5a-gathered.json")
	finishPath := filepath.Join(lab.artifactDir, "m5a-finish")
	resultPath := filepath.Join(lab.artifactDir, "m5a-result.json")
	child := lab.startChild("m5a-ice-adapter", topology.ClientNamespace, os.Getenv("HYDRA_M5A_ICEHELPER"),
		"--bind4", topology.ClientIPv4.String(), "--bind6", topology.ClientIPv6.String(),
		"--port", strconv.Itoa(clientMuxPort),
		"--receiver4", endpointValue(topology.ServerIPv4A1, stunPrimaryPort).String(),
		"--receiver6", endpointValue(topology.ServerIPv6A1, stunPrimaryPort).String(),
		"--ready-file", readyPath, "--start-file", startPath,
		"--gathered-file", gatheredPath, "--finish-file", finishPath,
		"--result-file", resultPath, "--timeout", "10s")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, readyPath); err != nil {
		t.Fatal(err)
	}
	var ready iceAdapterReport
	if err := readJSON(readyPath, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.Port != clientMuxPort || ready.PID <= 0 {
		t.Fatalf("invalid ICE helper readiness: %+v", ready)
	}
	before := lab.inNamespace(topology.ClientNamespace, "ss", "-H", "-a", "-u", "-n", "-p")
	assertIrvioSocketPair(t, before, ready.PID, topology.ClientIPv4, topology.ClientIPv6)
	if err := touch(startPath); err != nil {
		t.Fatal(err)
	}
	if err := waitForChildFile(ctx, child, gatheredPath); err != nil {
		t.Fatal(err)
	}
	var gathered iceAdapterReport
	if err := readJSON(gatheredPath, &gathered); err != nil {
		t.Fatal(err)
	}
	after := lab.inNamespace(topology.ClientNamespace, "ss", "-H", "-a", "-u", "-n", "-p")
	assertIrvioSocketPair(t, after, gathered.PID, topology.ClientIPv4, topology.ClientIPv6)
	if before != after {
		t.Fatalf("UDP socket ownership changed after Pion gathering:\nbefore: %s\nafter: %s", before, after)
	}
	assertICECandidates(t, gathered, topology.ClientIPv4, topology.ClientIPv6)
	if !gathered.OutboundIPv4 || !gathered.OutboundIPv6 || gathered.LocalUfrag == "" {
		t.Fatalf("missing outbound or credential evidence: %+v", gathered)
	}
	waitForObservations(t, server4.observationPath, 1)
	waitForObservations(t, server6.observationPath, 1)
	for _, observation := range append(mustObservations(t, server4.observationPath), mustObservations(t, server6.observationPath)...) {
		if observation.Source.Port() != clientMuxPort {
			t.Fatalf("Pion-generated Binding source = %s, want mux port %d", observation.Source, clientMuxPort)
		}
	}
	if err := touch(finishPath); err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer waitCancel()
	if err := child.wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	var result iceAdapterReport
	if err := readJSON(resultPath, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || !result.DataAfterICE || result.AdapterStats.ActiveUfrags != 0 || result.AdapterStats.ActiveTransactions != 0 {
		t.Fatalf("ICE helper shutdown result: %+v", result)
	}
	if output := lab.inNamespace(topology.ClientNamespace, "ss", "-H", "-a", "-u", "-n"); strings.TrimSpace(output) != "" {
		t.Fatalf("Irvio sockets leaked after helper exit: %q", output)
	}
	lab.captureNamespace(topology.ClientNamespace, "client", false)
	lab.captureNamespace(topology.ServerNamespace, "server", false)
}

func assertIrvioSocketPair(t *testing.T, output string, pid int, ipv4, ipv6 netip.Addr) {
	t.Helper()
	lines := nonemptyLines(output)
	if len(lines) != 2 {
		t.Fatalf("visible UDP socket count = %d, want exactly two: %q", len(lines), output)
	}
	for _, expected := range []string{ipv4.String(), ipv6.String(), fmt.Sprintf("pid=%d", pid)} {
		if !strings.Contains(output, expected) {
			t.Fatalf("socket evidence lacks %q: %s", expected, output)
		}
	}
}

func assertICECandidates(t *testing.T, report iceAdapterReport, ipv4, ipv6 netip.Addr) {
	t.Helper()
	if len(report.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want two: %+v", len(report.Candidates), report.Candidates)
	}
	want := map[string]string{ipv4.String(): "udp4", ipv6.String(): "udp6"}
	for _, candidate := range report.Candidates {
		if candidate.Type != "host" || candidate.Port != int(report.Port) || want[candidate.Address] != candidate.Network {
			t.Fatalf("unexpected host candidate: %+v", candidate)
		}
		delete(want, candidate.Address)
	}
	if len(want) != 0 {
		t.Fatalf("missing host candidates: %v", want)
	}
}

func waitForObservations(t *testing.T, path string, minimum int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		observations, err := readObservations(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(observations) >= minimum {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("observation count for %s remained %d", path, len(observations))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
