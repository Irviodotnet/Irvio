// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import "errors"

func (d *desktopController) persistentHosting() (bool, error) { return false, nil }
func (d *desktopController) clearPersistentHosting() error    { return nil }
func (d *desktopController) setPersistentHosting(bool) error {
	return errors.New("persistent sharing is supported on Linux only")
}
