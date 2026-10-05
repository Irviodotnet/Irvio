// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnelsession

import "errors"

var (
	ErrInvalidConfig   = errors.New("invalid logical tunnel configuration")
	ErrStaleGeneration = errors.New("stale tunnel transport generation")
	ErrClosed          = errors.New("logical tunnel is closed")
)
