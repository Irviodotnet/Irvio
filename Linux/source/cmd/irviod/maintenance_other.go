// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build !windows

package main

import (
	"context"
	"errors"
)

func checkProductMaintenance(context.Context) error {
	return errors.New("--maintenance-check is an internal Windows installer operation")
}

func removeProductMaintenanceProfile(context.Context) error {
	return errors.New("--maintenance-remove-profile is an internal Windows installer operation")
}
