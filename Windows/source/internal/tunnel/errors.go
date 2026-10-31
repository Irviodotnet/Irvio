// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnel

import "errors"

var (
	ErrInvalidConfig     = errors.New("invalid tunnel configuration")
	ErrClosed            = errors.New("tunnel is closed")
	ErrAlreadyBorrowed   = errors.New("tunnel already has an active borrower")
	ErrEventDrainTimeout = errors.New("tunnel event monitor did not stop before close deadline")
)
