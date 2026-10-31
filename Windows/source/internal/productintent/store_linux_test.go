// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package productintent

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestSameBootIntentLifecycle(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := Store{Path: filepath.Join(dir, "session-state")}
	if _, present, err := store.Read(); err != nil || present {
		t.Fatalf("empty intent: present=%t err=%v", present, err)
	}
	if err := store.Want("NL"); err != nil {
		t.Fatal(err)
	}
	state, present, err := store.Read()
	if err != nil || !present || !state.ConnectIntent || state.Country != "NL" {
		t.Fatalf("connected intent: %+v present=%t err=%v", state, present, err)
	}
	if err := store.BeginTeardown(); err != nil {
		t.Fatal(err)
	}
	state, present, err = store.Read()
	if err != nil || !present || !state.TeardownPending || state.ConnectIntent {
		t.Fatalf("teardown intent: %+v present=%t err=%v", state, present, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, present, err := store.Read(); err != nil || present {
		t.Fatalf("cleared intent: present=%t err=%v", present, err)
	}
}

func TestPrivateIntentLifecycle(t *testing.T) {
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		target  peer.ID
		hosting bool
	}{
		{"upstream", id, false},
		{"hosting", "", true},
		{"combined", id, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			store := Store{Path: filepath.Join(dir, "session-state")}
			if err := store.WantPrivate(test.target, test.hosting); err != nil {
				t.Fatal(err)
			}
			state, present, err := (Store{Path: store.Path}).Read()
			wantTarget := ""
			if test.target != "" {
				wantTarget = test.target.String()
			}
			if err != nil || !present || state.Version != 2 || state.PrivateTarget != wantTarget ||
				state.PrivateHosting != test.hosting || state.ConnectIntent != (test.target != "") || state.Country != "" {
				t.Fatalf("restored private intent: %+v present=%t err=%v", state, present, err)
			}
			if err := store.BeginTeardown(); err != nil {
				t.Fatal(err)
			}
			state, present, err = store.Read()
			if err != nil || !present || !state.TeardownPending || state.PrivateTarget != "" || state.PrivateHosting || state.ConnectIntent {
				t.Fatalf("private intent survived teardown: %+v %v", state, err)
			}
			if err := store.Clear(); err != nil {
				t.Fatal(err)
			}
			if _, present, err := store.Read(); err != nil || present {
				t.Fatalf("clear: %t %v", present, err)
			}
		})
	}
}

func TestPrivateIntentInvalidStateDoesNotReplaceIntent(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := Store{Path: filepath.Join(dir, "session-state")}
	if err := store.Want("NL"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []peer.ID{"", "not-a-multihash"} {
		if err := store.WantPrivate(target, false); err == nil {
			t.Fatal("accepted invalid private intent")
		}
	}
	state, present, err := store.Read()
	if err != nil || !present || state.Country != "NL" {
		t.Fatalf("lost prior intent: %+v %v", state, err)
	}
}

func TestIntentRejectsConflictingVersionsAndModes(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := Store{Path: filepath.Join(dir, "session-state")}
	for _, state := range []State{
		{Version: 3, PrivateHosting: true},
		{Version: 1, PrivateHosting: true},
		{Version: 1, ConnectIntent: true, Country: "NL", PrivateHosting: true},
		{Version: 2},
		{Version: 2, ConnectIntent: true, PrivateHosting: true},
		{Version: 2, PrivateTarget: "invalid", ConnectIntent: true},
		{Version: 2, Country: "NL", PrivateHosting: true},
		{Version: 2, TeardownPending: true, PrivateHosting: true},
		{Version: 2, TeardownPending: true, ConnectIntent: true},
	} {
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store.Path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Read(); err == nil {
			t.Fatalf("accepted %+v", state)
		}
	}
}

func TestIntentRejectsUnsafeFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := Store{Path: filepath.Join(dir, "session-state")}
	if err := os.Symlink("/dev/null", store.Path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Read(); err == nil {
		t.Fatal("accepted symlinked intent")
	}
	if err := store.Want("NL"); err == nil {
		t.Fatal("replaced symlinked intent")
	}
}
