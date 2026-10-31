// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/exitfirewall"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

type recordingExitFirewall struct {
	wanName string
	ipv4    netip.Addr
	wans    int
	nats    int
}

func (*recordingExitFirewall) Snapshot(context.Context) (exitfirewall.Snapshot, error) {
	return exitfirewall.Snapshot{}, nil
}
func (*recordingExitFirewall) SetLocalTunnelAddresses(context.Context, []netip.Addr) error {
	return nil
}
func (f *recordingExitFirewall) SetNATAddresses(_ context.Context, ipv4, _ netip.Addr) error {
	f.ipv4 = ipv4
	f.nats++
	return nil
}
func (f *recordingExitFirewall) SetWANPath(_ context.Context, name string, ipv4, _ netip.Addr) error {
	f.wanName, f.ipv4 = name, ipv4
	f.wans++
	return nil
}
func (*recordingExitFirewall) Close() error { return nil }

func TestExitReconcileMovesWANInterfaceWithSource(t *testing.T) {
	firewall := &recordingExitFirewall{wanName: "wan0", ipv4: netip.MustParseAddr("192.0.2.2")}
	plane := &exitDataPlane{config: ExitServerConfig{WANName: "wan0", IPv4Pool: netip.MustParsePrefix("10.77.0.0/24")},
		firewall: firewall, natIPv4: firewall.ipv4}
	state := networkstate.State{Generation: 2, Snapshot: networkstate.Snapshot{
		Links: []networkstate.Link{{Index: 3, Name: "wan1", Up: true,
			Addresses: []netip.Prefix{netip.MustParsePrefix("198.51.100.2/24")}}},
		Routes: []networkstate.Route{{Family: networkstate.FamilyIPv4, Table: 254, LinkIndex: 3,
			Gateway: netip.MustParseAddr("198.51.100.1"), PreferredSource: netip.MustParseAddr("198.51.100.2")}},
	}}
	if err := plane.ReconcileNetwork(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if firewall.wanName != "wan1" || firewall.ipv4.String() != "198.51.100.2" || firewall.wans != 1 || firewall.nats != 0 {
		t.Fatalf("WAN migration not atomic: %+v", firewall)
	}
	if plane.config.WANName != "wan1" || plane.natIPv4 != firewall.ipv4 {
		t.Fatalf("Exit owner state is stale: name=%q address=%s", plane.config.WANName, plane.natIPv4)
	}
	if err := plane.ReconcileNetwork(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if firewall.wans != 1 || firewall.nats != 1 {
		t.Fatalf("same-interface refresh must update NAT only: %+v", firewall)
	}
}
