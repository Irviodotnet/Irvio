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
	"sync"
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
	release     func() error
}

func AcquirePublisherState(dataDir string) (*PublisherState, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("announcement state data directory is empty")
	}
	file, release, err := openPublisherFile(dataDir)
	if err != nil {
		return nil, fmt.Errorf("open announcement state: %w", err)
	}
	fail := func(cause error) (*PublisherState, error) {
		_ = release()
		return nil, cause
	}
	info, err := file.Stat()
	if err != nil {
		return fail(fmt.Errorf("inspect announcement state: %w", err))
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
	if err := syncPublisherFile(file, dataDir); err != nil {
		return fail(fmt.Errorf("sync announcement state: %w", err))
	}
	return &PublisherState{file: file, incarnation: next, release: release}, nil
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
		s.closeErr = s.release()
	})
	return s.closeErr
}
