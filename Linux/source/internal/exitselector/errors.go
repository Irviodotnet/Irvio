// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import "errors"

var (
	ErrAlreadyStarted      = errors.New("exit selector already started")
	ErrClosed              = errors.New("exit selector is closed")
	ErrInvalidCandidate    = errors.New("invalid exit candidate")
	ErrInvalidConfig       = errors.New("invalid exit selector configuration")
	ErrInvalidCountry      = errors.New("invalid country intent")
	ErrInvalidProvider     = errors.New("invalid candidate provider response")
	ErrNotStarted          = errors.New("exit selector is not started")
	ErrProtectedModeOff    = errors.New("Irvio ProtectedMode is not active")
	ErrProbeUnavailable    = errors.New("candidate probe is unavailable")
	ErrUnderlayUnavailable = errors.New("physical underlay is unavailable")
)
