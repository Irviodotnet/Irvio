// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Irviodotnet/Irvio/internal/productintent"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
)

func (d *desktopController) persistentStore() productintent.Store {
	return productintent.Store{Path: filepath.Join(d.dataDir, "persistent-sharing-v1.json")}
}

func (d *desktopController) persistentHosting() (bool, error) {
	state, present, err := d.persistentStore().Read()
	if err != nil || !present {
		return false, err
	}
	if !state.PrivateHosting || state.PrivateTarget != "" || state.Country != "" || state.ConnectIntent || state.TeardownPending {
		return false, fmt.Errorf("invalid persistent sharing intent")
	}
	return true, nil
}

func (d *desktopController) setPersistentHosting(enabled bool) error {
	d.lockCommand()
	defer d.mu.Unlock()
	if !enabled {
		return d.clearPersistentHosting()
	}
	if d.closed || d.teardownPending || !d.desiredPrivate.Hosting || d.desiredPrivate.Target != "" || d.desiredCountry != "" {
		return irvioruntime.ErrInvalidState
	}
	if err := d.persistentStore().WantPrivate("", true); err != nil {
		return err
	}
	return d.syncPersistentDirectory()
}

func (d *desktopController) clearPersistentHosting() error {
	store := d.persistentStore()
	if _, err := os.Lstat(store.Path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := store.Clear(); err != nil {
		return err
	}
	return d.syncPersistentDirectory()
}

func (d *desktopController) syncPersistentDirectory() error {
	dir, err := os.Open(d.dataDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
