// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import "sync/atomic"

// Stats is a race-safe snapshot of ICE adapter activity.
type Stats struct {
	UfragsRegistered               uint64
	UfragsRemoved                  uint64
	ActiveUfrags                   uint64
	PeakActiveUfrags               uint64
	ActiveTransactions             uint64
	RetiredTransactions            uint64
	OutboundICERequestsObserved    uint64
	OutboundTransactionsRegistered uint64
	InboundRequestsRouted          uint64
	InboundResponsesRouted         uint64
	UnknownUfragPackets            uint64
	UnknownTransactions            uint64
	WrongSourceResponses           uint64
	WrongFamilyResponses           uint64
	RetiredResponsesDropped        uint64
	LogicalQueueDrops              uint64
	UnsupportedOutboundPayloads    uint64
	DispatcherErrors               uint64
	MalformedSTUNLikePackets       uint64
}

type counters struct {
	ufragsRegistered               atomic.Uint64
	ufragsRemoved                  atomic.Uint64
	peakActiveUfrags               atomic.Uint64
	outboundICERequestsObserved    atomic.Uint64
	outboundTransactionsRegistered atomic.Uint64
	inboundRequestsRouted          atomic.Uint64
	inboundResponsesRouted         atomic.Uint64
	unknownUfragPackets            atomic.Uint64
	unknownTransactions            atomic.Uint64
	wrongSourceResponses           atomic.Uint64
	wrongFamilyResponses           atomic.Uint64
	retiredResponsesDropped        atomic.Uint64
	logicalQueueDrops              atomic.Uint64
	unsupportedOutboundPayloads    atomic.Uint64
	dispatcherErrors               atomic.Uint64
	malformedSTUNLikePackets       atomic.Uint64
}

func updatePeak(counter *atomic.Uint64, value uint64) {
	for current := counter.Load(); value > current; current = counter.Load() {
		if counter.CompareAndSwap(current, value) {
			return
		}
	}
}
