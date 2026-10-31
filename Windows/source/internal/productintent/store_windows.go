// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productintent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Irviodotnet/Irvio/internal/winfile"
	"golang.org/x/sys/windows"
)

const sessionFilename = "session-state"
const sessionLock = "session-state.lock"
const maxEnvelopeBytes = maxStateBytes + 256

var ErrPreviousBoot = errors.New("IRVIO intent belongs to a previous Windows service-manager session; owned cleanup required")
var errNoState = errors.New("IRVIO session-state absent")

type Store struct {
	Path    string
	session func() (scmSession, error)
}

type windowsEnvelope struct {
	Version int        `json:"version"`
	Session scmSession `json:"session"`
	Intent  State      `json:"intent"`
}

// DefaultStatePath resolves ProgramData without trusting environment-variable overrides.
func DefaultStatePath() (string, error) {
	root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "IRVIO", "runtime", sessionFilename), nil
}

func (s Store) currentSession() (scmSession, error) {
	query := s.session
	if query == nil {
		query = currentSCMSession
	}
	current, err := query()
	if err != nil {
		return scmSession{}, err
	}
	if !current.valid() {
		return scmSession{}, errors.New("invalid Windows service-manager session")
	}
	return current, nil
}

func (s Store) withDirectory(create bool, operation func(*winfile.Directory) error) (resultErr error) {
	if !filepath.IsAbs(s.Path) || filepath.Base(s.Path) != sessionFilename {
		return errors.New("invalid IRVIO Windows intent path")
	}
	directory, err := winfile.OpenDirectory(filepath.Dir(s.Path), create)
	if !create && errors.Is(err, os.ErrNotExist) {
		return errNoState
	}
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	lock, err := directory.OpenFile(sessionLock, true, true)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	if err := winfile.Lock(lock); err != nil {
		return fmt.Errorf("lock IRVIO intent: %w", err)
	}
	return operation(directory)
}

func readEnvelope(directory *winfile.Directory) (windowsEnvelope, error) {
	file, err := directory.OpenFile(sessionFilename, false, false)
	if err != nil {
		return windowsEnvelope{}, err
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, maxEnvelopeBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return windowsEnvelope{}, err
	}
	if len(payload) > maxEnvelopeBytes {
		return windowsEnvelope{}, errors.New("IRVIO session-state is too large")
	}
	var value windowsEnvelope
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return windowsEnvelope{}, fmt.Errorf("decode IRVIO session-state: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return windowsEnvelope{}, errors.New("trailing IRVIO session-state data")
	}
	if value.Version != 1 || !value.Session.valid() || !value.Intent.valid() {
		return windowsEnvelope{}, errors.New("invalid IRVIO session-state")
	}
	return value, nil
}

func (s Store) Read() (State, bool, error) {
	var result State
	err := s.withDirectory(false, func(directory *winfile.Directory) error {
		value, err := readEnvelope(directory)
		if errors.Is(err, os.ErrNotExist) {
			return errNoState
		}
		if err != nil {
			return err
		}
		current, err := s.currentSession()
		if err != nil {
			return err
		}
		if value.Session != current {
			return ErrPreviousBoot
		}
		result = value.Intent
		return nil
	})
	if errors.Is(err, errNoState) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	return result, true, nil
}

func (s Store) write(state State) error {
	if !state.valid() {
		return errors.New("invalid IRVIO session-state")
	}
	return s.withDirectory(true, func(directory *winfile.Directory) error {
		current, err := s.currentSession()
		if err != nil {
			return err
		}
		previous, err := readEnvelope(directory)
		if err == nil && previous.Session != current {
			return ErrPreviousBoot
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		payload, err := json.Marshal(windowsEnvelope{Version: 1, Session: current, Intent: state})
		if err != nil {
			return err
		}
		if len(payload)+1 > maxEnvelopeBytes {
			return errors.New("IRVIO session-state is too large")
		}
		return directory.WriteAtomic(sessionFilename, append(payload, '\n'), true)
	})
}

// Clear is called only after owned teardown, including cleanup of previous-boot residue.
func (s Store) Clear() error {
	err := s.withDirectory(false, func(directory *winfile.Directory) error {
		removal, err := directory.OpenRemoval(sessionFilename)
		if errors.Is(err, os.ErrNotExist) {
			return errNoState
		}
		if err != nil {
			return err
		}
		defer removal.Close()
		return removal.Remove()
	})
	if errors.Is(err, errNoState) {
		return nil
	}
	return err
}
