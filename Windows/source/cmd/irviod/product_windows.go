// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/socketpolicy"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"golang.org/x/sys/windows"
)

const windowsProductService = "IRVIO"
const windowsRecoveryService = "IRVIO-Recovery"

type productPlatformOptions struct {
	root, runtime string
	owner         wfp.Config
}

func windowsProductOwner() wfp.Config {
	return wfp.Config{
		Provider: windows.GUID{Data1: 0x69aa32e1, Data2: 0x8f17, Data3: 0x4dbe, Data4: [8]byte{0xa8, 0x41, 0x40, 0x4b, 0xb9, 0x31, 0x98, 0x67}},
		Sublayer: windows.GUID{Data1: 0x66ba0cd4, Data2: 0x4187, Data3: 0x42f1, Data4: [8]byte{0x89, 0x4c, 0xd9, 0x1b, 0x67, 0x5a, 0xc2, 0x35}},
		Name:     "IRVIO ProtectedMode", Marker: "irvio-product-wfp-v1", ServiceName: windowsRecoveryService, Weight: 49200,
	}
}

func resolveProductOptions(o productOptions) (productOptions, error) {
	paths, err := productruntime.WindowsDefaultPaths()
	if err != nil {
		return o, err
	}
	if o.dataDir == "" {
		o.dataDir = paths.Profile
	}
	if o.geoipDB == "" {
		o.geoipDB = paths.GeoIP
	}
	if o.platform.root == "" {
		o.platform.root = filepath.Dir(paths.Config)
	}
	if o.platform.runtime == "" {
		o.platform.runtime = filepath.Join(o.platform.root, "runtime")
	}
	if o.platform.owner.Provider == (windows.GUID{}) {
		o.platform.owner = windowsProductOwner()
	}
	if o.intentPath == "" {
		o.intentPath = filepath.Join(o.platform.runtime, "session-state")
	}
	if !filepath.IsAbs(o.platform.root) || o.platform.runtime != filepath.Join(o.platform.root, "runtime") || !filepath.IsAbs(o.dataDir) {
		return o, fmt.Errorf("Windows product paths must be absolute and runtime must be inside the protected product root")
	}
	return o, nil
}

type windowsProductLease struct {
	root, runtime *winfile.Directory
	lock          *os.File
	once          sync.Once
	err           error
}

func (p *windowsProductLease) Close() error {
	p.once.Do(func() { p.err = errors.Join(winfile.Unlock(p.lock), p.lock.Close(), p.runtime.Close(), p.root.Close()) })
	return p.err
}

func acquireProductPlatform(o productOptions) (io.Closer, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return nil, fmt.Errorf("Windows IRVIO must be started by its SYSTEM service: %w", errors.Join(err, os.ErrPermission))
	}
	root, err := winfile.OpenDirectory(o.platform.root, true)
	if err != nil {
		return nil, err
	}
	directory, err := winfile.OpenDirectory(o.platform.runtime, true)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	lock, err := directory.OpenFile("product.lock", true, true)
	if err != nil {
		return nil, errors.Join(err, directory.Close(), root.Close())
	}
	if err := winfile.Lock(lock); err != nil {
		return nil, errors.Join(fmt.Errorf("another IRVIO product process owns this runtime: %w", err), lock.Close(), directory.Close(), root.Close())
	}
	lease := &windowsProductLease{root: root, runtime: directory, lock: lock}
	if o.role == productruntime.Server {
		_, present, err := (productintent.Store{Path: o.intentPath}).Read()
		if err != nil || present {
			return nil, errors.Join(fmt.Errorf("finish desktop session cleanup before starting a server: %v", err), lease.Close())
		}
	}
	return lease, nil
}

func productDaemonPlatform(p productPlatformOptions) irvioruntime.DaemonPlatformConfig {
	return irvioruntime.DaemonPlatformConfig{JournalDirectory: filepath.Join(p.runtime, "network"), ProtectionOwner: p.owner, RetainProtection: true}
}

func productProtectionPolicy() killswitch.Policy {
	return killswitch.Policy{TUNName: productTUNName(), BypassMark: socketpolicy.Underlay}
}

func windowsProductBackend(p productPlatformOptions) (*killswitch.WindowsBackend, error) {
	return killswitch.NewWindowsBackend(killswitch.WindowsBackendConfig{Owner: p.owner, Policy: productProtectionPolicy()})
}

