// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productbootstrap

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

const StoreFilename = "bootstrap-v1.pb"

func loadStore(path string) (*localv1.BootstrapBundle, error) {
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect bootstrap store: %w", err)
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("bootstrap store shape: %w", ErrInvalidBundle)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open bootstrap store: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat bootstrap store: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() ||
		info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > MaxBundleBytes {
		return nil, fmt.Errorf("bootstrap store shape: %w", ErrInvalidBundle)
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxBundleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bootstrap store: %w", err)
	}
	return DecodeStored(raw)
}

func writeStore(path string, bundle *localv1.BootstrapBundle) error {
	raw, err := EncodeStored(bundle)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create bootstrap store directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("bootstrap store directory is not private")
	}
	if existing, statErr := os.Lstat(path); statErr == nil {
		if !existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bootstrap store path is not a regular owned file")
		}
		stat, ok := existing.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() {
			return fmt.Errorf("bootstrap store path has unsafe ownership")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect bootstrap store: %w", statErr)
	}
	temporary, err := os.CreateTemp(directory, ".bootstrap-v1-*.tmp")
	if err != nil {
		return fmt.Errorf("create bootstrap store temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set bootstrap store permissions: %w", err)
	}
	if _, err := temporary.Write(raw); err != nil {
		return fmt.Errorf("write bootstrap store: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync bootstrap store: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close bootstrap store: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace bootstrap store: %w", err)
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open bootstrap store directory: %w", err)
	}
	defer directoryFile.Close()
	if err := directoryFile.Sync(); err != nil {
		return fmt.Errorf("sync bootstrap store directory: %w", err)
	}
	return nil
}
