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
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func runPrivateCombinedGUIShutdown(t *testing.T, ctx context.Context, lab *lab, host, client, upstream *localapi.Client,
	daemon *childProcess, namespaces []string, base, upstreamID string, ips []netip.Addr, target netip.AddrPort, baseline map[int]map[string]string) {
	t.Helper()
	check := func() {
		t.Helper()
		status, err := host.GetStatus(ctx)
		if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_CONNECTED || !status.PrivateHostingWanted || status.PrivateTargetPeerId != upstreamID {
			t.Fatalf("combined GUI lacks active upstream and hosting: %v %v", status, err)
		}
		stats, err := host.GetTrafficStats(ctx)
		if err != nil || stats.GetClients() != 1 {
			t.Fatalf("combined GUI lacks active downstream: %v %v", stats, err)
		}
	}
	runPrivateGUIRestarts(t, ctx, lab, namespaces[5], base, "private-host", check, func(gui *childProcess, environment []string, window string) {
		data, err := json.Marshal(map[string]any{"runner_pid": os.Getpid(), "namespace": namespaces[5], "environment": environment, "window": window})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(lab.artifactDir, "shutdown-console.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("combined native GUI ready for Cancel then confirmed Shutdown: %s", path)
		wait, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		if err := gui.wait(wait); err != nil {
			t.Fatalf("native GUI did not shut down successfully: %v", err)
		}
	})
	stopped, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := daemon.wait(stopped); err != nil {
		t.Fatalf("GUI Shutdown did not cleanly stop daemon: %v", err)
	}
	store := productintent.Store{Path: filepath.Join(base, "private-host-runtime", "irvio", "session-state")}
	if _, present, err := store.Read(); err != nil || present {
		t.Fatalf("GUI Shutdown retained combined intent: %t %v", present, err)
	}
	if actual := privateIdleNetwork(t, lab, namespaces[5]); !reflect.DeepEqual(baseline[5], actual) {
		t.Fatalf("GUI Shutdown left host network residue: before=%v after=%v", baseline[5], actual)
	}
	waitPrivateDownstreamReleased(t, ctx, upstream)
	probe := runM7Probe(t, lab, namespaces[5], "http", target)
	source, err := netip.ParseAddrPort(probe.ObservedSource)
	if err != nil || !probe.Success || source.Addr() != ips[5] {
		t.Fatalf("GUI Shutdown did not restore owner's WAN: %+v %v", probe, err)
	}
	for sample := 0; sample < 5; sample++ {
		blocked, err := tryM7Probe(t, lab, namespaces[6], "http", target, "--probe-timeout", "250ms")
		if err == nil && blocked.Success {
			t.Fatal("downstream traffic survived stopped private host")
		}
	}
	rules := lab.inNamespace(namespaces[6], "nft", "-j", "list", "table", "inet", "private_oracle")
	if packets, exists := counterForComment(rules, "private-user-wan-leak"); !exists || packets != 0 {
		t.Fatalf("GUI Shutdown caused downstream WAN leak: %d present=%t", packets, exists)
	}
	for _, api := range []*localapi.Client{client, upstream} {
		if err := api.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		waitPrivateRuntimeState(t, ctx, api, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
	}
	for _, index := range []int{6, 8} {
		if actual := privateIdleNetwork(t, lab, namespaces[index]); !reflect.DeepEqual(baseline[index], actual) {
			t.Fatalf("node %d retained network residue: before=%v after=%v", index, baseline[index], actual)
		}
	}
	t.Log("native GUI Shutdown stopped combined host, released upstream lease, restored WAN and kept downstream fail-closed")
}
