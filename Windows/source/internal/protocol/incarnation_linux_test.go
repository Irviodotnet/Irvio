// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package protocol

import (
	"os"
	"path/filepath"
	"testing"
)

func assertPublisherPermissions(t *testing.T, directory string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(directory, PublisherStateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%04o", info.Mode().Perm())
	}
}
