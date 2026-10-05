// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import "errors"

var (
	// ErrClosed indicates that the profiler has stopped.
	ErrClosed = errors.New("NAT behavior profiler is closed")
	// ErrProbeInProgress indicates that the same address family is already being profiled.
	ErrProbeInProgress = errors.New("NAT behavior profile already in progress for address family")
	// ErrInvalidServer indicates an unusable or ambiguous primary server endpoint.
	ErrInvalidServer = errors.New("invalid RFC5780 server endpoint")
	// ErrInvalidTopology indicates inconsistent RFC 5780 topology attributes.
	ErrInvalidTopology = errors.New("invalid RFC5780 server topology")
	// ErrServerUnsupported indicates missing required RFC 5780 behavior support.
	ErrServerUnsupported = errors.New("server does not provide required RFC5780 attributes")
	// ErrMappedAddressMismatch indicates disagreement between MAPPED-ADDRESS and XOR-MAPPED-ADDRESS.
	ErrMappedAddressMismatch = errors.New("MAPPED-ADDRESS and XOR-MAPPED-ADDRESS disagree")
)
