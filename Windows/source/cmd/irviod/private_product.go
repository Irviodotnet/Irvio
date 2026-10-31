// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package main

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/identity"
	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/libp2p/go-libp2p/core/peer"
)

type privateProductIntent struct {
	Target  peer.ID
	Hosting bool
}

func (intent privateProductIntent) active() bool { return intent.Target != "" || intent.Hosting }

func (d *desktopController) intentTarget() exitselector.Target {
	if d.desiredPrivate.Target != "" {
		return exitselector.Target{Kind: exitselector.TargetPrivatePeer, PeerID: d.desiredPrivate.Target}
	}
	return exitselector.Target{Kind: exitselector.TargetPublicCountry, Country: d.desiredCountry}
}

func (d *desktopController) restoreIntent(ctx context.Context, state productintent.State) error {
	var private privateProductIntent
	private.Hosting = state.PrivateHosting
	if state.PrivateTarget != "" {
		id, err := peer.Decode(state.PrivateTarget)
		if err != nil {
			return irvioruntime.ErrInvalidConfig
		}
		private.Target = id
	}
	if state.ConnectIntent {
		if err := d.ensureProtection(ctx); err != nil {
			return fmt.Errorf("protect same-boot ConnectIntent: %w", err)
		}
		d.recoveryPending = true
	}
	d.desiredCountry = exitselector.CountryIntent(state.Country)
	d.desiredPrivate = private
	return nil
}

func (d *desktopController) openPrivateStoreLocked() error {
	if d.privateStore != nil {
		return nil
	}
	id, err := identity.LoadOrCreate(d.dataDir)
	if err != nil {
		return err
	}
	store, err := privateaccess.Open(d.dataDir, id.PeerID)
	if err != nil {
		return err
	}
	d.privateStore = store
	return nil
}

func (d *desktopController) closePrivateStoreLocked() error {
	if d.privateStore == nil {
		return nil
	}
	err := d.privateStore.Close()
	d.privateStore = nil
	return err
}

// ConnectPrivate changes the whole desktop intent only from a fully disconnected state.
func (d *desktopController) ConnectPrivate(ctx context.Context, target peer.ID, hosting bool) error {
	d.lockCommand()
	defer d.mu.Unlock()
	return d.connectPrivateLocked(ctx, privateProductIntent{Target: target, Hosting: hosting}, true)
}

func (d *desktopController) connectPrivateLocked(ctx context.Context, intent privateProductIntent, persist bool) error {
	if d.closed || d.daemon != nil || d.teardownPending || d.desiredCountry != "" || !intent.active() ||
		(d.desiredPrivate.active() && d.desiredPrivate != intent) || (d.recoveryPending && intent.Target == "") {
		return irvioruntime.ErrInvalidState
	}
	if err := d.closeIdleProbeLocked(ctx); err != nil {
		return err
	}
	starting := d.statusLocked()
	starting.State = irvioruntime.StateStarting
	starting.PrivateHostingWanted = intent.Hosting
	if intent.Target != "" {
		starting.Target = exitselector.Target{Kind: exitselector.TargetPrivatePeer, PeerID: intent.Target}
	}
	d.cacheStatus(starting)
	defer func() { d.cacheStatus(d.statusLocked()) }()
	if err := d.openPrivateStoreLocked(); err != nil {
		return err
	}
	if _, err := privateIntentLocator(d.privateStore, intent); err != nil {
		return err
	}
	addresses, err := d.bootstrap.StartupAddresses()
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("import an IRVIO bootstrap bundle before private CONNECT: %w", irvioruntime.ErrInvalidConfig)
	}
	config, err := resolvePrivateProductDaemon(productOptions{role: productruntime.Desktop,
		dataDir: d.dataDir, logger: d.logger, allowTest: d.allowTest, geoipDB: d.geoipDB, platform: d.platform}, addresses, d.privateStore, intent)
	if err != nil {
		return err
	}
	d.applyWarmDiscovery(&config)
	daemon, err := irvioruntime.NewDaemon(config)
	if err != nil {
		return err
	}
	protected := intent.Target != ""
	if protected {
		if err := d.ensureProtection(ctx); err != nil {
			return fmt.Errorf("protect private ConnectIntent: %w", err)
		}
		d.recoveryPending = true
	}
	if persist {
		if err := d.intent.WantPrivate(intent.Target, intent.Hosting); err != nil {
			return fmt.Errorf("persist private intent: %w", err)
		}
		d.desiredPrivate = intent
	}
	return d.startDaemonLocked(ctx, daemon, persist, protected)
}

// privateIntentLocator accepts only saved targets; callers cannot inject a different locator.
func privateIntentLocator(store *privateaccess.Store, intent privateProductIntent) ([32]byte, error) {
	if store == nil || intent.Target == "" && !intent.Hosting {
		return [32]byte{}, irvioruntime.ErrInvalidConfig
	}
	state, err := store.Snapshot()
	if err != nil {
		return [32]byte{}, err
	}
	if intent.Target == "" {
		return [32]byte{}, nil
	}
	if _, err := peer.IDFromBytes([]byte(intent.Target)); err != nil || intent.Target.String() == state.HostPeerID {
		return [32]byte{}, irvioruntime.ErrInvalidConfig
	}
	for _, target := range state.Targets {
		if target.PeerID != intent.Target.String() {
			continue
		}
		decoded, err := hex.DecodeString(target.Locator)
		if err != nil || len(decoded) != 32 {
			return [32]byte{}, irvioruntime.ErrInvalidConfig
		}
		var locator [32]byte
		copy(locator[:], decoded)
		if locator == ([32]byte{}) {
			return locator, irvioruntime.ErrInvalidConfig
		}
		return locator, nil
	}
	return [32]byte{}, fmt.Errorf("private target is not saved: %w", irvioruntime.ErrInvalidConfig)
}

func resolvePrivateProductDaemon(options productOptions, addresses []string, store *privateaccess.Store, intent privateProductIntent) (irvioruntime.DaemonConfig, error) {
	if options.role != productruntime.Desktop {
		return irvioruntime.DaemonConfig{}, irvioruntime.ErrInvalidConfig
	}
	if _, err := privateIntentLocator(store, intent); err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	base, err := resolveProductDaemon(options, addresses, "")
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	return applyPrivateProductIntent(base, store, intent)
}

func applyPrivateProductIntent(base irvioruntime.DaemonConfig, store *privateaccess.Store, intent privateProductIntent) (irvioruntime.DaemonConfig, error) {
	if base.Production == nil || !base.M12.Enabled || base.PublicRole.Requested || base.M12.Serve || base.PublicSTUN || base.Connector != nil {
		return irvioruntime.DaemonConfig{}, irvioruntime.ErrInvalidConfig
	}
	locator, err := privateIntentLocator(store, intent)
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	production := *base.Production
	production.ConnectCountry = ""
	production.PrivateTarget, production.PrivateLocator = intent.Target, locator
	base.Production = &production
	base.Exit, base.Country = intent.Hosting, ""
	base.AutoExitCountry, base.RequireExitCountry = false, false
	base.ProtectedMode, base.ProtectAtStart = intent.Target != "", intent.Target != ""
	base.PrivateHosting = nil
	if intent.Hosting {
		base.PrivateHosting = store
	}
	return base, nil
}
