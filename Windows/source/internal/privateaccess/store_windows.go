// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/libp2p/go-libp2p/core/peer"
)

type storeStorage struct {
	dir  *winfile.Directory
	lock *os.File
}

// Open pins an existing private profile and holds an exclusive writer lock until Close.
func Open(directory string, host peer.ID) (*Store, error) {
	if !validPeer(host.String()) {
		return nil, ErrInvalid
	}
	dir, err := winfile.OpenDirectory(directory, false)
	if err != nil {
		return nil, err
	}
	s := &Store{storeStorage: storeStorage{dir: dir}}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	s.lock, err = dir.OpenFile(LockFilename, true, true)
	if err != nil {
		return nil, err
	}
	if err := winfile.Lock(s.lock); err != nil {
		return nil, err
	}
	file, err := dir.OpenFile(Filename, false, false)
	if errors.Is(err, os.ErrNotExist) {
		s.state, err = newState(host)
		if err == nil {
			err = s.persist(s.state)
		}
	} else if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, MaxBytes+1))
		err = errors.Join(readErr, file.Close())
		if err == nil {
			s.state, err = decode(data)
		}
	}
	if err != nil {
		return nil, err
	}
	if s.state.HostPeerID != host.String() {
		return nil, ErrInvalid
	}
	ok = true
	return s, nil
}

func (s *Store) persist(state State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > MaxBytes {
		return ErrCapacity
	}
	return s.dir.WriteAtomic(Filename, data, true)
}

// Close releases the native lock and directory; Windows rejects profile deletion while they are open.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.lock != nil {
		err = s.lock.Close()
	}
	return errors.Join(err, s.dir.Close())
}
