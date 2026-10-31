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

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

type socketmuxExerciseResult struct {
	Port               uint16          `json:"port"`
	LocalIPv4          string          `json:"local_ipv4"`
	LocalIPv6          string          `json:"local_ipv6"`
	ControlSourceIPv4  string          `json:"control_source_ipv4"`
	DataSourceIPv4     string          `json:"data_source_ipv4"`
	ControlSourceIPv6  string          `json:"control_source_ipv6"`
	DataSourceIPv6     string          `json:"data_source_ipv6"`
	Stats              socketmux.Stats `json:"stats"`
	BlockedReadersExit bool            `json:"blocked_readers_exit"`
	Error              string          `json:"error"`
}

func TestM4B_SocketMuxOwnershipBackpressureAndLifecycle(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "socketmux")
	topology := lab.buildDirectDualStack()
	readyPath := filepath.Join(lab.artifactDir, "socketmux-ready.json")
	startPath := filepath.Join(lab.artifactDir, "socketmux-start")
	resultPath := filepath.Join(lab.artifactDir, "socketmux-result.json")
	child := lab.startChild("socketmux", topology.ClientNamespace, os.Getenv("HYDRA_M4B_SOCKETMUX"),
		"--bind4", topology.ClientIPv4.String(), "--bind6", topology.ClientIPv6.String(),
		"--port", strconv.Itoa(clientMuxPort), "--ready-file", readyPath,
		"--start-file", startPath, "--result-file", resultPath, "--timeout", "5s")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, readyPath); err != nil {
		t.Fatal(err)
	}
	output := lab.inNamespace(topology.ClientNamespace, "ss", "-H", "-l", "-u", "-n", "-p", fmt.Sprintf("sport = :%d", clientMuxPort))
	if len(nonemptyLines(output)) != 2 {
		t.Fatalf("mux owns %d visible sockets, want exactly two: %q", len(nonemptyLines(output)), output)
	}
	if err := touch(startPath); err != nil {
		t.Fatal(err)
	}
	waitContext, waitCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer waitCancel()
	if err := child.wait(waitContext); err != nil {
		t.Fatal(err)
	}
	var result socketmuxExerciseResult
	if err := readJSON(resultPath, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Port != clientMuxPort || !result.BlockedReadersExit {
		t.Fatalf("socketmux exercise failed: %+v", result)
	}
	if result.Stats.ControlOverflowDrops < 2 || result.Stats.DataOverflowDrops < 2 {
		t.Fatalf("backpressure counters do not cover both families: %+v", result.Stats)
	}
	for name, source := range map[string]string{
		"control IPv4": result.ControlSourceIPv4, "data IPv4": result.DataSourceIPv4,
		"control IPv6": result.ControlSourceIPv6, "data IPv6": result.DataSourceIPv6,
	} {
		endpoint, err := netip.ParseAddrPort(source)
		if err != nil || endpoint.Port() != clientMuxPort {
			t.Fatalf("%s source %q does not use mux port %d", name, source, clientMuxPort)
		}
	}
	if output := lab.inNamespace(topology.ClientNamespace, "ss", "-H", "-l", "-u", "-n", fmt.Sprintf("sport = :%d", clientMuxPort)); strings.TrimSpace(output) != "" {
		t.Fatalf("socketmux leaked sockets after Close: %q", output)
	}
	pattern := "^(TestSocketMuxIPv6ReceiveAndSend|" +
		"TestSocketMuxDualStackPortZeroUsesCommonPort|" +
		"TestSocketMuxControlBackpressureDoesNotBlockData|" +
		"TestSocketMuxDataBackpressureDoesNotBlockControl|" +
		"TestSocketMuxCloseUnblocksReadersAndIsIdempotent)$"
	unitOutput := lab.inNamespace(topology.ClientNamespace, os.Getenv("HYDRA_M4B_SOCKETTEST"),
		"-test.v", "-test.run", pattern, "-test.count=1")
	if err := os.WriteFile(filepath.Join(lab.artifactDir, "socketmux-namespace-tests.txt"), []byte(unitOutput), 0o600); err != nil {
		t.Fatal(err)
	}
}
