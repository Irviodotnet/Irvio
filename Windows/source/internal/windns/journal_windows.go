// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

type fileJournal struct {
	dir  *winfile.Directory
	lock *os.File
}

func openJournal(path string) (*fileJournal, error) {
	d, err := winfile.OpenDirectory(path, true)
	if err != nil {
		return nil, err
	}
	f, err := d.OpenFile("dns.lock", true, true)
	if err != nil {
		return nil, errors.Join(err, d.Close())
	}
	if err := winfile.Lock(f); err != nil {
		return nil, errors.Join(err, f.Close(), d.Close())
	}
	return &fileJournal{dir: d, lock: f}, nil
}

func (j *fileJournal) Read() (record, error) {
	f, err := j.dir.OpenFile("dns-state.json", false, false)
	if errors.Is(err, os.ErrNotExist) {
		return record{Version: 1}, nil
	}
	if err != nil {
		return record{}, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 131073))
	err = errors.Join(err, f.Close())
	if err != nil {
		return record{}, err
	}
	if len(b) > 131072 {
		return record{}, fmt.Errorf("DNS journal too large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var r record
	if err := d.Decode(&r); err != nil {
		return record{}, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return record{}, fmt.Errorf("trailing DNS journal data")
	}
	return r, validateRecord(r)
}

func (j *fileJournal) Write(r record) error {
	if err := validateRecord(r); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return j.dir.WriteAtomic("dns-state.json", b, true)
}

func (j *fileJournal) Close() error {
	return errors.Join(winfile.Unlock(j.lock), j.lock.Close(), j.dir.Close())
}
