// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

type controlFileJournal struct {
	dir  *winfile.Directory
	lock *os.File
}

func openControlJournal(path string) (*controlFileJournal, error) {
	d, err := winfile.OpenDirectory(path, true)
	if err != nil {
		return nil, err
	}
	f, err := d.OpenFile("control-firewall.lock", true, true)
	if err != nil {
		return nil, errors.Join(err, d.Close())
	}
	if err := winfile.Lock(f); err != nil {
		return nil, errors.Join(err, f.Close(), d.Close())
	}
	return &controlFileJournal{dir: d, lock: f}, nil
}

func (j *controlFileJournal) Read() (controlRecord, error) {
	f, err := j.dir.OpenFile("control-firewall.json", false, false)
	if errors.Is(err, os.ErrNotExist) {
		return controlRecord{Version: 1}, nil
	}
	if err != nil {
		return controlRecord{}, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	err = errors.Join(err, f.Close())
	if err != nil {
		return controlRecord{}, err
	}
	if len(b) > 16384 {
		return controlRecord{}, fmt.Errorf("control firewall journal too large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var r controlRecord
	if err := d.Decode(&r); err != nil {
		return controlRecord{}, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return controlRecord{}, fmt.Errorf("trailing control firewall journal data")
	}
	return r, validateControlRecord(r)
}

func (j *controlFileJournal) Write(r controlRecord) error {
	if err := validateControlRecord(r); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return j.dir.WriteAtomic("control-firewall.json", b, true)
}

func (j *controlFileJournal) Close() error {
	return errors.Join(winfile.Unlock(j.lock), j.lock.Close(), j.dir.Close())
}
