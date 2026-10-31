// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import "errors"

var (
	// ErrClosed indicates that the adapter or logical connection is closed.
	ErrClosed = errors.New("ICE adapter is closed")
	// ErrTransportClosed indicates that IrvioSocketMux closed first.
	ErrTransportClosed = errors.New("Irvio UDP transport is closed")
	// ErrNonICEPayloadUnsupported rejects application data on the ICE control path.
	ErrNonICEPayloadUnsupported = errors.New("non-ICE payload is unsupported")
	// ErrTooManyUfrags indicates that the bounded local ufrag registry is full.
	ErrTooManyUfrags = errors.New("too many active ICE ufrags")
	// ErrTooManyTransactions indicates that the outbound transaction registry is full.
	ErrTooManyTransactions = errors.New("too many active ICE transactions")
	// ErrDuplicateTransaction indicates conflicting reuse of a transaction ID.
	ErrDuplicateTransaction = errors.New("duplicate ICE transaction ID")
	// ErrInvalidUfrag indicates an invalid ICE username fragment.
	ErrInvalidUfrag = errors.New("invalid ICE username fragment")
)
