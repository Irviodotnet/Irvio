// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productbootstrap

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"google.golang.org/protobuf/proto"
)

func TestBootstrapStorePersistsAtomicallyAndRejectsCorruption(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, StoreFilename)
	bundle := &localv1.BootstrapBundle{
		FormatVersion: BundleVersion,
		Nodes:         []*localv1.BootstrapNode{testNode(testPeerID(t), "198.51.100.9", 4001)},
	}
	if err := writeStore(path, bundle); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadStore(path)
	if err != nil || !proto.Equal(mustNormalize(t, bundle), loaded) {
		t.Fatalf("loaded=%v err=%v", loaded, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("store mode=%v", info.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{DataDir: directory, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("corrupt store killed manager: %v", err)
	}
	if state := manager.State(); state.Phase != localv1.BootstrapPhase_BOOTSTRAP_PHASE_DEGRADED {
		t.Fatalf("corrupt state=%v", state)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeStore(path, bundle); err != nil {
		t.Fatalf("replace damaged owned store: %v", err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("stat replacement: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("replacement mode=%v", info.Mode().Perm())
	}
}
