// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImmutableFilePinsBytesAndName(t *testing.T) {
	d, err := OpenDirectory(filepath.Join(t.TempDir(), "payload"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.WriteAtomic("module", []byte("pinned"), false); err != nil {
		t.Fatal(err)
	}
	f, err := d.OpenImmutableFile("module")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if w, err := d.OpenFile("module", true, false); err == nil {
		w.Close()
		t.Fatal("writer obtained immutable payload")
	}
	if err := os.Rename(f.Name(), f.Name()+"-moved"); err == nil {
		t.Fatal("immutable payload renamed")
	}
	if err := os.Remove(f.Name()); err == nil {
		t.Fatal("immutable payload removed")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	w, err := d.OpenFile("module", true, false)
	if err != nil {
		t.Fatal("reservation not released", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "..", "../module", "module:stream", `a\module`} {
		if f, err := d.OpenImmutableFile(name); err == nil {
			f.Close()
			t.Fatal("invalid filename", name)
		}
	}
}
