// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package main

import (
	"slices"

	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
)

// A bootstrap replacement must not inherit the previous network's warm discovery hints.
func (d *desktopController) applyWarmDiscovery(config *irvioruntime.DaemonConfig) {
	if len(d.warmBootstrap) != 0 && slices.Equal(d.warmBootstrap, config.Bootstrap) {
		config.M12.WarmDiscovery = d.warmDiscovery
	}
}
