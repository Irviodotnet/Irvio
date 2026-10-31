// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketpolicy

// Routing intent tags retain the existing Linux policy identifiers across platforms.
const (
	Underlay      uint32 = 0x48594452
	TunnelControl uint32 = 0x48595443
)
