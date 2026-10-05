// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceengine

import (
	"sync/atomic"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
)

// Stats is a race-safe snapshot of engine and shared adapter activity.
type Stats struct {
	SessionsCreated uint64
	SessionsClosed  uint64
	ActiveSessions  uint64
	PeakSessions    uint64
	Adapter         iceadapter.Stats
}

type counters struct {
	created atomic.Uint64
	closed  atomic.Uint64
	peak    atomic.Uint64
}

func updatePeak(counter *atomic.Uint64, value uint64) {
	for current := counter.Load(); value > current; current = counter.Load() {
		if counter.CompareAndSwap(current, value) {
			return
		}
	}
}
