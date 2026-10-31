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
	"os"
	"path/filepath"

	"github.com/Irviodotnet/Irvio/internal/awg"
)

func loadOrCreate(dataDir string) (*Identity, error) {
	if dataDir == "" {
		return nil, errors.New("AWG identity data directory is empty")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create AWG identity directory: %w", err)
	}
	path := filepath.Join(dataDir, fileName)
	privateKey, epoch, err := readIdentity(path)
	created := false
	if errors.Is(err, os.ErrNotExist) {
		privateKey, err = generatePrivateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		epoch = 1
		if err := writeIdentity(path, privateKey, epoch); err != nil {
			return nil, err
		}
		created = true
	} else if err != nil {
		return nil, err
	}
	publicKey, err := publicFromPrivate(privateKey)
	if err != nil {
		return nil, err
	}
	return &Identity{PrivateKey: privateKey, PublicKey: publicKey, Epoch: epoch, Created: created, path: path}, nil
}

func readIdentity(path string) (awg.Key, uint64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return awg.Key{}, 0, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return awg.Key{}, 0, errors.New("AWG identity must be a private regular file")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return awg.Key{}, 0, fmt.Errorf("read AWG identity: %w", err)
	}
	return decodeIdentity(payload)
}

func writeIdentity(path string, privateKey awg.Key, epoch uint64) error {
	payload, err := encodeIdentity(privateKey, epoch)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".awg-identity-")
	if err != nil {
		return fmt.Errorf("create AWG identity temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect AWG identity temporary file: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write AWG identity: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync AWG identity: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close AWG identity: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("install AWG identity: %w", err)
	}
	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open AWG identity directory: %w", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	return errors.Join(syncErr, closeErr)
}

func writeRotation(path string, key awg.Key, epoch uint64, _ awg.Key) error {
	return writeIdentity(path, key, epoch)
}
