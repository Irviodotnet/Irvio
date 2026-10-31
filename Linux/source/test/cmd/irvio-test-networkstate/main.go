// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Command hydra-test-networkstate observes generations only in disposable test namespaces.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

type options struct {
	stateDirectory string
	stopFile       string
	errorFile      string
	timeout        time.Duration
	debounce       time.Duration
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	options, err := parseOptions()
	if err != nil {
		return err
	}
	if err := os.Mkdir(options.stateDirectory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create state directory: %w", err)
	}
	monitor, err := networkstate.NewMonitor(networkstate.Config{Debounce: options.debounce})
	if err != nil {
		return err
	}
	defer monitor.Close()
	if err := writeState(options.stateDirectory, monitor.Current()); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), options.timeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case state, ok := <-monitor.Changes():
			if !ok {
				return errors.New("network monitor stopped without a terminal error")
			}
			if err := writeState(options.stateDirectory, state); err != nil {
				return err
			}
		case monitorErr, ok := <-monitor.Errors():
			if !ok {
				return errors.New("network monitor error channel closed")
			}
			_ = writeJSON(options.errorFile, map[string]string{"error": monitorErr.Error()})
			return monitorErr
		case <-ticker.C:
			if info, statErr := os.Stat(options.stopFile); statErr == nil && info.Mode().IsRegular() {
				return nil
			} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func parseOptions() (options, error) {
	var value options
	flag.StringVar(&value.stateDirectory, "state-dir", "", "absolute directory for atomic generation snapshots")
	flag.StringVar(&value.stopFile, "stop-file", "", "absolute file used for deterministic shutdown")
	flag.StringVar(&value.errorFile, "error-file", "", "absolute terminal-error report path")
	flag.DurationVar(&value.timeout, "timeout", 30*time.Second, "bounded helper lifetime")
	flag.DurationVar(&value.debounce, "debounce", 75*time.Millisecond, "netlink event coalescing interval")
	flag.Parse()
	for _, path := range []string{value.stateDirectory, value.stopFile, value.errorFile} {
		if path == "" || !filepath.IsAbs(path) {
			return options{}, errors.New("state, stop, and error paths must be absolute")
		}
	}
	if value.timeout <= 0 || value.debounce <= 0 {
		return options{}, errors.New("timeout and debounce must be positive")
	}
	return value, nil
}

func writeState(directory string, state networkstate.State) error {
	return writeJSON(filepath.Join(directory, fmt.Sprintf("generation-%020d.json", state.Generation)), state)
}

func writeJSON(path string, value any) error {
	temporary := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return errors.Join(encodeErr, closeErr)
	}
	return os.Rename(temporary, path)
}
