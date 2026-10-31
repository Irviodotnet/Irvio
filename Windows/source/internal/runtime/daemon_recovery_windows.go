// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/windns"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/Irviodotnet/Irvio/internal/winfirewall"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

// The product owns persistent intent; a network attempt may revoke grants but cannot restore WAN.
type windowsRetainedProtection struct{ *killswitch.WindowsBackend }

func (b windowsRetainedProtection) Ensure(ctx context.Context, policy killswitch.Policy) (killswitch.Installation, killswitch.EnsureResult, error) {
	installation, result, err := b.WindowsBackend.Ensure(ctx, policy)
	if err != nil {
		return nil, result, err
	}
	updater, err := b.NetworkUpdater(ctx)
	if err != nil {
		return nil, result, err
	}
	return windowsRetainedInstallation{Installation: installation, revoke: func(ctx context.Context) error {
		return updater.Update(ctx, killswitch.WindowsNetwork{})
	}}, result, nil
}

type windowsRetainedInstallation struct {
	killswitch.Installation
	revoke func(context.Context) error
}

// Remove retires only this attempt's grants; explicit product teardown removes the persistent policy.
func (i windowsRetainedInstallation) Remove(ctx context.Context) error {
	if err := i.revoke(ctx); err != nil {
		return err
	}
	snapshot, err := i.Installation.Snapshot(ctx)
	if err != nil {
		return err
	}
	if !snapshot.Installed || !snapshot.Complete {
		return errors.New("retired Windows attempt lost persistent protection")
	}
	return nil
}

// RecoverWindowsDaemonResources never removes WFP; the caller decides from durable product intent.
func RecoverWindowsDaemonResources(ctx context.Context, directory string) error {
	if ctx == nil || !filepath.IsAbs(directory) {
		return ErrInvalidConfig
	}
	return withWindowsRecoveryDirectory(ctx, directory, func() (resultErr error) {
		owner, err := openWindowsDaemonJournal(directory)
		if err != nil {
			return fmt.Errorf("lock Windows daemon recovery: %w", err)
		}
		defer func() { resultErr = errors.Join(resultErr, owner.Close()) }()
		return recoverWindowsDaemonJournals(ctx, directory)
	})
}

func withWindowsRecoveryDirectory(ctx context.Context, path string, recover func() error) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := winfile.OpenDirectory(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	return recover()
}

func recoverWindowsDaemonJournals(ctx context.Context, directory string) error {
	sockets := filepath.Join(directory, "sockets")
	if err := withWindowsRecoveryDirectory(ctx, sockets, func() error { return RecoverWindowsSocketPermissions(sockets) }); err != nil {
		return fmt.Errorf("recover Windows socket permissions: %w", err)
	}
	plane := filepath.Join(directory, "dataplane")
	return withWindowsRecoveryDirectory(ctx, plane, func() error {
		control := filepath.Join(plane, "control")
		if err := withWindowsRecoveryDirectory(ctx, control, func() error { return winfirewall.RecoverControl(control) }); err != nil {
			return fmt.Errorf("recover Windows Exit control: %w", err)
		}
		if err := recoverWindowsSessionJournals(ctx, plane); err != nil {
			return err
		}
		upstream := filepath.Join(plane, "upstream")
		return withWindowsRecoveryDirectory(ctx, upstream, func() error { return recoverWindowsSessionJournals(ctx, upstream) })
	})
}

func recoverWindowsSessionJournals(ctx context.Context, directory string) error {
	dns := filepath.Join(directory, "dns")
	if err := withWindowsRecoveryDirectory(ctx, dns, func() error { return windns.Recover(ctx, dns) }); err != nil {
		return fmt.Errorf("recover Windows session DNS: %w", err)
	}
	routes := filepath.Join(directory, "routes")
	if err := withWindowsRecoveryDirectory(ctx, routes, func() error { return winroute.RecoverTunnelRoutes(ctx, routes) }); err != nil {
		return fmt.Errorf("recover Windows session routes: %w", err)
	}
	return nil
}
