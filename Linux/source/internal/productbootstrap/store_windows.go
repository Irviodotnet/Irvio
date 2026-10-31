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

	"github.com/Irviodotnet/Irvio/internal/winfile"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func loadStore(path string) (bundle *localv1.BootstrapBundle, resultErr error) {
	directory, err := winfile.OpenDirectory(filepath.Dir(path), false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open bootstrap directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	file, err := directory.OpenFile(filepath.Base(path), false, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open bootstrap store: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	raw, err := io.ReadAll(io.LimitReader(file, MaxBundleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bootstrap store: %w", err)
	}
	return DecodeStored(raw)
}

func writeStore(path string, bundle *localv1.BootstrapBundle) (resultErr error) {
	raw, err := EncodeStored(bundle)
	if err != nil {
		return err
	}
	directory, err := winfile.OpenDirectory(filepath.Dir(path), true)
	if err != nil {
		return fmt.Errorf("open bootstrap directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	if err := directory.WriteAtomic(filepath.Base(path), raw, true); err != nil {
		return fmt.Errorf("replace bootstrap store: %w", err)
	}
	return nil
}
