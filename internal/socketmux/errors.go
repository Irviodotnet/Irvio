// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import "errors"

var (
	// ErrClosed indicates that the mux ownership domain has shut down.
	ErrClosed = errors.New("socket mux is closed")
	// ErrFamilyUnavailable indicates that no physical socket owns the requested family.
	ErrFamilyUnavailable = errors.New("address family is unavailable")
	// ErrInvalidDestination indicates a non-routable or ambiguous destination.
	ErrInvalidDestination = errors.New("invalid UDP destination")
	// ErrPayloadTooLarge indicates that a write exceeds the configured datagram bound.
	ErrPayloadTooLarge = errors.New("UDP payload exceeds configured maximum")
)
