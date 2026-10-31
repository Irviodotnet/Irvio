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
	"reflect"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func runPrivateActiveProfileDeletion(t *testing.T, ctx context.Context, lab *lab, host, client, upstream *localapi.Client,
	daemon *childProcess, namespaces []string, base string, ips []netip.Addr, target netip.AddrPort, baseline map[int]map[string]string) {
	t.Helper()
	before, err := host.GetPrivateState(ctx, 0, 64)
	if err != nil || len(before.GetTargets()) == 0 || len(before.GetMembers()) == 0 {
		t.Fatalf("missing active private trust before deletion: %v", err)
	}
	if err := host.DeleteProfile(ctx); err != nil {
		t.Fatal("active private profile deletion:", err)
	}
	_ = host.Close()
	deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var fresh *localapi.Client
	for deadline.Err() == nil {
		api, dialErr := localapi.Dial(deadline, filepath.Join(base, "private-host", "control.sock"))
		if dialErr == nil {
			state, stateErr := api.GetPrivateState(deadline, 0, 64)
			if stateErr == nil && state.LocalPeerId != "" && state.LocalPeerId != before.LocalPeerId {
				fresh = api
				if len(state.Targets)+len(state.Members)+len(state.Invitations)+len(state.Enrollments) != 0 {
					t.Fatal("private trust survived identity reset")
				}
				break
			}
			_ = api.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	if fresh == nil {
		t.Fatal("profile reset did not expose a new idle identity")
	}
	defer fresh.Close()
	status, err := fresh.GetStatus(ctx)
	if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED || status.PrivateHostingWanted || status.PrivateTargetPeerId != "" || status.ContributionEnabled {
		t.Fatalf("profile reset restored network participation: %v %v", status, err)
	}
	bootstrap, err := fresh.GetBootstrapState(ctx)
	if err != nil || bootstrap.ConfiguredCount != 0 {
		t.Fatalf("bootstrap survived profile reset: %v %v", bootstrap, err)
	}
	store := productintent.Store{Path: filepath.Join(base, "private-host-runtime", "irvio", "session-state")}
	if _, present, err := store.Read(); err != nil || present {
		t.Fatalf("reset retained recovery intent: %t %v", present, err)
	}
	waitPrivateDownstreamReleased(t, ctx, upstream)
	for i := 0; i < 5; i++ {
		blocked, err := tryM7Probe(t, lab, namespaces[6], "http", target, "--probe-timeout", "250ms")
		if err == nil && blocked.Success {
			t.Fatal("downstream traffic survived host profile deletion")
		}
	}
	rules := lab.inNamespace(namespaces[6], "nft", "-j", "list", "table", "inet", "private_oracle")
	if packets, exists := counterForComment(rules, "private-user-wan-leak"); !exists || packets != 0 {
		t.Fatal("profile deletion caused downstream WAN leak")
	}
	for _, api := range []*localapi.Client{client, upstream} {
		if err := api.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		waitPrivateRuntimeState(t, ctx, api, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
	}
	for _, index := range []int{5, 6, 8} {
		if !reflect.DeepEqual(baseline[index], privateIdleNetwork(t, lab, namespaces[index])) {
			t.Fatalf("node %d retained network residue after profile deletion", index)
		}
	}
	probe := runM7Probe(t, lab, namespaces[5], "http", target)
	source, err := netip.ParseAddrPort(probe.ObservedSource)
	if err != nil || !probe.Success || source.Addr() != ips[5] {
		t.Fatal("reset host did not restore ordinary WAN")
	}
	if err := fresh.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := daemon.wait(ctx); err != nil {
		t.Fatal(err)
	}
}
