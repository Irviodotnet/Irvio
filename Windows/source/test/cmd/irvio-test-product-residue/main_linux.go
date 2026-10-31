// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

// hydra-test-product-residue cleans confirmed owner-marked crash residue in a lab.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Irviodotnet/Irvio/internal/exitfirewall"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
)

func main() {
	if _, err := os.Stat("/sys/class/net/hydra0"); err == nil {
		fmt.Fprintln(os.Stderr, "refusing cleanup while hydra0 exists")
		os.Exit(1)
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := linuxroute.CleanupCrashResidue(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := exitfirewall.CleanupCrashResidue(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
