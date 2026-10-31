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
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsIdentityPublicationHandle(t *testing.T) {
	for _, transient := range []bool{true, false} {
		name := "permanent"
		if transient {
			name = "transient"
		}
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "profile")
			original, err := LoadOrCreate(dir)
			if err != nil {
				t.Fatal(err)
			}
			path, err := windows.UTF16PtrFromString(filepath.Join(dir, FileName))
			if err != nil {
				t.Fatal(err)
			}
			handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatal(err)
			}
			closed := make(chan error, 1)
			if transient {
				go func() { time.Sleep(40 * time.Millisecond); closed <- windows.CloseHandle(handle) }()
			} else {
				defer windows.CloseHandle(handle)
			}
			start := time.Now()
			loaded, loadErr := LoadOrCreate(dir)
			elapsed := time.Since(start)
			if transient {
				if err := <-closed; err != nil {
					t.Fatal(err)
				}
				if loadErr != nil || loaded == nil || loaded.Created || loaded.PeerID != original.PeerID {
					t.Fatalf("publication read: %+v %v", loaded, loadErr)
				}
			} else {
				if !errors.Is(loadErr, windows.ERROR_SHARING_VIOLATION) || loaded != nil {
					t.Fatalf("occupied key: %+v %v", loaded, loadErr)
				}
			}
			if elapsed > time.Second {
				t.Fatalf("unbounded sharing retry: %s", elapsed)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != FileName {
				t.Fatalf("publication residue: %v %v", entries, err)
			}
		})
	}
}
