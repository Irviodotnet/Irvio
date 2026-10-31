// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productbootstrap

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/productprofile"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"golang.org/x/sys/windows"
	"google.golang.org/protobuf/proto"
)

func windowsStoreBundle(t *testing.T) *localv1.BootstrapBundle {
	t.Helper()
	return &localv1.BootstrapBundle{FormatVersion: BundleVersion,
		Nodes: []*localv1.BootstrapNode{testNode(testPeerID(t), "198.51.100.9", 4000)}}
}

func windowsStoreManager(t *testing.T, path string) *Manager {
	t.Helper()
	m, err := New(Config{DataDir: path, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}

func TestWindowsBootstrapStoreRoundTripCorruptionAndBounds(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "профиль")
	path := filepath.Join(directory, StoreFilename)
	if value, err := loadStore(path); value != nil || err != nil {
		t.Fatalf("missing store: %v %v", value, err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created a directory")
	}
	bundle := windowsStoreBundle(t)
	for _, payload := range [][]byte{nil, []byte("corrupt"), bytes.Repeat([]byte{1}, MaxBundleBytes+1)} {
		if err := writeStore(path, bundle); err != nil {
			t.Fatal(err)
		}
		loaded, err := loadStore(path)
		if err != nil || !proto.Equal(loaded, mustNormalize(t, bundle)) {
			t.Fatalf("roundtrip: %v", err)
		}
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadStore(path); err == nil {
			t.Fatal("malformed payload accepted")
		}
		manager := windowsStoreManager(t, directory)
		if manager.State().Phase != localv1.BootstrapPhase_BOOTSTRAP_PHASE_DEGRADED {
			t.Fatal("corrupt profile not marked degraded")
		}
		code, err := Encode(bundle)
		if err != nil {
			t.Fatal(err)
		}
		state, err := manager.Import(code)
		if err != nil || state.ConfiguredCount != 1 || manager.loadErr != nil {
			t.Fatalf("repair owned corrupt payload: %v %v", state, err)
		}
		loaded, err = loadStore(path)
		if err != nil || !proto.Equal(loaded, mustNormalize(t, bundle)) {
			t.Fatalf("repaired payload did not persist: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeStore(path, nil); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(want, got) {
		t.Fatalf("invalid replacement modified valid bundle: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != StoreFilename {
		t.Fatalf("temporary residue: %v %v", entries, err)
	}
}

func TestWindowsBootstrapStoreRejectsUnsafeFilesAndDirectories(t *testing.T) {
	for _, kind := range []string{"public-file", "public-directory", "inherited-directory", "directory", "symlink", "hardlink", "reparse-directory", "readonly"} {
		t.Run(kind, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "profile")
			path := filepath.Join(directory, StoreFilename)
			bundle := windowsStoreBundle(t)
			if err := writeStore(path, bundle); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			checkPath := path
			switch kind {
			case "public-file", "public-directory":
				target := path
				if kind == "public-directory" {
					target = directory
				}
				sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
				if err != nil {
					t.Fatal(err)
				}
				acl, _, err := sd.DACL()
				if err != nil {
					t.Fatal(err)
				}
				if err := windows.SetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
					t.Fatal(err)
				}
			case "inherited-directory":
				path = filepath.Join(t.TempDir(), StoreFilename)
				if err := os.WriteFile(path, original, 0o600); err != nil {
					t.Fatal(err)
				}
				checkPath = path
			case "directory", "symlink", "hardlink":
				if err := os.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				checkPath = path + "-original"
				if kind == "directory" {
					err = os.Mkdir(path, 0o700)
				} else if kind == "symlink" {
					err = os.Symlink(checkPath, path)
				} else {
					err = os.Link(checkPath, path)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "reparse-directory":
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(directory, alias); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(alias, StoreFilename)
			case "readonly":
				if err := os.Chmod(path, 0o400); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(path, 0o600) })
			}
			if kind != "readonly" {
				if _, err := loadStore(path); err == nil {
					t.Fatal("unsafe store read accepted")
				}
			}
			bundle.GeneratedAtUnixMs = 123
			if err := writeStore(path, bundle); err == nil {
				t.Fatal("unsafe store replaced")
			}
			current, err := os.ReadFile(checkPath)
			if err != nil || !bytes.Equal(original, current) {
				t.Fatalf("unsafe store was modified: %v", err)
			}
		})
	}
}

func TestWindowsBootstrapBusyReplacementKeepsOldBundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile", StoreFilename)
	bundle := windowsStoreBundle(t)
	if err := writeStore(path, bundle); err != nil {
		t.Fatal(err)
	}
	directory, err := winfile.OpenDirectory(filepath.Dir(path), false)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	busy, err := directory.OpenFile(StoreFilename, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	replacement := proto.Clone(bundle).(*localv1.BootstrapBundle)
	replacement.GeneratedAtUnixMs = 123
	if err := writeStore(path, replacement); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("busy replacement: %v", err)
	}
	if err := busy.Close(); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadStore(path)
	if err != nil || !proto.Equal(loaded, mustNormalize(t, bundle)) {
		t.Fatalf("old bundle lost: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed rename left temporary file: %v %v", entries, err)
	}
	if err := writeStore(path, replacement); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsBootstrapImportDeleteCyclesReleaseHandles(t *testing.T) {
	previousGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGC)
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	path := filepath.Join(t.TempDir(), "profile")
	bundle := windowsStoreBundle(t)
	code, err := Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	cycle := func() {
		profile, err := productprofile.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		manager, err := New(Config{DataDir: path, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		if err != nil {
			t.Fatal(err)
		}
		_, err = manager.Import(code)
		manager.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := profile.DeleteContents(); err != nil {
			t.Fatal(err)
		}
		if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
			t.Fatalf("profile residue: %v %v", entries, err)
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
	t.Logf("100 import/delete cycles: handles %d -> %d", before, after)
	if after > before+4 {
		t.Fatalf("bootstrap handles grew beyond runtime tolerance: %d -> %d", before, after)
	}
}
