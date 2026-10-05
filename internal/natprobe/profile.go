// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import (
	"net/netip"
	"time"

	"hydra.local/hydra/internal/socketmux"
)

// Profile is one bounded diagnostic snapshot for an active mux socket and path.
type Profile struct {
	Family                       socketmux.AddressFamily
	PrimaryServer                netip.AddrPort
	Topology                     Topology
	LocalEndpoint                netip.AddrPort
	InitialMappedEndpoint        netip.AddrPort
	Translation                  TranslationState
	Mapping                      MappingBehavior
	Filtering                    FilteringBehavior
	Status                       ProfileStatus
	StartedAt                    time.Time
	CompletedAt                  time.Time
	Duration                     time.Duration
	Observations                 []Observation
	FilteringInferredFromTimeout bool
	Reason                       ObservationReason
}
