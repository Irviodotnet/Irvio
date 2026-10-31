// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awgidentity

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/winfile"
)

const lockFileName = "awg-identity.lock"

var ErrStaleIdentity = errors.New("AWG identity changed since it was loaded")

func withIdentityDirectory(path string, create bool, operation func(*winfile.Directory) error) (resultErr error) {
	if path == "" {
		return errors.New("AWG identity data directory is empty")
	}
	directory, err := winfile.OpenDirectory(path, create)
	if err != nil {
		return fmt.Errorf("open AWG identity directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	lock, err := directory.OpenFile(lockFileName, true, true)
	if err != nil {
		return fmt.Errorf("open AWG identity lock: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	if err := winfile.Lock(lock); err != nil {
		return fmt.Errorf("lock AWG identity: %w", err)
	}
	return operation(directory)
}

func readWindowsIdentity(directory *winfile.Directory) (awg.Key, uint64, error) {
	file, err := directory.OpenFile(fileName, false, false)
	if err != nil {
		return awg.Key{}, 0, err
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, fileSize+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return awg.Key{}, 0, fmt.Errorf("read AWG identity: %w", err)
	}
	return decodeIdentity(payload)
}

func loadOrCreate(dataDir string) (*Identity, error) {
	var result *Identity
	err := withIdentityDirectory(dataDir, true, func(directory *winfile.Directory) error {
		privateKey, epoch, err := readWindowsIdentity(directory)
		created := false
		if errors.Is(err, os.ErrNotExist) {
			privateKey, err = generatePrivateKey(rand.Reader)
			if err != nil {
				return err
			}
			epoch = 1
			payload, err := encodeIdentity(privateKey, epoch)
			if err != nil {
				return err
			}
			if err := directory.WriteAtomic(fileName, payload, false); err != nil {
				return fmt.Errorf("create AWG identity: %w", err)
			}
			created = true
		} else if err != nil {
			return err
		}
		publicKey, err := publicFromPrivate(privateKey)
		if err != nil {
			return err
		}
		result = &Identity{PrivateKey: privateKey, PublicKey: publicKey, Epoch: epoch, Created: created, path: filepath.Join(dataDir, fileName)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Rotation is fenced against stale instances while the native lock and directory remain pinned.
func writeRotation(path string, key awg.Key, epoch uint64, expected awg.Key) error {
	payload, err := encodeIdentity(key, epoch)
	if err != nil {
		return err
	}
	if filepath.Base(path) != fileName || epoch <= 1 {
		return ErrStaleIdentity
	}
	return withIdentityDirectory(filepath.Dir(path), false, func(directory *winfile.Directory) error {
		current, currentEpoch, err := readWindowsIdentity(directory)
		if err != nil {
			return err
		}
		if current != expected || currentEpoch != epoch-1 {
			return ErrStaleIdentity
		}
		return directory.WriteAtomic(fileName, payload, true)
	})
}
