// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package protocol

import (
	"errors"
	"os"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

func openPublisherFile(dataDir string) (*os.File, func() error, error) {
	directory, err := winfile.OpenDirectory(dataDir, true)
	if err != nil {
		return nil, nil, err
	}
	file, err := directory.OpenFile(PublisherStateFileName, true, true)
	if err != nil {
		directory.Close()
		return nil, nil, err
	}
	if err := winfile.Lock(file); err != nil {
		file.Close()
		directory.Close()
		return nil, nil, err
	}
	return file, func() error {
		return errors.Join(winfile.Unlock(file), file.Close(), directory.Close())
	}, nil
}

// The native handle uses FILE_WRITE_THROUGH; Sync flushes the record before publication.
func syncPublisherFile(file *os.File, _ string) error {
	return file.Sync()
}
