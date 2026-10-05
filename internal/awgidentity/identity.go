// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package awgidentity owns Irvio's persistent AmneziaWG transport identity.
package awgidentity

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/curve25519"

	"hydra.local/hydra/internal/awg"
)

const (
	fileName = "awg-identity"
	fileSize = 48
)

var fileMagic = [8]byte{'H', 'Y', 'D', 'R', 'A', 'W', 'G', '1'}

// Identity persists independently of Ed25519 identity and transport generations.
type Identity struct {
	PrivateKey awg.Key
	PublicKey  awg.Key
	Epoch      uint64
	Created    bool

	path string
	mu   sync.Mutex
}

// LoadOrCreate loads the persistent AWG key or atomically creates epoch one.
func LoadOrCreate(dataDir string) (*Identity, error) {
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

// Rotate atomically installs a fresh key at the next key epoch.
func (i *Identity) Rotate() (awg.Key, uint64, error) {
	if i == nil {
		return awg.Key{}, 0, errors.New("AWG identity is nil")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.Epoch == ^uint64(0) {
		return awg.Key{}, 0, errors.New("AWG key epoch exhausted")
	}
	privateKey, err := generatePrivateKey(rand.Reader)
	if err != nil {
		return awg.Key{}, 0, err
	}
	publicKey, err := publicFromPrivate(privateKey)
	if err != nil {
		return awg.Key{}, 0, err
	}
	epoch := i.Epoch + 1
	if err := writeIdentity(i.path, privateKey, epoch); err != nil {
		return awg.Key{}, 0, err
	}
	i.PrivateKey, i.PublicKey, i.Epoch, i.Created = privateKey, publicKey, epoch, false
	return publicKey, epoch, nil
}

// Snapshot reads key material under lock; never log or persist the private key elsewhere.
func (i *Identity) Snapshot() (awg.Key, awg.Key, uint64) {
	if i == nil {
		return awg.Key{}, awg.Key{}, 0
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.PrivateKey, i.PublicKey, i.Epoch
}

func generatePrivateKey(reader io.Reader) (awg.Key, error) {
	var key awg.Key
	if _, err := io.ReadFull(reader, key[:]); err != nil {
		return awg.Key{}, fmt.Errorf("generate AWG private key: %w", err)
	}
	key[0] &= 248
	key[31] &= 127
	key[31] |= 64
	return key, nil
}

func publicFromPrivate(privateKey awg.Key) (awg.Key, error) {
	payload, err := curve25519.X25519(privateKey[:], curve25519.Basepoint)
	if err != nil {
		return awg.Key{}, fmt.Errorf("derive AWG public key: %w", err)
	}
	var publicKey awg.Key
	copy(publicKey[:], payload)
	return publicKey, nil
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
	if len(payload) != fileSize || string(payload[:8]) != string(fileMagic[:]) {
		return awg.Key{}, 0, errors.New("invalid AWG identity file")
	}
	epoch := binary.BigEndian.Uint64(payload[8:16])
	if epoch == 0 {
		return awg.Key{}, 0, errors.New("invalid AWG key epoch")
	}
	var privateKey awg.Key
	copy(privateKey[:], payload[16:])
	publicKey, err := publicFromPrivate(privateKey)
	if err != nil || publicKey == (awg.Key{}) {
		return awg.Key{}, 0, errors.New("invalid AWG private key")
	}
	return privateKey, epoch, nil
}

func writeIdentity(path string, privateKey awg.Key, epoch uint64) error {
	if epoch == 0 || privateKey == (awg.Key{}) {
		return errors.New("invalid AWG identity")
	}
	payload := make([]byte, fileSize)
	copy(payload[:8], fileMagic[:])
	binary.BigEndian.PutUint64(payload[8:16], epoch)
	copy(payload[16:], privateKey[:])
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
