// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package controlbypass

import (
	"encoding/binary"
	"path/filepath"
	"testing"

	"github.com/google/nftables/expr"
)

func TestCgroupMarkRuleUsesSocketOwnershipAndRealMark(t *testing.T) {
	expressions := cgroupMarkExpressions(0x0102030405060708, 3, 0x48594452)
	if len(expressions) != 7 {
		t.Fatalf("expressions=%d", len(expressions))
	}
	current, ok := expressions[0].(*expr.Meta)
	if !ok || current.Key != expr.MetaKeyMARK || current.SourceRegister {
		t.Fatalf("current mark expression=%+v", expressions[0])
	}
	zero, ok := expressions[1].(*expr.Cmp)
	if !ok || binary.NativeEndian.Uint32(zero.Data) != 0 {
		t.Fatalf("zero mark comparison=%+v", expressions[1])
	}
	socket, ok := expressions[2].(*expr.Socket)
	if !ok || socket.Key != expr.SocketKeyCgroupv2 || socket.Level != 3 {
		t.Fatalf("socket expression=%+v", expressions[2])
	}
	compare, ok := expressions[3].(*expr.Cmp)
	if !ok || binary.NativeEndian.Uint64(compare.Data) != 0x0102030405060708 {
		t.Fatalf("cgroup comparison=%+v", expressions[1])
	}
	set, ok := expressions[4].(*expr.Immediate)
	if !ok || len(set.Data) != 4 {
		t.Fatalf("mark immediate=%+v", expressions[2])
	}
	meta, ok := expressions[5].(*expr.Meta)
	if !ok || meta.Key != expr.MetaKeyMARK || !meta.SourceRegister {
		t.Fatalf("mark assignment=%+v", expressions[3])
	}
}

func TestInboundCgroupConnmarkDoesNotOverrideExplicitTunnelMark(t *testing.T) {
	expressions := cgroupConnmarkExpressions(0x0102030405060708, 3, 0x48594452)
	if len(expressions) < 4 {
		t.Fatalf("expressions=%d", len(expressions))
	}
	socketMark, ok := expressions[0].(*expr.Socket)
	if !ok || socketMark.Key != expr.SocketKeyMark {
		t.Fatalf("socket mark expression=%+v", expressions[0])
	}
	zero, ok := expressions[1].(*expr.Cmp)
	if !ok || binary.NativeEndian.Uint32(zero.Data) != 0 {
		t.Fatalf("socket mark guard=%+v", expressions[1])
	}
}

func TestAcceptedTCPListenerRuleMarksOnlySelectedDestinationPort(t *testing.T) {
	expressions := acceptedTCPConnmarkExpressions(41463, 0x48594452)
	if len(expressions) != 7 {
		t.Fatalf("expressions=%d", len(expressions))
	}
	protocol, ok := expressions[0].(*expr.Meta)
	if !ok || protocol.Key != expr.MetaKeyL4PROTO {
		t.Fatalf("protocol match=%+v", expressions[0])
	}
	tcp, ok := expressions[1].(*expr.Cmp)
	if !ok || len(tcp.Data) != 1 || tcp.Data[0] != 6 {
		t.Fatalf("TCP match=%+v", expressions[1])
	}
	destination, ok := expressions[2].(*expr.Payload)
	if !ok || destination.Base != expr.PayloadBaseTransportHeader || destination.Offset != 2 || destination.Len != 2 {
		t.Fatalf("TCP destination port extraction=%+v", expressions[2])
	}
	port, ok := expressions[3].(*expr.Cmp)
	if !ok || binary.BigEndian.Uint16(port.Data) != 41463 {
		t.Fatalf("destination port match=%+v", expressions[3])
	}
	mark, ok := expressions[5].(*expr.Ct)
	if !ok || mark.Key != expr.CtKeyMARK || !mark.SourceRegister {
		t.Fatalf("conntrack mark=%+v", expressions[5])
	}
}

func TestCgroupLevelUsesAbsoluteHierarchyDepth(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "sys", "fs", "cgroup")
	path := filepath.Join(root, "system.slice", "irviod.service", "hydra-control")
	if level := cgroupLevel(root, path); level != 3 {
		t.Fatalf("level=%d", level)
	}
}

func TestInvalidSharedIdentityIsRejectedBeforeKernelMutation(t *testing.T) {
	if owner, err := Install(Config{Name: "../shared", Mark: 1}); owner != nil || err != ErrInvalidConfig {
		t.Fatalf("owner=%v error=%v", owner, err)
	}
}

func TestOwnerMarkerIsBoundToDeterministicTableName(t *testing.T) {
	if first, second := ownerMarker("hydra_ctl_a"), ownerMarker("hydra_ctl_b"); first == second || first != OwnerMarker+":hydra_ctl_a" {
		t.Fatalf("owner markers are not table-bound: first=%q second=%q", first, second)
	}
}
