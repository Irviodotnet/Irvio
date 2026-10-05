// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	PublisherStateFileName = "announcement-incarnation"
	publisherStateSize     = 16
)

var publisherStateMagic = [8]byte{'H', 'Y', 'D', 'R', 'A', 'A', 'N', '1'}

// PublisherState exclusively owns publishing and durably advances incarnation before emission.
type PublisherState struct {
	file        *os.File
	incarnation uint64
	once        sync.Once
	closeErr    error
}

func AcquirePublisherState(dataDir string) (*PublisherState, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("announcement state data directory is empty")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create announcement state directory: %w", err)
	}
	path := filepath.Join(dataDir, PublisherStateFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open announcement state: %w", err)
	}
	fail := func(cause error) (*PublisherState, error) {
		_ = file.Close()
		return nil, cause
	}
	info, err := file.Stat()
	if err != nil {
		return fail(fmt.Errorf("inspect announcement state: %w", err))
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fail(fmt.Errorf("announcement state must be a 0600 regular file"))
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(fmt.Errorf("acquire announcement publisher ownership: %w", err))
	}
	var current uint64
	switch info.Size() {
	case 0:
	case publisherStateSize:
		var payload [publisherStateSize]byte
		if _, err := file.ReadAt(payload[:], 0); err != nil && !errors.Is(err, io.EOF) {
			return fail(fmt.Errorf("read announcement state: %w", err))
		}
		if string(payload[:8]) != string(publisherStateMagic[:]) {
			return fail(fmt.Errorf("invalid announcement state magic"))
		}
		current = binary.BigEndian.Uint64(payload[8:])
	default:
		return fail(fmt.Errorf("announcement state size %d is invalid", info.Size()))
	}
	if current == ^uint64(0) {
		return fail(fmt.Errorf("announcement incarnation exhausted"))
	}
	next := current + 1
	var payload [publisherStateSize]byte
	copy(payload[:8], publisherStateMagic[:])
	binary.BigEndian.PutUint64(payload[8:], next)
	if _, err := file.WriteAt(payload[:], 0); err != nil {
		return fail(fmt.Errorf("write announcement state: %w", err))
	}
	if err := file.Truncate(publisherStateSize); err != nil {
		return fail(fmt.Errorf("truncate announcement state: %w", err))
	}
	if err := file.Sync(); err != nil {
		return fail(fmt.Errorf("sync announcement state: %w", err))
	}
	directory, err := os.Open(dataDir)
	if err != nil {
		return fail(fmt.Errorf("open announcement state directory: %w", err))
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fail(fmt.Errorf("sync announcement state directory: %w", err))
	}
	return &PublisherState{file: file, incarnation: next}, nil
}

func (s *PublisherState) Incarnation() uint64 {
	if s == nil {
		return 0
	}
	return s.incarnation
}

func (s *PublisherState) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		unlockErr := unix.Flock(int(s.file.Fd()), unix.LOCK_UN)
		s.closeErr = errors.Join(unlockErr, s.file.Close())
	})
	return s.closeErr
}
