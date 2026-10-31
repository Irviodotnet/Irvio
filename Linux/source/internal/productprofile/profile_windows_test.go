// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productprofile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/peerstore"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"golang.org/x/sys/windows"
)

func TestWindowsDeleteExistingProfileDoesNotCreateOrAdopt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	if err := DeleteExistingContents(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing profile was created: %v", err)
	}
	if err := DeleteExistingContents(""); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("empty path accepted: %v", err)
	}
	p := ownedProfile(t)
	if err := p.DeleteContents(); err != nil {
		t.Fatal(err)
	}
	if err := DeleteExistingContents(p.Path()); err != nil {
		t.Fatal(err)
	}
	privateFiles(t, p.Path(), map[string]string{"identity.key": "legacy profile without owner marker"})
	if err := DeleteExistingContents(p.Path()); !errors.Is(err, ErrProfileNotOwned) {
		t.Fatalf("markerless legacy profile adopted: %v", err)
	}
	preserved(t, p.Path(), "identity.key")
	if _, err := os.Lstat(filepath.Join(p.Path(), MarkerFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker created during erase: %v", err)
	}
}

func TestWindowsDeleteExistingProfileReentry(t *testing.T) {
	p := ownedProfile(t)
	privateFiles(t, p.Path(), map[string]string{"identity.key": "owned identity", "bootstrap-v1.pb": "bootstrap"})
	for range 2 {
		if err := DeleteExistingContents(p.Path()); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(p.Path())
		if err != nil || len(entries) != 0 {
			t.Fatalf("profile not empty: %v %v", entries, err)
		}
	}
}

func TestWindowsDeleteExistingProfilePreservesUnknownAndBusyFiles(t *testing.T) {
	for _, kind := range []string{"unknown", "busy", "marker", "link"} {
		t.Run(kind, func(t *testing.T) {
			p := ownedProfile(t)
			privateFiles(t, p.Path(), map[string]string{"identity.key": "preserved"})
			switch kind {
			case "unknown":
				privateFiles(t, p.Path(), map[string]string{"unowned.txt": "not ours"})
			case "busy":
				f, err := os.Open(filepath.Join(p.Path(), "identity.key"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
			case "marker":
				d, err := winfile.OpenDirectory(p.Path(), false)
				if err != nil {
					t.Fatal(err)
				}
				if err := d.WriteAtomic(MarkerFilename, []byte("foreign marker"), true); err != nil {
					t.Fatal(err)
				}
				d.Close()
			case "link":
				alias := filepath.Join(t.TempDir(), "profile-link")
				if err := os.Symlink(p.Path(), alias); err != nil {
					t.Fatal(err)
				}
				if err := DeleteExistingContents(alias); err == nil {
					t.Fatal("profile link accepted")
				}
				preserved(t, p.Path(), MarkerFilename, "identity.key")
				return
			}
			if err := DeleteExistingContents(p.Path()); !errors.Is(err, ErrProfileNotOwned) {
				t.Fatalf("unsafe profile accepted: %v", err)
			}
			preserved(t, p.Path(), MarkerFilename, "identity.key")
		})
	}
}

func TestWindowsProfileDeletesRealPeerDatabase(t *testing.T) {
	p := ownedProfile(t)
	privateFiles(t, p.Path(), map[string]string{"identity.key": "keep until validated", "bootstrap-v1.pb": "bundle"})
	db, err := peerstore.Open(context.Background(), filepath.Join(p.Path(), "hydra.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteContents(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(p.Path())
	if err != nil || len(entries) != 0 {
		t.Fatalf("profile not empty: %v %v", entries, err)
	}
}

func TestWindowsProfileDeletesInheritedSQLiteFilesOnly(t *testing.T) {
	for _, name := range []string{"hydra.db", "hydra.db-wal", "hydra.db-shm", "hydra.db-journal", "identity.key", "bootstrap-v1.pb"} {
		t.Run(name, func(t *testing.T) {
			p := ownedProfile(t)
			if err := os.WriteFile(filepath.Join(p.Path(), name), []byte("inherited"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := p.DeleteContents()
			if windowsSQLiteEntry(name) {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrProfileNotOwned) {
					t.Fatalf("non-SQLite inherited file accepted: %v", err)
				}
				preserved(t, p.Path(), MarkerFilename, name)
			}
		})
	}
}

func TestWindowsProfileInheritedSQLiteRefusesUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"public", "explicit-unprotected", "hardlink", "symlink", "readonly", "busy"} {
		t.Run(kind, func(t *testing.T) {
			p := ownedProfile(t)
			privateFiles(t, p.Path(), map[string]string{"identity.key": "preserved"})
			path := filepath.Join(p.Path(), "hydra.db")
			if kind == "hardlink" || kind == "symlink" {
				external := filepath.Join(t.TempDir(), "external")
				if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
					t.Fatal(err)
				}
				link := os.Link
				if kind == "symlink" {
					link = os.Symlink
				}
				if err := link(external, path); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if b, err := os.ReadFile(external); err != nil || string(b) != "external" {
						t.Errorf("external data changed: %v", err)
					}
				})
			} else if err := os.WriteFile(path, []byte("database"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "public", "explicit-unprotected":
				sddl := "D:P(A;;FA;;;WD)"
				flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
				if kind == "explicit-unprotected" {
					sddl = "D:(A;;FA;;;SY)"
					flags = windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION
				}
				sd, err := windows.SecurityDescriptorFromString(sddl)
				if err != nil {
					t.Fatal(err)
				}
				acl, _, err := sd.DACL()
				if err != nil {
					t.Fatal(err)
				}
				if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, flags, nil, nil, acl, nil); err != nil {
					t.Fatal(err)
				}
			case "readonly":
				name, _ := windows.UTF16PtrFromString(path)
				if err := windows.SetFileAttributes(name, windows.FILE_ATTRIBUTE_READONLY); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { windows.SetFileAttributes(name, windows.FILE_ATTRIBUTE_NORMAL) })
			case "busy":
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
			}
			if err := p.DeleteContents(); !errors.Is(err, ErrProfileNotOwned) {
				t.Fatalf("unsafe SQLite state accepted: %v", err)
			}
			preserved(t, p.Path(), MarkerFilename, "identity.key", "hydra.db")
		})
	}
}

