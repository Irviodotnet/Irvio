// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/winfile"
	"golang.org/x/sys/windows"
)

func TestWindowsDesktopIdentityRequiresInstallerOwnership(t *testing.T) {
	root := filepath.Join(t.TempDir(), "installed")
	if _, err := readWindowsDesktopIdentity(root); err == nil {
		t.Fatal("missing identity accepted")
	}
	d, err := winfile.OpenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	const sid = "S-1-5-21-123-456-789-1001"
	if err := d.WriteAtomic(WindowsDesktopIdentityFilename, []byte(sid+"\n"), false); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	value, err := readWindowsDesktopIdentity(root)
	if user.User.Sid.String() != "S-1-5-18" {
		if err == nil {
			t.Fatal("user-owned identity accepted")
		}
		return
	}
	if err != nil || value != sid {
		t.Fatalf("installed identity=%q %v", value, err)
	}
	for _, invalid := range []string{"", strings.Repeat("x", 257)} {
		if err := d.WriteAtomic(WindowsDesktopIdentityFilename, []byte(invalid), true); err != nil {
			t.Fatal(err)
		}
		if _, err := readWindowsDesktopIdentity(root); err == nil {
			t.Fatal("invalid length accepted")
		}
	}
}
