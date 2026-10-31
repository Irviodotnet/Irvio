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
	"testing"
)

func TestInspectLockedDirectoryRefusesMissingLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	d, err := OpenDirectory(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	called := false
	read := func(*Directory) error { called = true; return nil }
	if err := InspectLockedDirectory(path, "missing.lock", read); err == nil || called {
		t.Fatal("unlocked existing state accepted", err)
	}
	if _, err := os.Stat(filepath.Join(path, "missing.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inspection created lock", err)
	}
	if err := InspectLockedDirectory(path, "../escape", read); !errors.Is(err, ErrUnsafe) {
		t.Fatal("unsafe lock name", err)
	}
	if err := InspectLockedDirectory(path, "missing.lock", nil); !errors.Is(err, ErrUnsafe) {
		t.Fatal("nil callback", err)
	}
	f, err := d.OpenFile("valid.lock", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("inspection fixture error")
	if err := InspectLockedDirectory(path, "valid.lock", func(*Directory) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if err := InspectLockedDirectory(path, "valid.lock", read); err != nil || !called {
		t.Fatal("failed callback leaked lock", err)
	}
}
