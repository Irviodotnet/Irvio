// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceengine

import "errors"

var (
	ErrClosed          = errors.New("ICE engine is closed")
	ErrTooManySessions = errors.New("too many ICE sessions")
)
