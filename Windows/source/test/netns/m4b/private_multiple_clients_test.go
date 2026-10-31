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

func runPrivateMultipleClients(t *testing.T, ctx context.Context, lab *lab, host, first, second *localapi.Client,
	namespaces []string, ips []netip.Addr, base, hostID, firstID string, baseline map[int]map[string]string,
	selective bool, enroll func(*localapi.Client, *localapi.Client)) {
	t.Helper()
	secondState, err := second.GetPrivateState(ctx, 0, 64)
	if err != nil || secondState.GetLocalPeerId() == "" || secondState.GetLocalPeerId() == firstID {
		t.Fatalf("second client is not an independent identity: %v", err)
	}
	lab.inNamespace(namespaces[8], "nft", "add", "table", "inet", "private_oracle")
	lab.inNamespace(namespaces[8], "nft", "add", "chain", "inet", "private_oracle", "egress", "{ type filter hook postrouting priority 300; policy accept; }")
	lab.inNamespace(namespaces[8], "nft", "add", "rule", "inet", "private_oracle", "egress",
		"ip", "saddr", ips[8].String(), "ip", "daddr", ips[7].String(), "counter", "comment", "private-user-wan-leak")
	waitPrivateAddressReady(t, ctx, lab, namespaces[8])
	baseline[8] = privateIdleNetwork(t, lab, namespaces[8])
	enroll(host, second)
	if _, err := second.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, PeerId: hostID}); err != nil {
		t.Fatal(err)
	}
	waitPrivateRuntimeState(t, ctx, second, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
	target := netip.AddrPortFrom(ips[7], 18080)
	traffic := func(api *localapi.Client, index int, allowed bool) {
		t.Helper()
		status, err := api.GetStatus(ctx)
		if err != nil || status.GetPrivateTargetPeerId() != hostID || status.GetCountryCode() != "" {
			t.Fatalf("client %d changed pinned intent: %v", index, err)
		}
		probe, probeErr := tryM7Probe(t, lab, namespaces[index], "http", target)
		if allowed {
			source, parseErr := netip.ParseAddrPort(probe.ObservedSource)
			if status.GetState() != localv1.ConnectionState_CONNECTION_STATE_CONNECTED || probeErr != nil || !probe.Success || parseErr != nil || source.Addr() != ips[5] {
				t.Fatalf("client %d lost authorized forwarding: %+v %v", index, probe, probeErr)
			}
		} else if probeErr == nil && probe.Success {
			t.Fatalf("client %d retained revoked forwarding", index)
		}
		rules := lab.inNamespace(namespaces[index], "nft", "-j", "list", "table", "inet", "private_oracle")
		if count, present := counterForComment(rules, "private-user-wan-leak"); !present || count != 0 {
			t.Fatalf("client %d WAN leak count=%d present=%t", index, count, present)
		}
	}
	count := func(want uint32) {
		t.Helper()
		started := time.Now()
		for {
			stats, err := host.GetTrafficStats(ctx)
			fresh := want == 0 || stats.GetSampledAtUnixMs() >= uint64(started.UnixMilli())
			if err == nil && fresh && stats.GetClients() == want {
				return
			}
			if time.Since(started) >= 5*time.Second || ctx.Err() != nil {
				t.Fatalf("host clients want=%d actual=%v err=%v", want, stats, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	traffic(first, 6, true)
	traffic(second, 8, true)
	count(2)
	if selective {
		started := time.Now()
		if _, err := host.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_REVOKE_MEMBER, PeerId: firstID}); err != nil {
			t.Fatal(err)
		}
		if time.Since(started) > 12*time.Second {
			t.Fatal("selective revocation exceeded bounded drain")
		}
		count(1)
		for repeat := 0; repeat < 3; repeat++ {
			traffic(first, 6, false)
			traffic(second, 8, true)
		}
	}
	if err := host.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	waitPrivateRuntimeState(t, ctx, host, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
	count(0)
	traffic(first, 6, false)
	traffic(second, 8, false)
	for _, item := range []struct {
		api   *localapi.Client
		index int
		label string
	}{{first, 6, "private-client"}, {second, 8, "private-second"}, {host, 5, "private-host"}} {
		if err := item.api.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		status := waitPrivateRuntimeState(t, ctx, item.api, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
		if status.GetPrivateTargetPeerId() != "" || status.GetPrivateHostingWanted() || status.GetContributionEnabled() {
			t.Fatal("full leave retained participation")
		}
		store := productintent.Store{Path: filepath.Join(base, item.label+"-runtime", "irvio", "session-state")}
		if _, present, err := store.Read(); err != nil || present {
			t.Fatalf("full leave retained intent: %v", err)
		}
		if !reflect.DeepEqual(baseline[item.index], privateIdleNetwork(t, lab, namespaces[item.index])) {
			t.Fatalf("node %d retained network residue", item.index)
		}
		probe := runM7Probe(t, lab, namespaces[item.index], "http", target)
		source, err := netip.ParseAddrPort(probe.ObservedSource)
		if err != nil || !probe.Success || source.Addr() != ips[item.index] {
			t.Fatalf("node %d ordinary WAN not restored", item.index)
		}
	}
	t.Logf("two concurrent clients forwarded; selective revocation=%t; host leave drained remaining clients", selective)
}
