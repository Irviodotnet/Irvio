// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import "errors"

var (
	ErrInvalidConfig    = errors.New("invalid Irvio kill-switch configuration")
	ErrConflict         = errors.New("nftables object is not owned by Irvio")
	ErrVersionMismatch  = errors.New("Irvio kill-switch policy version mismatch")
	ErrNotProtected     = errors.New("Irvio ProtectedMode is not active")
	ErrAlreadyProtected = errors.New("Irvio ProtectedMode is already active")
	ErrStaleGeneration  = errors.New("stale Irvio protection generation")
	ErrClosed           = errors.New("Irvio ProtectedMode controller is closed")
)
