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
	"testing"

	"github.com/Irviodotnet/Irvio/internal/winfile"
	"golang.org/x/sys/windows"
)

func assertIdentityPermissions(t *testing.T, path string) {
	t.Helper()
	directory, err := winfile.OpenDirectory(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	file, err := directory.OpenFile(FileName, false, false)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}

func TestWindowsIdentityRejectsPublicACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	if _, err := LoadOrCreate(path); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(path, FileName)
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(keyPath, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(path); !errors.Is(err, winfile.ErrUnsafe) {
		t.Fatalf("public key ACL accepted: %v", err)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("unsafe identity was overwritten: %v", err)
	}
}
