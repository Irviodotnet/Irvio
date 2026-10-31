// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productprofile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteContentsRemovesOwnedProfileAndPreservesDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	profile, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "identity.key"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "awg-identity"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "listen-port-v1.json"), []byte(`{"version":1,"tcp_port":40755}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := profile.DeleteContents(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries after deletion = %v", entries)
	}
	if _, err := Open(directory); err != nil {
		t.Fatalf("reopen empty profile: %v", err)
	}
}

func TestDeleteContentsRejectsMissingOwnershipMarker(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	profile, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, MarkerFilename)); err != nil {
		t.Fatal(err)
	}
	if err := profile.DeleteContents(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("DeleteContents error = %v", err)
	}
}

func TestValidateDeletionRejectsBackupWithoutRemovingProfile(t *testing.T) {
	directory := t.TempDir()
	profile, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(directory, "bootstrap-v1.before-FI-20261013.pb")
	if err := os.WriteFile(backup, []byte("backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := profile.ValidateDeletion(); !errors.Is(err, ErrProfileNotOwned) {
		t.Fatalf("preflight error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, MarkerFilename)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(backup); err != nil {
		t.Fatal(err)
	}
	if err := profile.ValidateDeletion(); err != nil {
		t.Fatal(err)
	}
	if err := profile.DeleteContents(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteContentsRejectsReplacedDirectory(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "profile")
	profile, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "old-profile")
	if err := os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, MarkerFilename), []byte(markerContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := profile.DeleteContents(); !errors.Is(err, ErrProfileChanged) {
		t.Fatalf("DeleteContents error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, MarkerFilename)); err != nil {
		t.Fatalf("replacement marker removed: %v", err)
	}
}

func TestOpenRejectsRootAndSymlink(t *testing.T) {
	if _, err := Open(string(filepath.Separator)); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Open root error = %v", err)
	}
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "profile-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Open symlink error = %v", err)
	}
}

func TestOpenClaimsOnlyEmptyOrRecognizableLegacyProfile(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if _, err := Open(empty); err != nil {
		t.Fatalf("claim empty profile: %v", err)
	}

	legacy := filepath.Join(t.TempDir(), "legacy")
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "identity.key"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "hydra.db"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(legacy); err != nil {
		t.Fatalf("claim recognizable legacy profile: %v", err)
	}

	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "notes.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(foreign); !errors.Is(err, ErrProfileNotOwned) {
		t.Fatalf("claim foreign directory error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "notes.txt")); err != nil {
		t.Fatalf("foreign file changed: %v", err)
	}
}

func TestOpenPreservesExistingProductProfileMarker(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	const existingMarker = "HYDRA product profile v1\n"
	if err := os.WriteFile(filepath.Join(directory, MarkerFilename), []byte(existingMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "identity.key"), []byte("existing identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory); err != nil {
		t.Fatalf("open existing product profile: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(directory, MarkerFilename))
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != existingMarker {
		t.Fatalf("profile marker changed to %q", marker)
	}
	identity, err := os.ReadFile(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(identity) != "existing identity" {
		t.Fatalf("profile identity changed to %q", identity)
	}
}

func TestDeleteContentsRejectsForeignEntryAfterClaim(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	profile, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "notes.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err = Open(directory)
	if err != nil {
		t.Fatalf("reopen marked profile with foreign file: %v", err)
	}
	if err := profile.DeleteContents(); !errors.Is(err, ErrProfileNotOwned) {
		t.Fatalf("DeleteContents error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "notes.txt")); err != nil {
		t.Fatalf("foreign file removed: %v", err)
	}
}
