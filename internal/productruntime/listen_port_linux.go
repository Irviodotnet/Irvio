// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package productruntime

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const ListenPortFilename = "listen-port-v1.json"

type listenPortRecord struct {
	Version int `json:"version"`
	TCPPort int `json:"tcp_port"`
}

// ResolveListenPort persists the announced port; bind conflicts fail instead of changing it silently.
func ResolveListenPort(dataDir string) (int, error) {
	path := filepath.Join(dataDir, ListenPortFilename)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return 0, fmt.Errorf("persisted listen port record is not a private regular file")
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		var record listenPortRecord
		if len(value) > 128 || json.Unmarshal(value, &record) != nil || record.Version != 1 ||
			record.TCPPort < 40000 || record.TCPPort > 59999 {
			return 0, fmt.Errorf("persisted listen port record is invalid")
		}
		return record.TCPPort, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("inspect persisted listen port: %w", err)
	}
	var random [2]byte
	if _, err := rand.Read(random[:]); err != nil {
		return 0, fmt.Errorf("choose listen port: %w", err)
	}
	port := 40000 + int(binary.BigEndian.Uint16(random[:]))%20000
	value, err := json.Marshal(listenPortRecord{Version: 1, TCPPort: port})
	if err != nil {
		return 0, err
	}
	if err := writeExitPool(path, value); err != nil {
		return 0, fmt.Errorf("persist listen port: %w", err)
	}
	return port, nil
}
