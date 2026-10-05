// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnellease

import "errors"

var (
	ErrInvalidConfig     = errors.New("invalid tunnel lease configuration")
	ErrInvalidPeer       = errors.New("invalid tunnel lease peer")
	ErrExhausted         = errors.New("tunnel lease capacity exhausted")
	ErrUnknownLease      = errors.New("unknown tunnel lease")
	ErrWrongPeer         = errors.New("tunnel lease belongs to another peer")
	ErrLeaseReleased     = errors.New("tunnel lease already released")
	ErrLeaseExpired      = errors.New("tunnel lease expired")
	ErrStaleGeneration   = errors.New("stale tunnel lease generation")
	ErrRevocationPending = errors.New("tunnel lease revocation pending")
	ErrClosed            = errors.New("tunnel lease allocator is closed")
)
