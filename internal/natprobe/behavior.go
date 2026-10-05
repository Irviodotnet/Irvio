// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

// MappingBehavior describes destination sensitivity of a UDP mapping.
type MappingBehavior uint8

const (
	MappingUnknown MappingBehavior = iota
	MappingEndpointIndependent
	MappingAddressDependent
	MappingAddressAndPortDependent
)

// FilteringBehavior describes which remote endpoints can send through filtering state.
type FilteringBehavior uint8

const (
	FilteringUnknown FilteringBehavior = iota
	FilteringEndpointIndependent
	FilteringAddressDependent
	FilteringAddressAndPortDependent
)

// TranslationState reports whether the concrete local endpoint differs from the mapped endpoint.
type TranslationState uint8

const (
	TranslationUnknown TranslationState = iota
	TranslationAbsent
	TranslationPresent
)

// ProfileStatus summarizes how much of a profile was established.
type ProfileStatus uint8

const (
	ProfileComplete ProfileStatus = iota + 1
	ProfilePartial
	ProfileServerUnsupported
	ProfileConnectivityUnconfirmed
	ProfileInconclusive
)
