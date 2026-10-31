// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import "github.com/Irviodotnet/Irvio/internal/wintest"

// Local diagnostic counts handle types without collecting names or accessing another process.
func handleTypes() map[string]int {
	return wintest.HandleTypes()
}

// Go's Windows minit/semacreate allocate six handles per M; cap rather than ignore that growth.
func resourceGrowth(before, after map[string]int) error {
	return wintest.ResourceGrowth(before, after)
}
