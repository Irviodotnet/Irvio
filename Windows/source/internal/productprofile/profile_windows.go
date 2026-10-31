// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productprofile

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

// Profile retains ownership identity without keeping an idle directory handle open.
type Profile struct {
	path string
	id   winfile.Identity
}

func Open(path string) (_ *Profile, resultErr error) {
	if path == "" {
		return nil, ErrInvalidPath
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.Join(ErrInvalidPath, err)
	}
	directory, err := winfile.OpenDirectory(absolute, true)
	if err != nil {
		return nil, errors.Join(ErrInvalidPath, err)
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	id, err := directory.Identity()
	if err != nil {
		return nil, err
	}
	err = validateWindowsMarker(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := validateWindowsLegacy(directory); err != nil {
			return nil, err
		}
		if err := directory.WriteAtomic(MarkerFilename, []byte(markerContents), false); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		err = validateWindowsMarker(directory)
	}
	if err != nil {
		return nil, err
	}
	return &Profile{path: absolute, id: id}, nil
}

func (p *Profile) Path() string {
	if p == nil {
		return ""
	}
	return p.path
}

// DeleteExistingContents never creates or adopts a profile and accepts an already empty directory.
func DeleteExistingContents(path string) (resultErr error) {
	if path == "" {
		return ErrInvalidPath
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return errors.Join(ErrInvalidPath, err)
	}
	directory, err := winfile.OpenDirectory(absolute, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	names, err := directory.Names(4096)
	if err != nil || len(names) == 0 {
		return err
	}
	if err := validateWindowsMarker(directory); err != nil {
		return errors.Join(ErrProfileNotOwned, err)
	}
	id, err := directory.Identity()
	if err != nil {
		return err
	}
	return (&Profile{path: absolute, id: id}).DeleteContents()
}

func validateWindowsMarker(d *winfile.Directory) error {
	file, err := d.OpenFile(MarkerFilename, false, false)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(len(markerContents)+1)))
	closeErr := file.Close()
	if string(data) != markerContents {
		return errors.Join(ErrProfileNotOwned, readErr, closeErr)
	}
	return errors.Join(readErr, closeErr)
}

func windowsProfileEntry(name string) bool {
	if name == "awg-identity.lock" {
		return true
	}
	if knownProfileEntry(name) {
		return true
	}
	suffix, ok := strings.CutPrefix(name, ".irvio-write-")
	if !ok || len(suffix) != 32 {
		return false
	}
	_, err := hex.DecodeString(suffix)
	return err == nil
}

func windowsSQLiteEntry(name string) bool {
	return name == "hydra.db" || name == "hydra.db-wal" || name == "hydra.db-shm" || name == "hydra.db-journal"
}

func validateWindowsLegacy(d *winfile.Directory) error {
	names, err := d.Names(4096)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	if !slices.Contains(names, "identity.key") {
		return ErrProfileNotOwned
	}
	for _, name := range names {
		if !windowsProfileEntry(name) {
			return ErrProfileNotOwned
		}
		file, err := d.OpenFile(name, false, false)
		if err != nil {
			return errors.Join(ErrProfileNotOwned, err)
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}

// ValidateDeletion checks ownership and entry names without locking active database files.
func (p *Profile) ValidateDeletion() (resultErr error) {
	if p == nil || p.path == "" {
		return ErrInvalidPath
	}
	directory, err := winfile.OpenDirectory(p.path, false)
	if err != nil {
		return errors.Join(ErrProfileChanged, err)
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	id, err := directory.Identity()
	if err != nil || id != p.id {
		return errors.Join(ErrProfileChanged, err)
	}
	if err := validateWindowsMarker(directory); err != nil {
		return err
	}
	names, err := directory.Names(4096)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name != MarkerFilename && !windowsProfileEntry(name) {
			return fmt.Errorf("%w: unrecognized entry %q", ErrProfileNotOwned, name)
		}
	}
	return nil
}

// DeleteContents validates and pins every file before deleting anything, with the marker removed last.
func (p *Profile) DeleteContents() (resultErr error) {
	if p == nil || p.path == "" {
		return ErrInvalidPath
	}
	directory, err := winfile.OpenDirectory(p.path, false)
	if err != nil {
		return errors.Join(ErrProfileChanged, err)
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	id, err := directory.Identity()
	if err != nil || id != p.id {
		return errors.Join(ErrProfileChanged, err)
	}
	names, err := directory.Names(4096)
	if err != nil {
		return err
	}
	if !slices.Contains(names, MarkerFilename) {
		return os.ErrNotExist
	}
	for _, name := range names {
		if name != MarkerFilename && !windowsProfileEntry(name) {
			return fmt.Errorf("%w: unrecognized entry %q", ErrProfileNotOwned, name)
		}
	}
	files := make(map[string]*winfile.Removal, len(names))
	defer func() {
		for _, file := range files {
			resultErr = errors.Join(resultErr, file.Close())
		}
	}()
	for _, name := range names {
		open := directory.OpenRemoval
		if windowsSQLiteEntry(name) {
			open = directory.OpenInheritedRemoval
		}
		file, err := open(name)
		if err != nil {
			return fmt.Errorf("%w: cannot exclusively remove %q: %w", ErrProfileNotOwned, name, err)
		}
		files[name] = file
	}
	marker, err := files[MarkerFilename].Read(int64(len(markerContents) + 1))
	if err != nil || string(marker) != markerContents {
		return errors.Join(ErrProfileNotOwned, err)
	}
	current, err := directory.Names(4096)
	if err != nil {
		return err
	}
	slices.Sort(names)
	slices.Sort(current)
	if !slices.Equal(names, current) {
		return ErrProfileChanged
	}
	for _, name := range names {
		if name != MarkerFilename {
			if err := files[name].Remove(); err != nil {
				return fmt.Errorf("remove profile file %q: %w", name, err)
			}
		}
	}
	current, err = directory.Names(4096)
	if err != nil || len(current) != 1 || current[0] != MarkerFilename {
		return errors.Join(ErrProfileChanged, err)
	}
	if err := files[MarkerFilename].Remove(); err != nil {
		return err
	}
	current, err = directory.Names(4096)
	if err != nil || len(current) != 0 {
		return errors.Join(ErrProfileChanged, err)
	}
	return nil
}
