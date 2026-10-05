// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package productintent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const DefaultPath = "/run/irvio/session-state"

type State struct {
	Version         int    `json:"version"`
	ConnectIntent   bool   `json:"connect_intent"`
	Country         string `json:"country,omitempty"`
	TeardownPending bool   `json:"teardown_pending,omitempty"`
}

type Store struct{ Path string }

func (s Store) Read() (State, bool, error) {
	info, err := os.Lstat(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return State{}, false, errors.New("unsafe IRVIO session-state file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return State{}, false, errors.New("IRVIO session-state has unexpected owner")
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return State{}, false, err
	}
	if len(data) > 256 {
		return State{}, false, errors.New("IRVIO session-state is too large")
	}
	var state State
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, false, fmt.Errorf("decode IRVIO session-state: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return State{}, false, errors.New("trailing IRVIO session-state data")
	}
	if state.Version != 1 || state.ConnectIntent == state.TeardownPending ||
		(state.ConnectIntent && !validCountry(state.Country)) ||
		(state.TeardownPending && state.Country != "") {
		return State{}, false, errors.New("invalid IRVIO session-state")
	}
	return state, true, nil
}

func (s Store) Want(country string) error {
	if !validCountry(country) {
		return errors.New("invalid IRVIO country intent")
	}
	return s.write(State{Version: 1, ConnectIntent: true, Country: country})
}

func (s Store) BeginTeardown() error {
	return s.write(State{Version: 1, TeardownPending: true})
}

func (s Store) Clear() error {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s Store) write(state State) error {
	parent := filepath.Dir(s.Path)
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect IRVIO runtime directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("unsafe IRVIO runtime directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("IRVIO runtime directory has unexpected owner")
	}
	if existing, err := os.Lstat(s.Path); err == nil && !existing.Mode().IsRegular() {
		return errors.New("refusing to replace non-file IRVIO session-state")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(parent, ".session-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(state); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.Path)
}

func validCountry(country string) bool {
	if len(country) != 2 {
		return false
	}
	for _, value := range country {
		if value < 'A' || value > 'Z' {
			return false
		}
	}
	return true
}
