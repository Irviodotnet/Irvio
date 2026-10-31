// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"os"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/productprofile"
)

func TestPersistentSharingExplicitStopVersusProcessStop(t *testing.T) {
	d := privateLifecycleController(t)
	if err := d.setPersistentHosting(true); err == nil {
		t.Fatal("enabled without hosting intent")
	}
	d.desiredPrivate = privateProductIntent{Hosting: true}
	if err := d.setPersistentHosting(true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(d.persistentStore().Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe persistent intent")
	}
	if err := d.disconnect(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if enabled, err := d.persistentHosting(); err != nil || !enabled {
		t.Fatalf("process stop lost persistence: %v", err)
	}
	state, present, err := d.persistentStore().Read()
	if err != nil || !present {
		t.Fatal(err)
	}
	if err := d.restoreIntent(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if !d.desiredPrivate.Hosting || d.recoveryPending {
		t.Fatal("hosting restored as protected client")
	}
	if err := d.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if enabled, err := d.persistentHosting(); err != nil || enabled {
		t.Fatalf("explicit stop retained persistence: %v", err)
	}
}

func TestPersistentSharingRejectsOtherModesAndDeletesWithProfile(t *testing.T) {
	d := privateLifecycleController(t)
	p, err := productprofile.Open(d.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	d.desiredPrivate = privateProductIntent{Hosting: true, Target: privateLifecyclePeer(t)}
	if err := d.setPersistentHosting(true); err == nil {
		t.Fatal("accepted private client")
	}
	d.desiredPrivate = privateProductIntent{Hosting: true}
	if err := d.setPersistentHosting(true); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteContents(); err != nil {
		t.Fatal(err)
	}
	if enabled, err := d.persistentHosting(); err != nil || enabled {
		t.Fatal("profile reset retained persistence")
	}
}

func TestPersistentSharingRejectsInvalidDurableIntent(t *testing.T) {
	d := privateLifecycleController(t)
	if err := d.persistentStore().Want("DE"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.persistentHosting(); err == nil {
		t.Fatal("accepted persistent public VPN intent")
	}
}
