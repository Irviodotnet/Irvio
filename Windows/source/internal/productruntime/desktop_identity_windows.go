// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

const WindowsDesktopIdentityFilename = "desktop-user.sid"

// WindowsLocalAPIUserSID reads installer-owned authorization, never the process environment or profile.
func WindowsLocalAPIUserSID() (string, error) {
	paths, err := WindowsDefaultPaths()
	if err != nil {
		return "", err
	}
	return readWindowsDesktopIdentity(filepath.Dir(paths.Config))
}

func readWindowsDesktopIdentity(root string) (sid string, resultErr error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("desktop authorization directory must be absolute")
	}
	directory, err := winfile.OpenSystemDirectory(root)
	if err != nil {
		return "", fmt.Errorf("open installed desktop authorization directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	file, err := directory.OpenImmutableFile(WindowsDesktopIdentityFilename)
	if err != nil {
		return "", fmt.Errorf("read installed desktop identity; repair the IRVIO installation: %w", err)
	}
	value, readErr := io.ReadAll(io.LimitReader(file, 257))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "", err
	}
	if len(value) > 256 || len(value) == 0 {
		return "", errors.New("invalid installed desktop identity length")
	}
	return strings.TrimSpace(string(value)), nil
}
