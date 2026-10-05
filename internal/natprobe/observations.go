// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import (
	"net/netip"
	"time"
)

// TestStep identifies one ordered RFC 5780 diagnostic transaction.
type TestStep uint8

const (
	StepBaseline TestStep = iota + 1
	StepFilteringChangeIPPort
	StepFilteringLivenessAfterChangeIPPort
	StepFilteringChangePort
	StepFilteringFinalLiveness
	StepMappingAlternateIPPrimaryPort
	StepMappingAlternateIPAlternatePort
)

// ObservationOutcome is the bounded result category of a test step.
type ObservationOutcome uint8

const (
	OutcomeSuccess ObservationOutcome = iota + 1
	OutcomeTimeout
	OutcomeProtocolError
	OutcomeCanceled
	OutcomeClosed
)

// ObservationReason records a bounded diagnostic reason without network-controlled text.
type ObservationReason uint8

const (
	ReasonNone ObservationReason = iota
	ReasonTimeout
	ReasonLivenessFailed
	ReasonServerUnsupported
	ReasonInvalidResponse
	ReasonContextCanceled
	ReasonProfilerClosed
)

// Observation is bounded structured evidence for one transaction.
type Observation struct {
	Step                   TestStep
	RequestDestination     netip.AddrPort
	ExpectedResponseSource netip.AddrPort
	ActualResponseSource   netip.AddrPort
	MappedEndpoint         netip.AddrPort
	Attempts               int
	RoundTrip              time.Duration
	Outcome                ObservationOutcome
	Reason                 ObservationReason
}
