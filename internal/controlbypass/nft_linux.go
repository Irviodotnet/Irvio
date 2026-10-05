// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package controlbypass

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"golang.org/x/sys/unix"
)

const (
	preroutingChainName = "prerouting_v1"
	inputChainName      = "input_v1"
	routeChainName      = "route_output_v1"
)

type nftOwnership struct {
	table   *nftables.Table
	mu      sync.Mutex
	removed bool
}

func ensureNFT(tableName string, mark uint32, cgroupID uint64, level uint32, listenPorts []uint16) (*nftOwnership, error) {
	existing, err := findNFTTable(tableName)
	if err != nil {
		return nil, err
	}
	connection := &nftables.Conn{}
	if existing != nil {
		owned, err := ownedNFT(existing, tableName)
		if err != nil {
			return nil, err
		}
		if !owned {
			return nil, ErrConflict
		}
		connection.DelTable(existing)
	}
	table := connection.CreateTable(&nftables.Table{Name: tableName, Family: nftables.TableFamilyINet})
	accept := nftables.ChainPolicyAccept
	prerouting := connection.AddChain(&nftables.Chain{
		Name: preroutingChainName, Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookPrerouting, Priority: nftables.ChainPriorityMangle, Policy: &accept,
	})
	input := connection.AddChain(&nftables.Chain{
		Name: inputChainName, Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookInput, Priority: nftables.ChainPriorityMangle, Policy: &accept,
	})
	chain := connection.AddChain(&nftables.Chain{
		Name: routeChainName, Table: table, Type: nftables.ChainTypeRoute,
		Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityMangle, Policy: &accept,
	})
	ownerRule := &nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{&expr.Counter{}}}
	ownerRule.UserData = userdata.AppendString(nil, userdata.TypeComment, ownerMarker(tableName))
	connection.AddRule(ownerRule)
	inboundRule := &nftables.Rule{Table: table, Chain: prerouting, Exprs: cgroupConnmarkExpressions(cgroupID, level, mark)}
	inboundRule.UserData = userdata.AppendString(nil, userdata.TypeComment, "mark-inbound-hydrad-cgroup")
	connection.AddRule(inboundRule)
	for _, port := range listenPorts {
		rule := &nftables.Rule{Table: table, Chain: input, Exprs: acceptedTCPConnmarkExpressions(port, mark)}
		rule.UserData = userdata.AppendString(nil, userdata.TypeComment, "mark-accepted-hydrad-tcp")
		connection.AddRule(rule)
	}
	restoreRule := &nftables.Rule{Table: table, Chain: chain, Exprs: restoreConnmarkExpressions(mark)}
	restoreRule.UserData = userdata.AppendString(nil, userdata.TypeComment, "restore-inbound-control-mark")
	connection.AddRule(restoreRule)
	markRule := &nftables.Rule{Table: table, Chain: chain, Exprs: cgroupMarkExpressions(cgroupID, level, mark)}
	markRule.UserData = userdata.AppendString(nil, userdata.TypeComment, "mark-hydrad-cgroup")
	connection.AddRule(markRule)
	if err := connection.Flush(); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("install Irvio control route marking: %w", err)
	}
	return &nftOwnership{table: table}, nil
}

// Mark inbound conntrack before SYN-ACK; socket cgroup lookup may miss the first SYN.
func acceptedTCPConnmarkExpressions(port uint16, mark uint32) []expr.Any {
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, port)
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: portBytes},
		&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint32(mark)},
		&expr.Ct{Key: expr.CtKeyMARK, SourceRegister: true, Register: 1},
		&expr.Counter{},
	}
}

func cgroupConnmarkExpressions(cgroupID uint64, level, mark uint32) []expr.Any {
	return []expr.Any{
		&expr.Socket{Key: expr.SocketKeyMark, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(0)},
		&expr.Socket{Key: expr.SocketKeyCgroupv2, Level: level, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint64(cgroupID)},
		&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint32(mark)},
		&expr.Ct{Key: expr.CtKeyMARK, SourceRegister: true, Register: 1},
		&expr.Counter{},
	}
}

func restoreConnmarkExpressions(mark uint32) []expr.Any {
	return []expr.Any{
		&expr.Ct{Key: expr.CtKeyMARK, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(mark)},
		&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint32(mark)},
		&expr.Meta{Key: expr.MetaKeyMARK, SourceRegister: true, Register: 1},
		&expr.Counter{},
	}
}

func cgroupMarkExpressions(cgroupID uint64, level, mark uint32) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyMARK, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(0)},
		&expr.Socket{Key: expr.SocketKeyCgroupv2, Level: level, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint64(cgroupID)},
		&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint32(mark)},
		&expr.Meta{Key: expr.MetaKeyMARK, SourceRegister: true, Register: 1},
		&expr.Counter{},
	}
}

func findNFTTable(name string) (*nftables.Table, error) {
	tables, err := (&nftables.Conn{}).ListTables()
	if err != nil {
		return nil, err
	}
	for _, table := range tables {
		if table.Family == nftables.TableFamilyINet && table.Name == name {
			return table, nil
		}
	}
	return nil, nil
}

func ownedNFT(table *nftables.Table, tableName string) (bool, error) {
	connection := &nftables.Conn{}
	chains, err := connection.ListChains()
	if err != nil {
		return false, err
	}
	for _, chain := range chains {
		if chain.Table == nil || chain.Table.Name != table.Name || chain.Table.Family != table.Family {
			continue
		}
		rules, err := connection.GetRules(table, chain)
		if err != nil {
			return false, err
		}
		for _, rule := range rules {
			label, _ := userdata.GetString(rule.UserData, userdata.TypeComment)
			if label == ownerMarker(tableName) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (n *nftOwnership) counter() (uint64, uint64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.removed {
		return 0, 0, nil
	}
	connection := &nftables.Conn{}
	chains, err := connection.ListChains()
	if err != nil {
		return 0, 0, err
	}
	for _, chain := range chains {
		if chain.Table == nil || chain.Table.Name != n.table.Name || chain.Name != routeChainName {
			continue
		}
		rules, err := connection.GetRules(n.table, chain)
		if err != nil {
			return 0, 0, err
		}
		for _, rule := range rules {
			label, _ := userdata.GetString(rule.UserData, userdata.TypeComment)
			if label != "mark-hydrad-cgroup" {
				continue
			}
			for _, expression := range rule.Exprs {
				if counter, ok := expression.(*expr.Counter); ok {
					return counter.Packets, counter.Bytes, nil
				}
			}
		}
	}
	return 0, 0, ErrConflict
}

func (n *nftOwnership) remove() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.removed {
		return nil
	}
	table, err := findNFTTable(n.table.Name)
	if err != nil {
		return err
	}
	if table != nil {
		owned, err := ownedNFT(table, n.table.Name)
		if err != nil {
			return err
		}
		if !owned {
			return ErrConflict
		}
		connection := &nftables.Conn{}
		connection.DelTable(table)
		if err := connection.Flush(); err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	n.removed = true
	return nil
}

func ownerMarker(tableName string) string { return OwnerMarker + ":" + tableName }
