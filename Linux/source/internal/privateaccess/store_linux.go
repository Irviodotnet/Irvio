// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package privateaccess

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/sys/unix"
)

type storeStorage struct {
	dir  int
	lock int
}

// Open requires an existing owned profile directory and holds an exclusive writer lock until Close.
func Open(directory string, host peer.ID) (*Store, error) {
	if !validPeer(host.String()) {
		return nil, ErrInvalid
	}
	dir, err := unix.Open(directory, unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	s := &Store{storeStorage: storeStorage{dir: dir, lock: -1}}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	var stat unix.Stat_t
	if err := unix.Fstat(dir, &stat); err != nil {
		return nil, err
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o022 != 0 {
		return nil, ErrInvalid
	}
	s.lock, err = unix.Openat(dir, LockFilename, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	if err := secureFile(s.lock); err != nil {
		return nil, err
	}
	if err := unix.Flock(s.lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(dir, Filename, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		s.state, err = newState(host)
		if err == nil {
			err = s.persist(s.state)
		}
	} else if err == nil {
		file := os.NewFile(uintptr(fd), Filename)
		err = secureFile(fd)
		var data []byte
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(file, MaxBytes+1))
		}
		file.Close()
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

func secureFile(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return ErrInvalid
	}
	return nil
}

func (s *Store) persist(state State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > MaxBytes {
		return ErrCapacity
	}
	suffix, err := randomHex(16)
	if err != nil {
		return err
	}
	name := ".private-access-v1-" + suffix
	fd, err := unix.Openat(s.dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(s.dir, name, 0)
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(s.dir, name, s.dir, Filename); err != nil {
		return err
	}
	return unix.Fsync(s.dir)
}

// Close must precede profile deletion; a live store must never be reused after deletion.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.lock >= 0 {
		err = unix.Close(s.lock)
	}
	return errors.Join(err, unix.Close(s.dir))
}
