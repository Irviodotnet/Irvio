// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesession

import "errors"

var (
	// ErrControlBusy indicates another high-level owner holds Mux.Control.
	ErrControlBusy = errors.New("Irvio Control phase is busy")
	// ErrWrongPhase indicates that a lease does not own the requested phase.
	ErrWrongPhase = errors.New("wrong Irvio Control phase")
	// ErrClosed indicates that a phase manager or prepared session is closed.
	ErrClosed = errors.New("ICE session is closed")
	// ErrInvalidDescription indicates invalid credentials or candidate state.
	ErrInvalidDescription = errors.New("invalid ICE description")
	// ErrConnectivityClaimed indicates a PreparedSession already has an ICE generation.
	ErrConnectivityClaimed = errors.New("prepared ICE session connectivity already claimed")
	// ErrPreparedSessionClosed indicates connectivity was requested after session shutdown.
	ErrPreparedSessionClosed = errors.New("prepared ICE session is closed")
	// ErrSessionAlreadyBound indicates that another signaling exchange already owns the session.
	ErrSessionAlreadyBound = errors.New("prepared ICE session already bound to signaling")
	// ErrSessionNotBound indicates that authenticated signaling did not bind the session.
	ErrSessionNotBound = errors.New("prepared ICE session is not bound to signaling")
)
