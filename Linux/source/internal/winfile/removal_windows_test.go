// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfile

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDirectoryIdentityAndHandleEnumeration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	d, err := OpenDirectory(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	id, err := d.Identity()
	if err != nil || id == (Identity{}) {
		t.Fatalf("identity: %+v %v", id, err)
	}
	for _, name := range []string{"a", "bb", "ccc"} {
		if err := d.WriteAtomic(name, []byte(name), false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Names(2); err == nil {
		t.Fatal("directory bound ignored")
	}
	for i := 0; i < 3; i++ {
		names, err := d.Names(3)
		slices.Sort(names)
		if err != nil || !slices.Equal(names, []string{"a", "bb", "ccc"}) {
			t.Fatalf("enumeration %d: %v %v", i, names, err)
		}
	}
	other, err := OpenDirectory(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherID, err := other.Identity()
	if err != nil || otherID != id {
		t.Fatalf("identity changed: %+v %v", otherID, err)
	}
}

func TestRemovalPinsFileAndDeletesThroughHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	d, err := OpenDirectory(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.WriteAtomic("state", []byte("value"), false); err != nil {
		t.Fatal(err)
	}
	r, err := d.OpenRemoval("state")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if data, err := r.Read(6); err != nil || string(data) != "value" {
		t.Fatalf("read: %q %v", data, err)
	}
	if err := os.Rename(filepath.Join(path, "state"), filepath.Join(path, "other")); err == nil {
		t.Fatal("pinned file renamed")
	}
	if err := os.WriteFile(filepath.Join(path, "state"), []byte("changed"), 0o600); err == nil {
		t.Fatal("pinned file modified")
	}
	if err := r.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file not deleted: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("closed handle accepted: %v", err)
	}
}

func TestInheritedRemovalRequiresPrivatePinnedParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	d, err := OpenDirectory(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := os.WriteFile(filepath.Join(path, "database"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := d.OpenRemoval("database"); err == nil {
		r.Close()
		t.Fatal("strict removal accepted inherited ACL")
	}
	r, err := d.OpenInheritedRemoval("database")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("closed parent accepted: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(path, "database")); err != nil || string(b) != "data" {
		t.Fatalf("data removed: %v", err)
	}
}
