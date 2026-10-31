// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func runPrivateSystemdCrash(t *testing.T, ctx context.Context, lab *lab, client *localapi.Client,
	namespace, base, unit, socket, hostID string, hostWAN netip.Addr, target netip.AddrPort) *localapi.Client {
	t.Helper()
	property := func(name string) string {
		return strings.TrimSpace(lab.run("systemctl", "show", unit, "--property="+name, "--value"))
	}
	first := property("MainPID")
	if pid, err := strconv.Atoi(first); err != nil || pid <= 1 || property("NRestarts") != "0" {
		t.Fatal("transient daemon lacks a clean initial systemd process")
	}
	store := productintent.Store{Path: filepath.Join(base, "private-client-runtime", "irvio", "session-state")}
	before, present, err := store.Read()
	if err != nil || !present || before.PrivateTarget != hostID || !before.ConnectIntent {
		t.Fatalf("missing pinned systemd recovery intent: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	lab.run("systemctl", "kill", "--kill-whom=main", "--signal=SIGKILL", unit)
	lab.inNamespace(namespace, "nft", "list", "table", "inet", "hydra_protect")
	blocked, probeErr := tryM7Probe(t, lab, namespace, "http", target, "--probe-timeout", "250ms")
	if probeErr == nil && blocked.Success {
		t.Fatal("private user traffic passed during systemd restart delay")
	}
	after, present, err := store.Read()
	if err != nil || !present || before != after {
		t.Fatal("SIGKILL changed same-boot private intent")
	}
	deadline := time.Now().Add(30 * time.Second)
	var replacement *localapi.Client
	for time.Now().Before(deadline) {
		pid := property("MainPID")
		if pid != "" && pid != "0" && pid != first && property("NRestarts") == "1" {
			replacement, err = localapi.Dial(ctx, socket)
			if err == nil {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if replacement == nil {
		t.Fatalf("systemd did not automatically restore Local API: %v", err)
	}
	t.Cleanup(func() { replacement.Close() })
	status := waitPrivateRuntimeState(t, ctx, replacement, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
	if status.PrivateTargetPeerId != hostID || status.CountryCode != "" || property("NRestarts") != "1" {
		t.Fatal("automatic systemd recovery changed target or restarted repeatedly")
	}
	probe := runM7Probe(t, lab, namespace, "http", target)
	source, err := netip.ParseAddrPort(probe.ObservedSource)
	if err != nil || !probe.Success || source.Addr() != hostWAN {
		t.Fatalf("systemd recovery did not restore pinned egress: %+v err=%v", probe, err)
	}
	rules := lab.inNamespace(namespace, "nft", "-j", "list", "table", "inet", "private_oracle")
	if packets, found := counterForComment(rules, "private-user-wan-leak"); !found || packets != 0 {
		t.Fatalf("systemd recovery WAN leak: %d present=%t", packets, found)
	}
	t.Logf("systemd Restart=on-failure restored pinned user traffic after %s; NRestarts=1", time.Since(started))
	return replacement
}

func runPrivateSystemdShutdown(t *testing.T, ctx context.Context, lab *lab, client *localapi.Client, process *childProcess, unit string) {
	t.Helper()
	if err := client.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	stop, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := process.wait(stop); err != nil {
		t.Fatalf("intentional Shutdown did not exit successfully: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		units := lab.run("systemctl", "list-units", "--all", "--plain", "--no-legend", unit)
		if strings.TrimSpace(units) != "" {
			t.Fatalf("Shutdown retained or restarted transient unit: %s", units)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Log("intentional Shutdown exited successfully without systemd restart")
}
