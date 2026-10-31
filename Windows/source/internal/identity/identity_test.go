// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package identity

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoadOrCreatePersistsEd25519Identity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	first, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created {
		t.Fatal("first identity was not marked created")
	}
	assertIdentityPermissions(t, dir)

	second, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created {
		t.Fatal("reloaded identity was marked created")
	}
	if first.PeerID != second.PeerID {
		t.Fatalf("PeerID changed: %s != %s", first.PeerID, second.PeerID)
	}
}

func TestLoadOrCreateRejectsCorruptIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	if _, err := LoadOrCreate(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(dir); err == nil {
		t.Fatal("corrupt identity was accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "corrupt" {
		t.Fatal("corrupt identity was silently replaced")
	}
}

func TestConcurrentLoadOrCreateKeepsOneIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	const count = 16
	identities := make([]*Identity, count)
	errors := make([]error, count)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			identities[index], errors[index] = LoadOrCreate(dir)
		}()
	}
	close(start)
	group.Wait()
	created := 0
	for index := range count {
		if errors[index] != nil {
			t.Fatalf("creator %d: %v", index, errors[index])
		}
		if identities[index].PeerID != identities[0].PeerID {
			t.Fatal("concurrent callers received different identities")
		}
		if identities[index].Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created=%d, want one", created)
	}
}

func TestLoadOrCreateRejectsLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("not a key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(dir); err == nil {
		t.Fatal("identity with loose permissions was accepted")
	}
}
