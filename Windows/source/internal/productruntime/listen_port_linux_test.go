// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package productruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListenPortPersistsAcrossConnectionGenerations(t *testing.T) {
	directory := t.TempDir()
	first, err := ResolveListenPort(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveListenPort(directory)
	if err != nil || first != second || first < 40000 || first > 59999 {
		t.Fatalf("listen port changed: %d -> %d, err=%v", first, second, err)
	}
	info, err := os.Stat(filepath.Join(directory, ListenPortFilename))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("listen port record permissions: %v, err=%v", info, err)
	}
}

func TestInvalidListenPortRecordFailsClosed(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, ListenPortFilename)
	if err := os.WriteFile(path, []byte(`{"version":1,"tcp_port":22}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveListenPort(directory); err == nil {
		t.Fatal("invalid persisted control port was accepted")
	}
}
