// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productprofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	MarkerFilename = ".hydra-profile-v1"
	// Preserve the v1 ownership marker bytes for profile compatibility across rebranding.
	markerContents = "HYDRA product profile v1\n"
)

var (
	ErrInvalidPath     = errors.New("invalid Irvio profile path")
	ErrProfileNotOwned = errors.New("directory is not an owned Irvio profile")
	ErrProfileChanged  = errors.New("Irvio profile directory changed")
)

// Profile fences destructive operations with an ownership marker and the original device/inode.
type Profile struct {
	path   string
	device uint64
	inode  uint64
}

// Open claims an existing or new data directory as the current Irvio profile.
func Open(path string) (*Profile, error) {
	if path == "" {
		return nil, ErrInvalidPath
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	absolute = filepath.Clean(absolute)
	if absolute == string(filepath.Separator) {
		return nil, ErrInvalidPath
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create Irvio profile directory: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect Irvio profile directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, ErrInvalidPath
	}
	markerErr := validateMarker(absolute)
	if errors.Is(markerErr, os.ErrNotExist) {
		if err := validateLegacyContents(absolute); err != nil {
			return nil, err
		}
		markerErr = ensureMarker(absolute)
	}
	if markerErr != nil {
		return nil, markerErr
	}
	device, inode, err := fileIdentity(info)
	if err != nil {
		return nil, err
	}
	return &Profile{path: absolute, device: device, inode: inode}, nil
}

func (profile *Profile) Path() string {
	if profile == nil {
		return ""
	}
	return profile.path
}

// DeleteContents preserves the profile directory and its service-managed permissions.
func (profile *Profile) DeleteContents() error {
	if profile == nil || profile.path == "" {
		return ErrInvalidPath
	}
	info, err := os.Lstat(profile.path)
	if err != nil {
		return fmt.Errorf("inspect Irvio profile before deletion: %w", err)
	}
	device, inode, err := fileIdentity(info)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		device != profile.device || inode != profile.inode {
		return ErrProfileChanged
	}
	if err := validateMarker(profile.path); err != nil {
		return err
	}
	if err := validateOwnedContents(profile.path); err != nil {
		return err
	}
	entries, err := os.ReadDir(profile.path)
	if err != nil {
		return fmt.Errorf("read Irvio profile directory: %w", err)
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(profile.path, entry.Name())); err != nil {
			return fmt.Errorf("remove Irvio profile entry %q: %w", entry.Name(), err)
		}
	}
	return syncDirectory(profile.path)
}

func ensureMarker(directory string) error {
	temporary, err := os.CreateTemp(directory, ".hydra-profile-marker-")
	if err != nil {
		return fmt.Errorf("create Irvio profile marker: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.WriteString(markerContents)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write Irvio profile marker: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, MarkerFilename)); err != nil {
		return fmt.Errorf("install Irvio profile marker: %w", err)
	}
	return syncDirectory(directory)
}

func validateMarker(directory string) error {
	path := filepath.Join(directory, MarkerFilename)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrProfileNotOwned
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Irvio profile marker: %w", err)
	}
	if string(contents) != markerContents {
		return ErrProfileNotOwned
	}
	return nil
}

func validateLegacyContents(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read Irvio profile directory: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}
	hasIdentity := false
	for _, entry := range entries {
		if entry.Name() == "identity.key" {
			hasIdentity = true
		}
		if err := validateOwnedEntry(entry, false); err != nil {
			return err
		}
	}
	if !hasIdentity {
		return ErrProfileNotOwned
	}
	return nil
}

func validateOwnedContents(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read Irvio profile directory: %w", err)
	}
	for _, entry := range entries {
		if err := validateOwnedEntry(entry, true); err != nil {
			return err
		}
	}
	return nil
}

func validateOwnedEntry(entry os.DirEntry, allowMarker bool) error {
	name := entry.Name()
	if allowMarker && name == MarkerFilename {
		// validateMarker checks the exact contents separately.
	} else if !knownProfileEntry(name) {
		return fmt.Errorf("%w: unrecognized entry %q", ErrProfileNotOwned, name)
	}
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("inspect Irvio profile entry %q: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: unsafe entry %q", ErrProfileNotOwned, name)
	}
	return nil
}

func knownProfileEntry(name string) bool {
	switch name {
	case "identity.key", "awg-identity", "announcement-incarnation",
		"hydra.db", "hydra.db-wal", "hydra.db-shm", "hydra.db-journal",
		"bootstrap-v1.pb", "exit-pool-v1.json", "listen-port-v1.json":
		return true
	}
	for _, prefix := range []string{
		".identity-", ".awg-identity-", ".bootstrap-v1-", ".exit-pool-v1-", ".hydra-profile-marker-",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func fileIdentity(info os.FileInfo) (uint64, uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, ErrInvalidPath
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
