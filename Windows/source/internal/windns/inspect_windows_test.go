// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windns

import (
	"bytes"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestInactiveJournalInspection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	if err := CheckInactiveJournal(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inspection created absent state", err)
	}
	j, err := openJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err := j.Write(record{Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := CheckInactiveJournal(path); err == nil {
		t.Fatal("accepted live owner")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CheckInactiveJournal(path); err != nil {
		t.Fatal(err)
	}

	j, err = openJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Write(record{Version: 1, Active: true, Original: Snapshot{Interface: windows.GUID{Data1: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(path, "dns-state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckInactiveJournal(path); err == nil {
		t.Fatal("accepted unresolved state")
	}
	after, err := os.ReadFile(state)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("inspection changed active journal", err)
	}
	if err := os.WriteFile(state, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckInactiveJournal(path); err == nil {
		t.Fatal("accepted corrupt journal")
	}
	after, err = os.ReadFile(state)
	if err != nil || string(after) != "corrupt" {
		t.Fatal("inspection destroyed forensic state", err)
	}
}
