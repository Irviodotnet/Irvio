// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Irviodotnet/Irvio/internal/exitfirewall"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
)

func runProduct(options productOptions) (runErr error) {
	root, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	return runProductWithContext(root, options)
}

func restartProductProcess() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(executable, os.Args, os.Environ())
}

type productPlatformOptions struct{}
type emptyProductPlatform struct{}

func (emptyProductPlatform) Close() error                      { return nil }
func acquireProductPlatform(productOptions) (io.Closer, error) { return emptyProductPlatform{}, nil }
func resolveProductOptions(o productOptions) (productOptions, error) {
	if o.dataDir == "" {
		o.dataDir = productruntime.DefaultDataDir
	}
	return o, nil
}
func configureProductDesktop(root context.Context, options productOptions, controller *desktopController) error {
	if controller.intent.Path == "" {
		controller.intent.Path = productintent.DefaultPath
	}
	if controller.ensureProtection == nil {
		controller.ensureProtection = ensureProductProtection
	}
	if controller.cleanupProtection == nil {
		controller.cleanupProtection = removeRecoveredProtection
	}
	inspect := options.inspectProtection
	if inspect == nil {
		inspect = inspectProductProtection
	}
	var err error
	controller.recoveryPending, err = inspect(root)
	if err != nil {
		return fmt.Errorf("inspect same-boot protected session: %w", err)
	}

	return nil
}
func readProductIntent(_ context.Context, d *desktopController) (productintent.State, bool, error) {
	return d.intent.Read()
}
func finishProductPlatformTeardown(*desktopController) error { return nil }
func productDaemonPlatform(productPlatformOptions) irvioruntime.DaemonPlatformConfig {
	return irvioruntime.DaemonPlatformConfig{}
}

func productProtectionPolicy() killswitch.Policy {
	return killswitch.Policy{TUNName: "hydra0", BypassMark: linuxroute.IrvioBypassMark,
		TunnelControlMark: linuxroute.IrvioTunnelControlMark}
}

func inspectProductProtection(ctx context.Context) (bool, error) {
	backend := killswitch.NFTBackend{}
	snapshot, err := backend.Inspect(ctx, productProtectionPolicy())
	if err != nil || !snapshot.Installed {
		return false, err
	}
	if _, _, err := backend.Ensure(ctx, productProtectionPolicy()); err != nil {
		return false, err
	}
	snapshot, err = backend.Inspect(ctx, productProtectionPolicy())
	if err != nil {
		return false, err
	}
	if !snapshot.Complete {
		return false, fmt.Errorf("persistent protected state is incomplete")
	}
	return true, nil
}

func ensureProductProtection(ctx context.Context) error {
	backend := killswitch.NFTBackend{}
	if _, _, err := backend.Ensure(ctx, productProtectionPolicy()); err != nil {
		return err
	}
	snapshot, err := backend.Inspect(ctx, productProtectionPolicy())
	if err != nil {
		return err
	}
	if !snapshot.Installed || !snapshot.Complete {
		return fmt.Errorf("IRVIO ProtectedMode is incomplete")
	}
	return nil
}

const productBootMarker = "/var/lib/irvio/boot-id"

func recoverPreviousBoot(ctx context.Context) error {
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return fmt.Errorf("read current boot ID: %w", err)
	}
	current := strings.TrimSpace(string(bootID))
	if len(current) < 32 || len(current) > 64 {
		return fmt.Errorf("current boot ID is empty")
	}
	parent := filepath.Dir(productBootMarker)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return err
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 ||
		parentInfo.Mode().Perm()&0o022 != 0 || !ok || parentStat.Uid != 0 {
		return fmt.Errorf("unsafe IRVIO state directory")
	}
	if info, err := os.Lstat(productBootMarker); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || !ok || stat.Uid != 0 {
			return fmt.Errorf("unsafe IRVIO boot marker")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	previous, err := os.ReadFile(productBootMarker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read IRVIO boot marker: %w", err)
	}
	if strings.TrimSpace(string(previous)) == current {
		return nil
	}
	if len(previous) != 0 {
		if err := removeRecoveredProtection(ctx); err != nil {
			return fmt.Errorf("reconcile previous-boot IRVIO state: %w", err)
		}
	}
	file, err := os.CreateTemp(parent, ".boot-id-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.WriteString(current + "\n"); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), productBootMarker)
}

func removeRecoveredProtection(ctx context.Context) error {
	if _, err := os.Stat("/sys/class/net/hydra0"); err == nil {
		return fmt.Errorf("cannot leave recovered protection while hydra0 is still owned by a live process")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect recovered TUN ownership: %w", err)
	}
	if err := linuxroute.CleanupCrashResidue(); err != nil {
		return fmt.Errorf("remove crash-owned IRVIO routing: %w", err)
	}
	if err := exitfirewall.CleanupCrashResidue(ctx); err != nil {
		return fmt.Errorf("remove crash-owned Exit firewall: %w", err)
	}
	controller, err := killswitch.New(killswitch.Config{Backend: killswitch.NFTBackend{}, Policy: productProtectionPolicy()})
	if err != nil {
		return err
	}
	if _, err := controller.Protect(ctx); err != nil {
		return err
	}
	if err := controller.Disconnect(ctx); err != nil {
		return err
	}
	return controller.Close(ctx)
}

func resolveProductDaemon(options productOptions, addresses []string, country exitselector.CountryIntent) (irvioruntime.DaemonConfig, error) {
	pool := productruntime.BootstrapSTUNIPv4WithTestAddresses(addresses, options.allowTest)
	if len(pool) == 0 {
		return irvioruntime.DaemonConfig{}, fmt.Errorf("bootstrap bundle has no direct IPv4 server for STUN UDP/3478")
	}
	path, err := productruntime.ResolveBootstrapUnderlay(pool, linuxroute.IrvioBypassMark)
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	exitPool, tunAddress, err := productruntime.ResolveExitPool(options.dataDir)
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	bypass, err := productruntime.ConnectedBypassPrefixes()
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	return resolveProductDaemonWithHost(options, addresses, country, pool, productUnderlay{IfName: path.IfName, Source: path.Source}, exitPool, tunAddress, bypass)
}

func productTUNName() string { return "hydra0" }
func productGeoIPPath(options productOptions) (string, error) {
	if options.geoipDB != "" {
		return options.geoipDB, nil
	}
	return productruntime.DefaultGeoIPPath, nil
}
