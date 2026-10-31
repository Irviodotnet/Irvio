// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

type routeFileJournal struct {
	dir  *winfile.Directory
	lock *os.File
}

func openRouteJournal(path string) (*routeFileJournal, error) {
	d, err := winfile.OpenDirectory(path, true)
	if err != nil {
		return nil, err
	}
	f, err := d.OpenFile("routes.lock", true, true)
	if err != nil {
		return nil, errors.Join(err, d.Close())
	}
	if err := winfile.Lock(f); err != nil {
		return nil, errors.Join(err, f.Close(), d.Close())
	}
	return &routeFileJournal{dir: d, lock: f}, nil
}

func (j *routeFileJournal) Read() (routeRecord, error) {
	f, err := j.dir.OpenFile("routes-state.json", false, false)
	if errors.Is(err, os.ErrNotExist) {
		return routeRecord{Version: 1}, nil
	}
	if err != nil {
		return routeRecord{}, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	err = errors.Join(err, f.Close())
	if err != nil {
		return routeRecord{}, err
	}
	if len(b) > 16384 {
		return routeRecord{}, fmt.Errorf("route journal too large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var r routeRecord
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return routeRecord{}, fmt.Errorf("trailing route journal data")
	}
	return r, validateRouteRecord(r)
}

func (j *routeFileJournal) Write(r routeRecord) error {
	if err := validateRouteRecord(r); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return j.dir.WriteAtomic("routes-state.json", b, true)
}

func (j *routeFileJournal) Close() error {
	return errors.Join(winfile.Unlock(j.lock), j.lock.Close(), j.dir.Close())
}
