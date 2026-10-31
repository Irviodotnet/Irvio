// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build windows

package winfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestServiceACLFixture(t *testing.T) {
	path := os.Getenv("IRVIO_WINFILE_ACL_FIXTURE")
	if path == "" {
		return
	}
	d, err := OpenDirectory(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.WriteAtomic("state", []byte("non-secret ACL fixture"), false); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateDirectoryFilesAndLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	d, err := OpenDirectory(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	f, err := d.OpenFile("state", true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := Lock(f); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("state")); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	second, err := d.OpenFile("state", true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := Lock(second); !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		t.Fatalf("second writer acquired or wrong error: %v", err)
	}
	if err := os.Rename(path, path+"-moved"); err == nil {
		t.Fatal("pinned directory was renamed")
	}
	if err := Unlock(f); err != nil {
		t.Fatal(err)
	}
	if err := Lock(second); err != nil {
		t.Fatal(err)
	}
	if err := Unlock(second); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedOpenCloseDoesNotLeakHandles(t *testing.T) {
	// Exclude GC worker handles and never let finalizers hide missing explicit closes.
	previousGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGC)
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	countHandles := func() uint32 {
		t.Helper()
		var count uint32
		proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
		result, _, err := proc.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
		if result == 0 {
			t.Fatal(err)
		}
		return count
	}
	path := filepath.Join(t.TempDir(), "profile")
	cycle := func() {
		t.Helper()
		d, err := OpenDirectory(path, true)
		if err != nil {
			t.Fatal(err)
		}
		f, err := d.OpenFile("state", true, true)
		if err != nil {
			d.Close()
			t.Fatal(err)
		}
		if err := Lock(f); err != nil {
			f.Close()
			d.Close()
			t.Fatal(err)
		}
		if err := errors.Join(Unlock(f), f.Close(), d.Close()); err != nil {
			t.Fatal(err)
		}
	}
	for range 20 {
		cycle()
	}
	before := countHandles()
	for range 100 {
		cycle()
	}
	after := countHandles()
	t.Logf("handles before=%d after=%d; 100 directory/file/lock cycles", before, after)
	if after > before+4 {
		t.Fatalf("handles grew: %d -> %d", before, after)
	}
}

func TestAtomicCreateReplaceAndCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	d, err := OpenDirectory(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.WriteAtomic("state", []byte("first"), false); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteAtomic("state", []byte("lost race"), false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("create overwrote existing file or unexpected error: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(path, "state")); err != nil || string(got) != "first" {
		t.Fatalf("original contents changed: %q, %v", got, err)
	}
	if err := d.WriteAtomic("state", []byte("second"), true); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(path, "state")); err != nil || string(got) != "second" {
		t.Fatalf("replacement mismatch: %q, %v", got, err)
	}
	f, err := d.OpenFile("state", false, false)
	if err != nil {
		t.Fatalf("replacement lost private ACL: %v", err)
	}
	f.Close()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".irvio-write-") {
			t.Fatalf("temporary file remained: %s", entry.Name())
		}
	}
}

func TestRejectsPermissiveACLWithoutRepair(t *testing.T) {
	for _, descriptor := range []string{"D:P(A;;FA;;;WD)", "D:NO_ACCESS_CONTROL"} {
		t.Run(descriptor, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profile")
			d, err := OpenDirectory(path, true)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			f, err := d.OpenFile("state", true, true)
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
			sd, err := windows.SecurityDescriptorFromString(descriptor)
			if err != nil {
				t.Fatal(err)
			}
			acl, _, err := sd.DACL()
			if err != nil {
				t.Fatal(err)
			}
			filePath := filepath.Join(path, "state")
			if err := windows.SetNamedSecurityInfo(filePath, windows.SE_FILE_OBJECT,
				windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
				t.Fatal(err)
			}
			before, err := windows.GetNamedSecurityInfo(filePath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			if f, err := d.OpenFile("state", true, false); !errors.Is(err, ErrUnsafe) {
				if f != nil {
					f.Close()
				}
				t.Fatalf("unsafe ACL accepted: %v", err)
			}
			after, err := windows.GetNamedSecurityInfo(filePath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil || before.String() != after.String() {
				t.Fatalf("existing ACL modified: %v", err)
			}
		})
	}
}

func TestRejectsUnprotectedDirectory(t *testing.T) {
	if d, err := OpenDirectory(t.TempDir(), false); err == nil {
		d.Close()
		t.Fatal("inherited directory ACL accepted")
	}
}

func TestRejectsLinksAndInvalidNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	d, err := OpenDirectory(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, name := range []string{"..", "../escape", `sub\file`, "state:stream", "state.", "state "} {
		if f, err := d.OpenFile(name, true, true); !errors.Is(err, ErrUnsafe) {
			if f != nil {
				f.Close()
			}
			t.Fatalf("unsafe name %q: %v", name, err)
		}
	}
	f, err := d.OpenFile("state", true, true)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Link(filepath.Join(path, "state"), filepath.Join(path, "alias")); err != nil {
		t.Fatal(err)
	}
	if f, err := d.OpenFile("state", false, false); !errors.Is(err, ErrUnsafe) {
		if f != nil {
			f.Close()
		}
		t.Fatalf("hardlinked state accepted: %v", err)
	}
	if err := d.WriteAtomic("state", []byte("overwrite alias"), true); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("hardlinked state replacement accepted: %v", err)
	}
	if err := os.Symlink(filepath.Join(path, "state"), filepath.Join(path, "state-link")); err != nil {
		t.Fatal(err)
	}
	if f, err := d.OpenFile("state-link", false, false); err == nil {
		f.Close()
		t.Fatal("reparse file accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenDirectory(link, false); err == nil {
		other.Close()
		t.Fatal("reparse directory accepted")
	}
	ancestorLink := filepath.Join(t.TempDir(), "ancestor-link")
	if err := os.Symlink(filepath.Dir(path), ancestorLink); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenDirectory(filepath.Join(ancestorLink, "profile"), false); err == nil {
		other.Close()
		t.Fatal("reparse ancestor accepted")
	}
}
