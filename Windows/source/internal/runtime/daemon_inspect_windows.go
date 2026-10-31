// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Irviodotnet/Irvio/internal/windns"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/Irviodotnet/Irvio/internal/winfirewall"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

// CheckWindowsDaemonJournalsInactive validates recorded ownership only; the product must also check WFP and intent.
func CheckWindowsDaemonJournalsInactive(ctx context.Context, directory string) error {
	if ctx == nil || !filepath.IsAbs(directory) {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return winfile.InspectLockedDirectory(directory, "daemon.lock", func(*winfile.Directory) error {
		sockets := filepath.Join(directory, "sockets")
		if err := winfile.InspectLockedDirectory(sockets, "socket-permissions.lock", func(*winfile.Directory) error {
			for slot := 0; slot < 12; slot++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := winfirewall.CheckInactiveJournal(socketPermissionSlot(sockets, slot)); err != nil {
					return fmt.Errorf("socket slot %d: %w", slot, err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		plane := filepath.Join(directory, "dataplane")
		return withWindowsRecoveryDirectory(ctx, plane, func() error {
			if err := winfirewall.CheckInactiveJournal(filepath.Join(plane, "control")); err != nil {
				return err
			}
			for _, scope := range []string{plane, filepath.Join(plane, "upstream")} {
				if err := withWindowsRecoveryDirectory(ctx, scope, func() error {
					if err := windns.CheckInactiveJournal(filepath.Join(scope, "dns")); err != nil {
						return err
					}
					return winroute.CheckInactiveJournal(filepath.Join(scope, "routes"))
				}); err != nil {
					return err
				}
			}
			return nil
		})
	})
}
