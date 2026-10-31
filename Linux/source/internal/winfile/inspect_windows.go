// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfile

import (
	"errors"
	"fmt"
	"os"
)

// InspectLockedDirectory reads existing protected state without creating a directory, lock, or journal.
func InspectLockedDirectory(path, lockName string, inspect func(*Directory) error) (result error) {
	if inspect == nil || !validName(lockName) {
		return ErrUnsafe
	}
	d, err := OpenDirectory(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, d.Close()) }()
	f, err := d.OpenFile(lockName, false, false)
	if err != nil {
		return fmt.Errorf("existing journal lock unavailable: %w", err)
	}
	defer func() { result = errors.Join(result, f.Close()) }()
	if err := Lock(f); err != nil {
		return fmt.Errorf("journal is owned by another operation: %w", err)
	}
	defer func() { result = errors.Join(result, Unlock(f)) }()
	return inspect(d)
}
