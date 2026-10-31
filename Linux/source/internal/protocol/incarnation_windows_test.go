// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package protocol

import (
	"testing"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

func assertPublisherPermissions(t *testing.T, path string) {
	t.Helper()
	directory, err := winfile.OpenDirectory(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	file, err := directory.OpenFile(PublisherStateFileName, false, false)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}
