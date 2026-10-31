// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package safetypolicy

import (
	"fmt"
	"slices"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

const (
	LinuxTCPSetName = "safe_tcp_v0"
	LinuxUDPSetName = "safe_udp_v0"
)

// LinuxPortSets are the two immutable inet_service sets compiled from V0.
type LinuxPortSets struct {
	TCP *nftables.Set
	UDP *nftables.Set
}

// AddLinuxPortSets stages canonical sets; callers add referencing rules before the same batch commits.
func AddLinuxPortSets(conn *nftables.Conn, table *nftables.Table) (LinuxPortSets, error) {
	if conn == nil || table == nil || table.Family != nftables.TableFamilyINet {
		return LinuxPortSets{}, fmt.Errorf("compile safety policy v0: invalid inet table")
	}
	sets := LinuxPortSets{
		TCP: linuxPortSet(table, LinuxTCPSetName),
		UDP: linuxPortSet(table, LinuxUDPSetName),
	}
	if err := conn.AddSet(sets.TCP, linuxPortElements(TransportTCP)); err != nil {
		return LinuxPortSets{}, fmt.Errorf("compile safety policy v0 TCP set: %w", err)
	}
	if err := conn.AddSet(sets.UDP, linuxPortElements(TransportUDP)); err != nil {
		return LinuxPortSets{}, fmt.Errorf("compile safety policy v0 UDP set: %w", err)
	}
	return sets, nil
}

// DeleteLinuxPortSets removes all sets only from an exclusively IRVIO-owned table.
func DeleteLinuxPortSets(conn *nftables.Conn, table *nftables.Table) error {
	if conn == nil || table == nil {
		return fmt.Errorf("reconcile safety policy v0: invalid table")
	}
	sets, err := (&nftables.Conn{}).GetSets(table)
	if err != nil {
		return fmt.Errorf("list safety policy v0 sets: %w", err)
	}
	for _, set := range sets {
		conn.DelSet(set)
	}
	return nil
}

// LinuxPortSetsComplete checks exact sets before protected crash adoption.
func LinuxPortSetsComplete(table *nftables.Table) (bool, error) {
	if table == nil {
		return false, nil
	}
	conn := &nftables.Conn{}
	sets, err := conn.GetSets(table)
	if err != nil {
		return false, fmt.Errorf("list safety policy v0 sets: %w", err)
	}
	if len(sets) != 2 {
		return false, nil
	}
	want := map[string][]uint16{
		LinuxTCPSetName: Ports(V0, TransportTCP),
		LinuxUDPSetName: Ports(V0, TransportUDP),
	}
	for _, set := range sets {
		ports, exists := want[set.Name]
		if !exists || set.KeyType.Name != nftables.TypeInetService.Name || set.KeyType.Bytes != nftables.TypeInetService.Bytes {
			return false, nil
		}
		elements, err := conn.GetSetElements(set)
		if err != nil {
			return false, fmt.Errorf("read safety policy v0 set %s: %w", set.Name, err)
		}
		got := make([]uint16, 0, len(elements))
		for _, element := range elements {
			if len(element.Key) != 2 {
				return false, nil
			}
			got = append(got, binaryutil.BigEndian.Uint16(element.Key))
		}
		slices.Sort(got)
		if !slices.Equal(got, ports) {
			return false, nil
		}
	}
	return true, nil
}

// LinuxDestinationPortMatch handles both IP families after defragmentation.
func LinuxDestinationPortMatch(transport Transport, sets LinuxPortSets) []expr.Any {
	var protocol byte
	var set *nftables.Set
	switch transport {
	case TransportTCP:
		protocol, set = unix.IPPROTO_TCP, sets.TCP
	case TransportUDP:
		protocol, set = unix.IPPROTO_UDP, sets.UDP
	default:
		return nil
	}
	if set == nil {
		return nil
	}
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{protocol}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Lookup{SourceRegister: 1, SetID: set.ID, SetName: set.Name},
	}
}

func linuxPortSet(table *nftables.Table, name string) *nftables.Set {
	return &nftables.Set{
		Table: table, Name: name, Constant: true,
		KeyType: nftables.TypeInetService, KeyByteOrder: binaryutil.BigEndian,
		Comment: "Irvio Safety Policy v0",
	}
}

func linuxPortElements(transport Transport) []nftables.SetElement {
	ports := Ports(V0, transport)
	elements := make([]nftables.SetElement, 0, len(ports))
	for _, port := range ports {
		elements = append(elements, nftables.SetElement{Key: binaryutil.BigEndian.PutUint16(port)})
	}
	return elements
}
