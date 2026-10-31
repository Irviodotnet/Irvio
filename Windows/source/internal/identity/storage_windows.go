// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package identity

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Irviodotnet/Irvio/internal/winfile"
	"golang.org/x/sys/windows"
)

func ensureDataDirectory(path string) error {
	directory, err := winfile.OpenDirectory(path, true)
	if err != nil {
		return err
	}
	return directory.Close()
}

func openIdentityFile(path string) (*os.File, func() error, error) {
	directory, err := winfile.OpenDirectory(filepath.Dir(path), false)
	if err != nil {
		return nil, nil, err
	}
	file, err := directory.OpenFile(filepath.Base(path), false, false)
	// Atomic publication can briefly retain the writer's DELETE handle after rename.
	for deadline := time.Now().Add(250 * time.Millisecond); errors.Is(err, windows.ERROR_SHARING_VIOLATION) && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
		file, err = directory.OpenFile(filepath.Base(path), false, false)
	}
	if err != nil {
		directory.Close()
		return nil, nil, err
	}
	return file, func() error { return errors.Join(file.Close(), directory.Close()) }, nil
}

func persist(path string, encoded []byte) error {
	directory, err := winfile.OpenDirectory(filepath.Dir(path), false)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.WriteAtomic(filepath.Base(path), encoded, false)
}
