// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windivert

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winfile"
	"golang.org/x/sys/windows"
)

func TestPayloadRejectsMissingAndModifiedFiles(t *testing.T) {
	if _, err := verifyPayload(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, ErrPayload) {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "bad")
	d, err := winfile.OpenDirectory(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.WriteAtomic("WinDivert.dll", []byte("not executable"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPayload(dir); !errors.Is(err, ErrPayload) {
		t.Fatal("accepted altered DLL", err)
	}
}

func TestNativePinnedDriverModule(t *testing.T) {
	mode := os.Getenv("IRVIO_DIVERT_TEST_SERVICE")
	if mode == "" {
		t.Skip("requires disposable VM payload and explicit service state")
	}
	if mode != "absent" && mode != "running" {
		t.Fatal("invalid service fixture mode")
	}
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := Load(); err != nil || again != m {
		t.Fatal("module must remain pinned", err)
	}
	for _, file := range m.payload.files {
		if w, err := os.OpenFile(file.Name(), os.O_WRONLY, 0); err == nil {
			w.Close()
			t.Fatal("pinned file can be changed")
		}
	}
	if mode == "absent" {
		if err := m.Probe(); !errors.Is(err, ErrService) {
			t.Fatal("missing service accepted", err)
		}
		return
	}
	wrong := &Module{payload: &payload{path: `C:\not-the-installed-payload`}}
	if err := wrong.checkService(); !errors.Is(err, ErrService) {
		t.Fatal("foreign service path accepted", err)
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	count := func() uint32 {
		var n uint32
		ok, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
		if ok == 0 {
			t.Fatal(err)
		}
		return n
	}
	for i := 0; i < 5; i++ {
		if err := m.Probe(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	before := count()
	for i := 0; i < 100; i++ {
		if err := m.Probe(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	after := count()
	t.Logf("driver_probe_cycles=100 handles before=%d after=%d", before, after)
	if after > before+4 {
		t.Fatal("probe handles grew")
	}
}

func TestNativePayloadAttackMatrix(t *testing.T) {
	if os.Getenv("IRVIO_DIVERT_TEST_SERVICE") == "" {
		t.Skip("requires pinned vendor payload in disposable VM")
	}
	root, err := PackageDirectory()
	if err != nil {
		t.Fatal(err)
	}
	clone := func(t *testing.T) (*winfile.Directory, string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "payload")
		d, err := winfile.OpenDirectory(path, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		for _, name := range []string{"WinDivert.dll", "WinDivert64.sys"} {
			b, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.WriteAtomic(name, b, false); err != nil {
				t.Fatal(err)
			}
		}
		return d, path
	}
	t.Run("valid-and-pinned", func(t *testing.T) {
		d, path := clone(t)
		p, err := verifyPayload(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.WriteAtomic("WinDivert.dll", []byte("replacement"), true); err == nil {
			t.Fatal("verified file replaced")
		}
		if err := verifyDriverSignature(p.files[0]); err == nil {
			t.Fatal("unsigned DLL accepted as signed driver")
		}
		if err := p.close(); err != nil {
			t.Fatal(err)
		}
		if err := d.WriteAtomic("WinDivert.dll", []byte("replacement"), true); err != nil {
			t.Fatal("payload pin not released", err)
		}
	})
	for _, name := range []string{"WinDivert.dll", "WinDivert64.sys"} {
		t.Run("modified-"+name, func(t *testing.T) {
			d, path := clone(t)
			if err := d.WriteAtomic(name, []byte("modified"), true); err != nil {
				t.Fatal(err)
			}
			if p, err := verifyPayload(path); !errors.Is(err, ErrPayload) {
				if p != nil {
					p.close()
				}
				t.Fatal(err)
			}
		})
	}
	t.Run("concurrent-writer", func(t *testing.T) {
		d, path := clone(t)
		f, err := d.OpenFile("WinDivert64.sys", true, false)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if p, err := verifyPayload(path); !errors.Is(err, ErrPayload) {
			if p != nil {
				p.close()
			}
			t.Fatal(err)
		}
	})
	t.Run("unsafe-directory-acl", func(t *testing.T) {
		_, path := clone(t)
		sd, err := windows.SecurityDescriptorFromString("O:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;BU)")
		if err != nil {
			t.Fatal(err)
		}
		acl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			t.Fatal(err)
		}
		if p, err := verifyPayload(path); !errors.Is(err, ErrPayload) {
			if p != nil {
				p.close()
			}
			t.Fatal(err)
		}
	})
}