func privateFiles(t *testing.T, path string, files map[string]string) {
	t.Helper()
	d, err := winfile.OpenDirectory(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for name, value := range files {
		if err := d.WriteAtomic(name, []byte(value), false); err != nil {
			t.Fatal(err)
		}
	}
}

func ownedProfile(t *testing.T) *Profile {
	t.Helper()
	p, err := Open(filepath.Join(t.TempDir(), "profile"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func preserved(t *testing.T, path string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(path, name)); err != nil {
			t.Errorf("file %s was not preserved: %v", name, err)
		}
	}
}

func TestWindowsProfileCreateDeleteAndReopen(t *testing.T) {
	p := ownedProfile(t)
	privateFiles(t, p.Path(), map[string]string{
		"identity.key": "identity", "awg-identity": "awg", "hydra.db": "db",
		"private-access-v1.json": "private", "announcement-incarnation": "1",
		".irvio-write-0123456789abcdef0123456789abcdef": "interrupted write",
	})
	if err := p.DeleteContents(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(p.Path())
	if err != nil || len(entries) != 0 {
		t.Fatalf("profile not empty: %v %v", entries, err)
	}
	reopened, err := Open(p.Path())
	if err != nil || reopened.id != p.id {
		t.Fatalf("directory identity changed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(p.Path(), MarkerFilename))
	if err != nil || string(data) != markerContents {
		t.Fatalf("marker bytes changed: %q %v", data, err)
	}
}

func TestWindowsProfileRejectsForeignAndUnsafeFilesBeforeDeleting(t *testing.T) {
	for _, kind := range []string{"foreign", "directory", "symlink", "hardlink", "readonly", "permissive", "bad-temp"} {
		t.Run(kind, func(t *testing.T) {
			p := ownedProfile(t)
			privateFiles(t, p.Path(), map[string]string{"identity.key": "keep", "awg-identity": "keep"})
			unsafePath := filepath.Join(p.Path(), "bootstrap-v1.pb")
			switch kind {
			case "foreign":
				privateFiles(t, p.Path(), map[string]string{"notes.txt": "keep"})
			case "bad-temp":
				privateFiles(t, p.Path(), map[string]string{".irvio-write-not-a-token": "keep"})
			case "directory":
				if err := os.Mkdir(unsafePath, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink":
				target := filepath.Join(t.TempDir(), "external")
				if err := os.WriteFile(target, []byte("external"), 0o600); err != nil {
					t.Fatal(err)
				}
				link := os.Symlink
				if kind == "hardlink" {
					link = os.Link
				}
				if err := link(target, unsafePath); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if data, err := os.ReadFile(target); err != nil || string(data) != "external" {
						t.Errorf("external file changed: %v", err)
					}
				})
			case "readonly", "permissive":
				privateFiles(t, p.Path(), map[string]string{"bootstrap-v1.pb": "keep"})
				if kind == "readonly" {
					name, _ := windows.UTF16PtrFromString(unsafePath)
					if err := windows.SetFileAttributes(name, windows.FILE_ATTRIBUTE_READONLY); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { windows.SetFileAttributes(name, windows.FILE_ATTRIBUTE_NORMAL) })
				} else {
					sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
					if err != nil {
						t.Fatal(err)
					}
					acl, _, _ := sd.DACL()
					if err := windows.SetNamedSecurityInfo(unsafePath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := p.DeleteContents(); !errors.Is(err, ErrProfileNotOwned) {
				t.Fatalf("unsafe profile accepted or wrong error: %v", err)
			}
			preserved(t, p.Path(), MarkerFilename, "identity.key", "awg-identity")
		})
	}
}

func TestWindowsProfileBusyFileBlocksWholeDeletion(t *testing.T) {
	p := ownedProfile(t)
	privateFiles(t, p.Path(), map[string]string{"identity.key": "keep", "hydra.db": "open database"})
	busy, err := os.Open(filepath.Join(p.Path(), "hydra.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := p.DeleteContents(); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("busy deletion error: %v", err)
	}
	preserved(t, p.Path(), MarkerFilename, "identity.key", "hydra.db")
	if err := busy.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteContents(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsProfileReplacementIsNotDeleted(t *testing.T) {
	p := ownedProfile(t)
	if err := os.Rename(p.Path(), p.Path()+"-old"); err != nil {
		t.Fatal(err)
	}
	replacement, err := Open(p.Path())
	if err != nil {
		t.Fatal(err)
	}
	if replacement.id == p.id {
		t.Fatal("different directories share identity")
	}
	if err := p.DeleteContents(); !errors.Is(err, ErrProfileChanged) {
		t.Fatalf("replacement accepted: %v", err)
	}
	preserved(t, p.Path(), MarkerFilename)
	preserved(t, p.Path()+"-old", MarkerFilename)
}

func TestWindowsProfileMarkerIsRequiredAndExact(t *testing.T) {
	for _, value := range []string{"", "foreign", markerContents + "extra", strings.Repeat("x", 1<<20)} {
		p := ownedProfile(t)
		privateFiles(t, p.Path(), map[string]string{"identity.key": "keep"})
		if err := os.WriteFile(filepath.Join(p.Path(), MarkerFilename), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := p.DeleteContents(); !errors.Is(err, ErrProfileNotOwned) {
			t.Fatalf("marker accepted: %v", err)
		}
		preserved(t, p.Path(), "identity.key", MarkerFilename)
	}
	p := ownedProfile(t)
	if err := os.Remove(filepath.Join(p.Path(), MarkerFilename)); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteContents(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing marker error: %v", err)
	}
}

func TestWindowsProfileRejectsRootReparseAndInheritedACL(t *testing.T) {
	for _, path := range []string{"", `C:\`, `\\server\share\profile`, t.TempDir()} {
		if _, err := Open(path); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("unsafe path %q: %v", path, err)
		}
	}
	p := ownedProfile(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(p.Path(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("reparse path accepted: %v", err)
	}
	preserved(t, p.Path(), MarkerFilename)
}

func TestWindowsProfileClaimsOnlyRecognizablePrivateLegacyState(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "legacy")
	privateFiles(t, legacy, map[string]string{"identity.key": "legacy", "hydra.db": "db"})
	if _, err := Open(legacy); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "foreign")
	privateFiles(t, foreign, map[string]string{"notes.txt": "keep"})
	if _, err := Open(foreign); !errors.Is(err, ErrProfileNotOwned) {
		t.Fatalf("foreign directory accepted: %v", err)
	}
	preserved(t, foreign, "notes.txt")
	if _, err := os.Stat(filepath.Join(foreign, MarkerFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("foreign directory claimed: %v", err)
	}
}

func TestWindowsProfileRepeatedDeletionAndRefusalReleaseHandles(t *testing.T) {
	previousGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGC)
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	path := filepath.Join(t.TempDir(), "profile")
	cycle := func() {
		p, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		privateFiles(t, path, map[string]string{"identity.key": "keep", "hydra.db": "keep"})
		busy, err := os.Open(filepath.Join(path, "hydra.db"))
		if err != nil {
			t.Fatal(err)
		}
		err = p.DeleteContents()
		closeErr := busy.Close()
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) || closeErr != nil {
			t.Fatalf("busy gate: %v %v", err, closeErr)
		}
		preserved(t, path, MarkerFilename, "identity.key", "hydra.db")
		if err := p.DeleteContents(); err != nil {
			t.Fatal(err)
		}
	}
	for range 20 {
		cycle()
	}
	count := func() uint32 {
		var value uint32
		result, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&value)))
		if result == 0 {
			t.Fatal(err)
		}
		return value
	}
	before := count()
	for range 100 {
		cycle()
	}
	after := count()
	t.Logf("100 refused and completed profile deletions: handles %d -> %d", before, after)
	if after > before+4 {
		t.Fatalf("profile handles grew beyond runtime tolerance: %d -> %d", before, after)
	}
}
