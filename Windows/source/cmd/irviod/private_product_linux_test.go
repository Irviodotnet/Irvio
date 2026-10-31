// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"encoding/hex"
	"os"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPrivateProductIntentResolution(t *testing.T) {
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	identity := func() peer.ID {
		key, _, err := crypto.GenerateEd25519Key(nil)
		must(err)
		id, err := peer.IDFromPrivateKey(key)
		must(err)
		return id
	}
	local, remote, unknown := identity(), identity(), identity()
	directory := t.TempDir()
	must(os.Chmod(directory, 0700))
	store, err := privateaccess.Open(directory, local)
	must(err)
	defer store.Close()
	locator := [32]byte{17}
	must(store.SaveTarget(privateaccess.Target{PeerID: remote.String(), Name: "friend", Locator: hex.EncodeToString(locator[:])}))
	base := irvioruntime.DaemonConfig{DataDir: directory, Exit: true, Country: "NL", ProtectedMode: true, ProtectAtStart: true,
		AutoExitCountry: true, RequireExitCountry: true, Production: &irvioruntime.ProductionConfig{ConnectCountry: "DE"},
		M12: irvioruntime.M12Config{Enabled: true, SessionControlPort: 51822}}
	for _, intent := range []privateProductIntent{{Hosting: true}, {Target: remote}, {Target: remote, Hosting: true}} {
		resolved, err := applyPrivateProductIntent(base, store, intent)
		must(err)
		_, err = irvioruntime.NewDaemon(resolved)
		must(err)
		if resolved.Exit != intent.Hosting || (resolved.PrivateHosting != nil) != intent.Hosting || resolved.Country != "" || resolved.AutoExitCountry || resolved.RequireExitCountry {
			t.Fatal("private mode retained public Exit intent")
		}
		if resolved.ProtectedMode != (intent.Target != "") || resolved.ProtectAtStart != resolved.ProtectedMode || resolved.Production.ConnectCountry != "" || resolved.Production.PrivateTarget != intent.Target {
			t.Fatal("incorrect private upstream protection or target")
		}
		if intent.Target != "" && resolved.Production.PrivateLocator != locator {
			t.Fatal("saved locator lost")
		}
		if base.Production.ConnectCountry != "DE" || base.Production.PrivateTarget != "" {
			t.Fatal("resolver mutated public configuration")
		}
	}
	for _, intent := range []privateProductIntent{{}, {Target: local}, {Target: unknown}, {Target: "invalid"}} {
		if _, err := applyPrivateProductIntent(base, store, intent); err == nil {
			t.Fatal("invalid or unsaved target accepted")
		}
	}
	server := base
	server.PublicRole.Requested = true
	if _, err := applyPrivateProductIntent(server, store, privateProductIntent{Hosting: true}); err == nil {
		t.Fatal("server silently converted into private desktop")
	}
	must(store.RemoveTarget(remote))
	if _, err := applyPrivateProductIntent(base, store, privateProductIntent{Target: remote}); err == nil {
		t.Fatal("removed target remained connectable")
	}
}
