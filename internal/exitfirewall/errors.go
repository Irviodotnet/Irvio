// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitfirewall

import "errors"

var (
	ErrInvalidPolicy = errors.New("invalid Irvio Exit firewall policy")
	ErrConflict      = errors.New("Irvio Exit nftables table already exists")
	ErrClosed        = errors.New("Irvio Exit firewall is closed")
)
