// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/productprofile"
)

func privateTestDirectory(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	must(t, os.Chmod(path, 0o700))
	return path
}

func TestUnsafeStorageAndIdentityBinding(t *testing.T) {
	s, dir, host := fixture(t)
	if other, err := Open(dir, host); err == nil {
		other.Close()
		t.Fatal("two writers allowed")
	}
	must(t, s.Close())
	if other, err := Open(dir, identity(t)); err == nil {
		other.Close()
		t.Fatal("host identity mismatch allowed")
	}
	path := filepath.Join(dir, Filename)
	must(t, os.Chmod(path, 0o644))
	if other, err := Open(dir, host); err == nil {
		other.Close()
		t.Fatal("world-readable secret accepted")
	}
	must(t, os.Chmod(path, 0o600))
	must(t, os.Rename(path, path+".real"))
	must(t, os.Symlink(path+".real", path))
	if other, err := Open(dir, host); err == nil {
		other.Close()
		t.Fatal("symlink accepted")
	}
}

func TestProfileDeletion(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Chmod(dir, 0o700))
	profile, err := productprofile.Open(dir)
	must(t, err)
	s, err := Open(dir, identity(t))
	must(t, err)
	_, err = s.CreateInvitation(time.Now(), time.Hour)
	must(t, err)
	must(t, s.Close())
	must(t, os.WriteFile(filepath.Join(dir, ".private-access-v1-interrupted"), []byte("secret"), 0o600))
	must(t, profile.DeleteContents())
	entries, err := os.ReadDir(dir)
	must(t, err)
	if len(entries) != 0 {
		t.Fatal("private profile residue")
	}
	if _, err := s.Snapshot(); !errors.Is(err, ErrClosed) {
		t.Fatal("closed store reusable")
	}
}