func ensureWindowsProductProtection(ctx context.Context, p productPlatformOptions) error {
	backend, err := windowsProductBackend(p)
	if err != nil {
		return err
	}
	if _, _, err := backend.Ensure(ctx, productProtectionPolicy()); err != nil {
		return err
	}
	state, err := backend.Inspect(ctx, productProtectionPolicy())
	if err != nil {
		return err
	}
	if !state.Installed || !state.Complete {
		return errors.New("Windows product protection is incomplete")
	}
	return nil
}

func inspectWindowsProductProtection(ctx context.Context, p productPlatformOptions) (bool, error) {
	backend, err := windowsProductBackend(p)
	if err != nil {
		return false, err
	}
	state, err := backend.Inspect(ctx, productProtectionPolicy())
	if err != nil || !state.Installed {
		return false, err
	}
	return true, ensureWindowsProductProtection(ctx, p)
}

func cleanupWindowsProductProtection(ctx context.Context, p productPlatformOptions) error {
	if err := irvioruntime.RecoverWindowsDaemonResources(ctx, productDaemonPlatform(p).JournalDirectory); err != nil {
		return err
	}
	store, err := wfp.NewStore(p.owner)
	if err != nil {
		return err
	}
	return store.Remove(ctx)
}

func configureProductDesktop(ctx context.Context, o productOptions, d *desktopController) error {
	if d.ensureProtection == nil {
		d.ensureProtection = func(ctx context.Context) error { return ensureWindowsProductProtection(ctx, o.platform) }
	}
	if d.cleanupProtection == nil {
		d.cleanupProtection = func(ctx context.Context) error { return cleanupWindowsProductProtection(ctx, o.platform) }
	}
	inspect := o.inspectProtection
	if inspect == nil {
		inspect = func(ctx context.Context) (bool, error) { return inspectWindowsProductProtection(ctx, o.platform) }
	}
	var err error
	d.recoveryPending, err = inspect(ctx)
	return err
}

func readProductIntent(ctx context.Context, d *desktopController) (productintent.State, bool, error) {
	state, present, err := d.intent.Read()
	if errors.Is(err, productintent.ErrPreviousBoot) {
		if err := d.cleanupProtection(ctx); err != nil {
			return productintent.State{}, false, err
		}
		if err := d.intent.Clear(); err != nil {
			return productintent.State{}, false, err
		}
		d.recoveryPending = false
		return productintent.State{}, false, nil
	}
	if err != nil {
		return state, false, err
	}
	if state.ConnectIntent {
		if err := d.ensureProtection(ctx); err != nil {
			return state, false, err
		}
		d.recoveryPending = true
	}
	if err := irvioruntime.RecoverWindowsDaemonResources(ctx, productDaemonPlatform(d.platform).JournalDirectory); err != nil {
		return state, false, err
	}
	return state, present, nil
}

func finishProductPlatformTeardown(d *desktopController) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return d.cleanupProtection(ctx)
}

func resolveProductDaemon(o productOptions, addresses []string, country exitselector.CountryIntent) (irvioruntime.DaemonConfig, error) {
	o, err := resolveProductOptions(o)
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	if err := irvioruntime.RecoverWindowsDaemonResources(context.Background(), productDaemonPlatform(o.platform).JournalDirectory); err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	pool := productruntime.BootstrapSTUNIPv4WithTestAddresses(addresses, o.allowTest)
	path, err := productruntime.ResolveBootstrapUnderlay(pool, nil)
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	exitPool, tun, err := productruntime.ResolveExitPool(o.dataDir)
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	return resolveProductDaemonWithHost(o, addresses, country, pool, productUnderlay{IfName: path.Name, Source: path.Binding.Source}, exitPool, tun, nil)
}

func productTUNName() string { return "irvio0" }
func productGeoIPPath(o productOptions) (string, error) {
	if o.geoipDB != "" {
		return o.geoipDB, nil
	}
	paths, err := productruntime.WindowsDefaultPaths()
	return paths.GeoIP, err
}

func recoverWindowsPreviousBoot(ctx context.Context) (result error) {
	o, err := resolveProductOptions(productOptions{role: productruntime.Desktop})
	if err != nil {
		return err
	}
	owner, err := acquireProductPlatform(o)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, owner.Close()) }()
	store := productintent.Store{Path: o.intentPath}
	_, _, err = store.Read()
	if err == nil {
		return nil
	}
	if !errors.Is(err, productintent.ErrPreviousBoot) {
		return err
	}
	if err := cleanupWindowsProductProtection(ctx, o.platform); err != nil {
		return err
	}
	return store.Clear()
}
