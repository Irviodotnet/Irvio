// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package protocol

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openPublisherFile(dataDir string) (*os.File, func() error, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create announcement state directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dataDir, PublisherStateFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0o600) {
		err = fmt.Errorf("announcement state must be a 0600 regular file")
	}
	if err == nil {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, func() error {
		return errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
	}, nil
}

func syncPublisherFile(file *os.File, dataDir string) error {
	if err := file.Sync(); err != nil {
		return err
	}
	directory, err := os.Open(dataDir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
