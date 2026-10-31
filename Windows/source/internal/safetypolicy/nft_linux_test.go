// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package safetypolicy

import (
	"slices"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

func TestLinuxCompilerBuildsExactInetServiceSets(t *testing.T) {
	table := &nftables.Table{Name: "hydra_test", Family: nftables.TableFamilyINet}
	sets, err := AddLinuxPortSets(&nftables.Conn{}, table)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		set       *nftables.Set
		transport Transport
		name      string
	}{
		{sets.TCP, TransportTCP, LinuxTCPSetName},
		{sets.UDP, TransportUDP, LinuxUDPSetName},
	} {
		if test.set.Name != test.name || test.set.Table != table || !test.set.Constant ||
			test.set.KeyType.Name != nftables.TypeInetService.Name {
			t.Fatalf("set=%+v", test.set)
		}
		var got []uint16
		for _, element := range linuxPortElements(test.transport) {
			got = append(got, binaryutil.BigEndian.Uint16(element.Key))
		}
		if want := Ports(V0, test.transport); !slices.Equal(got, want) {
			t.Fatalf("set %s ports=%v, want %v", test.name, got, want)
		}
	}
}

func TestLinuxCompilerMatchesTransportBeforeDestinationSet(t *testing.T) {
	table := &nftables.Table{Name: "hydra_test", Family: nftables.TableFamilyINet}
	sets, err := AddLinuxPortSets(&nftables.Conn{}, table)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		transport Transport
		protocol  byte
		set       *nftables.Set
	}{
		{TransportTCP, unix.IPPROTO_TCP, sets.TCP},
		{TransportUDP, unix.IPPROTO_UDP, sets.UDP},
	} {
		expressions := LinuxDestinationPortMatch(test.transport, sets)
		if len(expressions) != 4 {
			t.Fatalf("expressions=%#v", expressions)
		}
		comparison, ok := expressions[1].(*expr.Cmp)
		if !ok || len(comparison.Data) != 1 || comparison.Data[0] != test.protocol {
			t.Fatalf("protocol expression=%#v", expressions[1])
		}
		payload, ok := expressions[2].(*expr.Payload)
		if !ok || payload.Base != expr.PayloadBaseTransportHeader || payload.Offset != 2 || payload.Len != 2 {
			t.Fatalf("destination-port expression=%#v", expressions[2])
		}
		lookup, ok := expressions[3].(*expr.Lookup)
		if !ok || lookup.SetName != test.set.Name || lookup.SetID != test.set.ID {
			t.Fatalf("lookup expression=%#v", expressions[3])
		}
	}
}
