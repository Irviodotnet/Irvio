// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"net/netip"
	"sync/atomic"
)

// BindStats is a race-safe bounded counter snapshot.
type BindStats struct {
	OpenCalls       uint64
	CloseCalls      uint64
	SendPackets     uint64
	SendBytes       uint64
	ReceivePackets  uint64
	ReceiveBytes    uint64
	SendIPv4        uint64
	SendIPv6        uint64
	ReceiveIPv4     uint64
	ReceiveIPv6     uint64
	ReceiveTooLarge uint64
	LastSend        netip.AddrPort
	LastReceive     netip.AddrPort
}

type bindCounters struct {
	openCalls, closeCalls        atomic.Uint64
	sendPackets, sendBytes       atomic.Uint64
	receivePackets, receiveBytes atomic.Uint64
	sendIPv4, sendIPv6           atomic.Uint64
	receiveIPv4, receiveIPv6     atomic.Uint64
	receiveTooLarge              atomic.Uint64
	lastSend, lastReceive        atomic.Value
}

func (c *bindCounters) snapshot() BindStats {
	return BindStats{
		OpenCalls: c.openCalls.Load(), CloseCalls: c.closeCalls.Load(),
		SendPackets: c.sendPackets.Load(), SendBytes: c.sendBytes.Load(),
		ReceivePackets: c.receivePackets.Load(), ReceiveBytes: c.receiveBytes.Load(),
		SendIPv4: c.sendIPv4.Load(), SendIPv6: c.sendIPv6.Load(),
		ReceiveIPv4: c.receiveIPv4.Load(), ReceiveIPv6: c.receiveIPv6.Load(),
		ReceiveTooLarge: c.receiveTooLarge.Load(),
		LastSend:        loadAddrPort(&c.lastSend),
		LastReceive:     loadAddrPort(&c.lastReceive),
	}
}

func loadAddrPort(value *atomic.Value) netip.AddrPort {
	loaded := value.Load()
	if loaded == nil {
		return netip.AddrPort{}
	}
	return loaded.(netip.AddrPort)
}
