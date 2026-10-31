// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

// Product values are create-once, ACL-checked records, never silently replaced on a concurrent start.
func resolveWindowsRecord(path, name string, makeValue func() ([]byte, error)) (value []byte, resultErr error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("Windows product profile must use an absolute path")
	}
	directory, err := winfile.OpenDirectory(path, false)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	value, err = readWindowsRecord(directory, name)
	if !errors.Is(err, os.ErrNotExist) {
		return value, err
	}
	value, err = makeValue()
	if err != nil {
		return nil, err
	}
	if err := directory.WriteAtomic(name, value, false); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return readWindowsRecord(directory, name)
}

func readWindowsRecord(directory *winfile.Directory, name string) ([]byte, error) {
	file, err := directory.OpenImmutableFile(name)
	if err != nil {
		return nil, err
	}
	value, readErr := io.ReadAll(io.LimitReader(file, 1025))
	err = errors.Join(readErr, file.Close())
	if len(value) > 1024 {
		err = errors.Join(err, errors.New("Windows product profile record exceeds size limit"))
	}
	return value, err
}

func ResolveListenPort(dataDir string) (int, error) {
	value, err := resolveWindowsRecord(dataDir, ListenPortFilename, func() ([]byte, error) {
		var random [2]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		return json.Marshal(listenPortRecord{Version: 1, TCPPort: 40000 + int(binary.BigEndian.Uint16(random[:]))%20000})
	})
	if err != nil {
		return 0, fmt.Errorf("resolve persisted listen port: %w", err)
	}
	var record listenPortRecord
	if len(value) > 128 || rejectDuplicateFields(value) != nil || json.Unmarshal(value, &record) != nil ||
		record.Version != 1 || record.TCPPort < 40000 || record.TCPPort > 59999 {
		return 0, errors.New("persisted listen port record is invalid")
	}
	return record.TCPPort, nil
}

// ResolveExitPool checks native host routes before reusing the same per-profile pool.
func ResolveExitPool(dataDir string) (netip.Prefix, netip.Prefix, error) {
	occupied, err := hostIPv4Prefixes()
	if err != nil {
		return netip.Prefix{}, netip.Prefix{}, err
	}
	return resolveWindowsExitPool(dataDir, occupied)
}

func resolveWindowsExitPool(dataDir string, occupied []netip.Prefix) (netip.Prefix, netip.Prefix, error) {
	value, err := resolveWindowsRecord(dataDir, ExitPoolFilename, func() ([]byte, error) {
		var random [4]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		pool, err := selectExitPoolFrom(occupied, binary.BigEndian.Uint32(random[:]))
		if err != nil {
			return nil, err
		}
		return json.Marshal(exitPoolRecord{Version: 1, IPv4: pool.String()})
	})
	if err != nil {
		return netip.Prefix{}, netip.Prefix{}, fmt.Errorf("resolve persisted Exit pool: %w", err)
	}
	var record exitPoolRecord
	if rejectDuplicateFields(value) != nil || json.Unmarshal(value, &record) != nil || record.Version != 1 {
		return netip.Prefix{}, netip.Prefix{}, errors.New("persisted Exit pool record is invalid")
	}
	pool, err := netip.ParsePrefix(record.IPv4)
	if err != nil || !allowedExitPool(pool) {
		return netip.Prefix{}, netip.Prefix{}, errors.New("persisted Exit pool is invalid")
	}
	if overlapsAny(pool, occupied) {
		return netip.Prefix{}, netip.Prefix{}, fmt.Errorf("persisted Exit pool %s now conflicts with a host route or address", pool)
	}
	return pool, exitTUNAddress(pool), nil
}
