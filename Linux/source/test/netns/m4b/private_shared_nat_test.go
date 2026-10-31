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
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

// Model one public NAT with routed internal segments and allowed inter-LAN traffic.
func installPrivateSharedNAT(t *testing.T, lab *lab, ips []netip.Addr) netip.Addr {
	t.Helper()
	outside := netip.MustParseAddr("198.18.91.2")
	router := lab.name + "-router"
	for _, index := range []int{5, 6} {
		insideIf := lab.interfaceName(byte('1' + index))
		gateway := fmt.Sprintf("198.18.%d.254/24", 51+index)
		lab.inNamespace(router, "ip", "link", "set", insideIf, "nomaster")
		lab.inNamespace(router, "ip", "addr", "del", gateway, "dev", "br0")
		lab.inNamespace(router, "ip", "addr", "add", gateway, "dev", insideIf)
	}
	lab.inNamespace(router, "ip", "addr", "add", outside.String()+"/32", "dev", "lo")
	lab.installNFT(router, "private-shared-nat", fmt.Sprintf(`table ip private_shared_nat {
 chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
  ip saddr { %[1]s, %[2]s } ip daddr { %[1]s, %[2]s } return
  ip saddr { %[1]s, %[2]s } counter snat to %[3]s
 }
 chain forward {
  type filter hook forward priority filter; policy accept;
  ip saddr { %[1]s, %[2]s } ip daddr { %[1]s, %[2]s } counter accept
  ip daddr { %[1]s, %[2]s } ct state established,related counter accept
  ip daddr { %[1]s, %[2]s } counter drop comment "private-shared-no-public-bypass"
 }
}`, ips[5], ips[6], outside))
	return outside
}

func runPrivateUnreachableNAT(t *testing.T, ctx context.Context, lab *lab, host, client *localapi.Client,
	namespaces []string, ips []netip.Addr, base, hostID string, outside netip.Addr, baseline map[int]map[string]string, started time.Time) {
	t.Helper()
	waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_NO_EXIT)
	t.Logf("unreachable private NAT reported NO_EXIT after %s", time.Since(started))
	target := netip.AddrPortFrom(ips[7], 18080)
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		status, err := client.GetStatus(ctx)
		if err != nil || status.State == localv1.ConnectionState_CONNECTION_STATE_CONNECTED || status.PrivateTargetPeerId != hostID || status.CountryCode != "" {
			t.Fatalf("incompatible NAT changed target or connected: status=%v err=%v", status, err)
		}
		probe, err := tryM7Probe(t, lab, namespaces[6], "http", target)
		if err == nil && probe.Success {
			t.Fatal("incompatible NAT allowed ordinary user traffic")
		}
	}
	lab.inNamespace(namespaces[6], "nft", "list", "table", "inet", "hydra_protect")
	rules := lab.inNamespace(namespaces[6], "nft", "-j", "list", "table", "inet", "private_oracle")
	if packets, found := counterForComment(rules, "private-user-wan-leak"); !found || packets != 0 {
		t.Fatalf("incompatible NAT WAN leak packets=%d counter_present=%t", packets, found)
	}
	stats, err := host.GetTrafficStats(ctx)
	if err != nil || stats.GetClients() != 0 {
		t.Fatalf("unreachable peer retained active downstream leases: %v %v", stats, err)
	}
	for _, item := range []struct {
		api   *localapi.Client
		index int
		label string
	}{{client, 6, "private-client"}, {host, 5, "private-host"}} {
		if err := item.api.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		status := waitPrivateRuntimeState(t, ctx, item.api, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
		if status.PrivateHostingWanted || status.PrivateTargetPeerId != "" || status.ContributionEnabled {
			t.Fatal("full leave retained network intent")
		}
		store := productintent.Store{Path: filepath.Join(base, item.label+"-runtime", "irvio", "session-state")}
		if _, present, err := store.Read(); err != nil || present {
			t.Fatalf("full leave retained runtime intent: %v", err)
		}
		if !reflect.DeepEqual(baseline[item.index], privateIdleNetwork(t, lab, namespaces[item.index])) {
			t.Fatal("incompatible NAT teardown retained network residue")
		}
		probe := runM7Probe(t, lab, namespaces[item.index], "http", target)
		source, err := netip.ParseAddrPort(probe.ObservedSource)
		if err != nil || !probe.Success || source.Addr() != outside {
			t.Fatalf("full leave failed to restore ordinary WAN: %+v err=%v", probe, err)
		}
	}
}
