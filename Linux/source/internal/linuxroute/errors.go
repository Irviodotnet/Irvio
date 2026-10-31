// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package linuxroute

import "errors"

var (
	ErrInvalidConfig = errors.New("invalid Linux routing configuration")
	ErrConflict      = errors.New("Linux routing identifier already in use")
	ErrClosed        = errors.New("Linux routing manager is closed")
)
