// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package connsupervisor

import "time"

// Timer is the minimal deterministic timer contract used by the supervisor.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Clock constructs reconnect and disconnect-grace timers.
type Clock interface {
	NewTimer(time.Duration) Timer
}

type realClock struct{}

func (realClock) NewTimer(duration time.Duration) Timer { return realTimer{time.NewTimer(duration)} }

type realTimer struct{ timer *time.Timer }

func (t realTimer) C() <-chan time.Time { return t.timer.C }
func (t realTimer) Stop() bool          { return t.timer.Stop() }
