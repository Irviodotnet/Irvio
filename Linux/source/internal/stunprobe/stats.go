// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import "sync/atomic"

// Stats is a race-safe point-in-time client and classifier snapshot.
type Stats struct {
	ProbesStarted                      uint64
	ProbesSucceeded                    uint64
	ProbesTimedOut                     uint64
	ProbesCanceled                     uint64
	ProbesRejectedInFlight             uint64
	RequestsTransmitted                uint64
	Retransmissions                    uint64
	ValidResponsesDispatched           uint64
	MalformedSTUNLikePackets           uint64
	WrongSourceResponsesIgnored        uint64
	UnknownTransactionResponsesIgnored uint64
	RetiredLateResponsesDropped        uint64
	DispatcherQueueDrops               uint64
	DispatcherErrors                   uint64
}

type clientCounters struct {
	probesStarted            atomic.Uint64
	probesSucceeded          atomic.Uint64
	probesTimedOut           atomic.Uint64
	probesCanceled           atomic.Uint64
	probesRejectedInFlight   atomic.Uint64
	requestsTransmitted      atomic.Uint64
	retransmissions          atomic.Uint64
	validResponsesDispatched atomic.Uint64
	dispatcherErrors         atomic.Uint64
}

type registryCounters struct {
	malformedSTUNLike    atomic.Uint64
	wrongSource          atomic.Uint64
	unknownTransaction   atomic.Uint64
	retiredLate          atomic.Uint64
	dispatcherQueueDrops atomic.Uint64
}

func (c *Client) statsSnapshot() Stats {
	return Stats{
		ProbesStarted:                      c.stats.probesStarted.Load(),
		ProbesSucceeded:                    c.stats.probesSucceeded.Load(),
		ProbesTimedOut:                     c.stats.probesTimedOut.Load(),
		ProbesCanceled:                     c.stats.probesCanceled.Load(),
		ProbesRejectedInFlight:             c.stats.probesRejectedInFlight.Load(),
		RequestsTransmitted:                c.stats.requestsTransmitted.Load(),
		Retransmissions:                    c.stats.retransmissions.Load(),
		ValidResponsesDispatched:           c.stats.validResponsesDispatched.Load(),
		MalformedSTUNLikePackets:           c.registry.stats.malformedSTUNLike.Load(),
		WrongSourceResponsesIgnored:        c.registry.stats.wrongSource.Load(),
		UnknownTransactionResponsesIgnored: c.registry.stats.unknownTransaction.Load(),
		RetiredLateResponsesDropped:        c.registry.stats.retiredLate.Load(),
		DispatcherQueueDrops:               c.registry.stats.dispatcherQueueDrops.Load(),
		DispatcherErrors:                   c.stats.dispatcherErrors.Load(),
	}
}
