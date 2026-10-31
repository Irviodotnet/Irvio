// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"errors"
	"net/netip"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

type memoryControlJournal struct {
	record controlRecord
	fail   error
	closed bool
}

func (j *memoryControlJournal) Read() (controlRecord, error) { return j.record, nil }
func (j *memoryControlJournal) Write(r controlRecord) error {
	if j.fail != nil {
		return j.fail
	}
	j.record = r
	return nil
}
func (j *memoryControlJournal) Close() error { j.closed = true; return nil }

type memoryControlOps struct {
	j                      *memoryControlJournal
	installed              bool
	failInstall, failClose error
	calls                  int
}

func (o *memoryControlOps) Install() error {
	if o.j.record.Rule == nil {
		return errors.New("native mutation without intent")
	}
	o.calls++
	o.installed = true
	return o.failInstall
}
func (o *memoryControlOps) Close() error {
	if o.failClose != nil {
		return o.failClose
	}
	o.installed = false
	return nil
}

func testControlScope() controlScope {
	return controlScope{Owner: windows.GUID{Data1: 1}, Tunnel: windows.GUID{Data1: 2}, Application: `C:\IRVIO\irviod.exe`, Interface: "IRVIO test",
		Endpoint: netip.MustParseAddrPort("10.17.0.1:7890"), Pool: netip.MustParsePrefix("10.17.0.0/24")}
}

func TestOwnedControlWriteBeforeMutationAndRetry(t *testing.T) {
	j := &memoryControlJournal{}
	o := &memoryControlOps{j: j}
	m := &OwnedControl{rule: o, scope: testControlScope(), journal: j}
	boom := errors.New("fixture failure")
	j.fail = boom
	if !errors.Is(m.Install(), boom) || o.calls != 0 {
		t.Fatal("mutation after journal failure")
	}
	j.fail = nil
	o.failInstall = boom
	if !errors.Is(m.Install(), boom) || !o.installed || j.record.Rule == nil {
		t.Fatal("partial install intent lost")
	}
	o.failClose = ErrConflict
	if !errors.Is(m.Close(), ErrConflict) || j.closed || j.record.Rule == nil {
		t.Fatal("failed cleanup lost owner")
	}
	o.failClose = nil
	j.fail = boom
	if !errors.Is(m.Close(), boom) || o.installed || j.closed || j.record.Rule == nil {
		t.Fatal("failed journal clear lost retry")
	}
	j.fail = nil
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if !j.closed || j.record.Rule != nil {
		t.Fatal("cleanup did not clear durable intent")
	}
	if err := m.Close(); err != nil {
		t.Fatal("idempotent close", err)
	}
	if m.Install() == nil {
		t.Fatal("reused closed owner")
	}
}

func TestControlJournalLockValidationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	j, err := openControlJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			j.Close()
		}
	}()
	if other, err := openControlJournal(path); err == nil {
		other.Close()
		t.Fatal("duplicate journal owner")
	}
	scope := testControlScope()
	want := controlRecord{Version: 1, Rule: &scope}
	if err := j.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	j, err = openControlJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	got, err := j.Read()
	if err != nil || got.Rule == nil || *got.Rule != scope {
		t.Fatal("durable scope mismatch", got, err)
	}
	for _, bad := range []string{`{"Version":2}`, `{"Version":1,"surprise":true}`, `{"Version":1} {}`, `{"Version":1,"Rule":{}}`} {
		if err := j.dir.WriteAtomic("control-firewall.json", []byte(bad), true); err != nil {
			t.Fatal(err)
		}
		if _, err := j.Read(); err == nil {
			t.Fatal("accepted invalid journal", bad)
		}
	}
}
