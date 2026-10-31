// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awgidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityRejectsSymlinkAndLoosePermissions(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, make([]byte, fileSize), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, fileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(directory); err == nil {
		t.Fatal("symlink AWG identity accepted")
	}
	if err := os.Remove(filepath.Join(directory, fileName)); err != nil {
		t.Fatal(err)
	}
	identity, err := LoadOrCreate(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(identity.path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(directory); err == nil {
		t.Fatal("loosely protected AWG identity accepted")
	}
}

func assertIdentityPermissions(t *testing.T, directory string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(directory, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("AWG identity mode = %o", info.Mode().Perm())
	}
}
