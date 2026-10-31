// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import "sync/atomic"

// FamilyStats is a race-safe snapshot of physical socket traffic and errors.
type FamilyStats struct {
	ReceivedPackets    uint64
	ReceivedBytes      uint64
	TransmittedPackets uint64
	TransmittedBytes   uint64
	ReadErrors         uint64
	WriteErrors        uint64
}

// Stats is a point-in-time mux statistics snapshot.
type Stats struct {
	IPv4 FamilyStats
	IPv6 FamilyStats

	ControlEnqueued      uint64
	DataEnqueued         uint64
	ClassifierDrops      uint64
	ControlOverflowDrops uint64
	DataOverflowDrops    uint64
	OversizedDrops       uint64
}

type familyCounters struct {
	receivedPackets    atomic.Uint64
	receivedBytes      atomic.Uint64
	transmittedPackets atomic.Uint64
	transmittedBytes   atomic.Uint64
	readErrors         atomic.Uint64
	writeErrors        atomic.Uint64
}

func (c *familyCounters) snapshot() FamilyStats {
	return FamilyStats{
		ReceivedPackets:    c.receivedPackets.Load(),
		ReceivedBytes:      c.receivedBytes.Load(),
		TransmittedPackets: c.transmittedPackets.Load(),
		TransmittedBytes:   c.transmittedBytes.Load(),
		ReadErrors:         c.readErrors.Load(),
		WriteErrors:        c.writeErrors.Load(),
	}
}

type counters struct {
	ipv4 familyCounters
	ipv6 familyCounters

	controlEnqueued      atomic.Uint64
	dataEnqueued         atomic.Uint64
	classifierDrops      atomic.Uint64
	controlOverflowDrops atomic.Uint64
	dataOverflowDrops    atomic.Uint64
	oversizedDrops       atomic.Uint64
}

func (c *counters) family(family AddressFamily) *familyCounters {
	if family == IPv6 {
		return &c.ipv6
	}
	return &c.ipv4
}

func (c *counters) snapshot() Stats {
	return Stats{
		IPv4:                 c.ipv4.snapshot(),
		IPv6:                 c.ipv6.snapshot(),
		ControlEnqueued:      c.controlEnqueued.Load(),
		DataEnqueued:         c.dataEnqueued.Load(),
		ClassifierDrops:      c.classifierDrops.Load(),
		ControlOverflowDrops: c.controlOverflowDrops.Load(),
		DataOverflowDrops:    c.dataOverflowDrops.Load(),
		OversizedDrops:       c.oversizedDrops.Load(),
	}
}
