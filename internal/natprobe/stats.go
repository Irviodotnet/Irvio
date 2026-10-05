// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import "sync/atomic"

// Stats is a race-safe point-in-time profiler snapshot.
type Stats struct {
	ProfilesStarted                 uint64
	ProfilesComplete                uint64
	ProfilesPartial                 uint64
	ProfilesServerUnsupported       uint64
	ProfilesConnectivityUnconfirmed uint64
	ProfilesInconclusive            uint64
	ProfilesRejectedInProgress      uint64
	ProfilesCanceled                uint64
	Exchanges                       uint64
	ExchangeTimeouts                uint64
	ExchangeProtocolErrors          uint64
}

type counters struct {
	profilesStarted                 atomic.Uint64
	profilesComplete                atomic.Uint64
	profilesPartial                 atomic.Uint64
	profilesServerUnsupported       atomic.Uint64
	profilesConnectivityUnconfirmed atomic.Uint64
	profilesInconclusive            atomic.Uint64
	profilesRejectedInProgress      atomic.Uint64
	profilesCanceled                atomic.Uint64
	exchanges                       atomic.Uint64
	exchangeTimeouts                atomic.Uint64
	exchangeProtocolErrors          atomic.Uint64
}
