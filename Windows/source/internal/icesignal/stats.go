// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesignal

import "sync/atomic"

// Stats is a race-safe signaling activity snapshot.
type Stats struct {
	OffersStarted     uint64
	OffersSucceeded   uint64
	OffersRejected    uint64
	AnswersSent       uint64
	Unauthorized      uint64
	Busy              uint64
	Replays           uint64
	InvalidMessages   uint64
	AcceptedQueued    uint64
	AcceptedHandedOff uint64
	ActiveHandlers    uint64
	RecentSessions    uint64
}

type counters struct {
	offersStarted     atomic.Uint64
	offersSucceeded   atomic.Uint64
	offersRejected    atomic.Uint64
	answersSent       atomic.Uint64
	unauthorized      atomic.Uint64
	busy              atomic.Uint64
	replays           atomic.Uint64
	invalidMessages   atomic.Uint64
	acceptedQueued    atomic.Uint64
	acceptedHandedOff atomic.Uint64
	activeHandlers    atomic.Uint64
}
