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
	"sync"

	"golang.org/x/crypto/curve25519"

	"github.com/Irviodotnet/Irvio/internal/awg"
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
	return loadOrCreate(dataDir)
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
	if err := writeRotation(i.path, privateKey, epoch, i.PrivateKey); err != nil {
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

func decodeIdentity(payload []byte) (awg.Key, uint64, error) {
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

func encodeIdentity(privateKey awg.Key, epoch uint64) ([]byte, error) {
	if epoch == 0 || privateKey == (awg.Key{}) {
		return nil, errors.New("invalid AWG identity")
	}
	payload := make([]byte, fileSize)
	copy(payload[:8], fileMagic[:])
	binary.BigEndian.PutUint64(payload[8:16], epoch)
	copy(payload[16:], privateKey[:])
	return payload, nil
}
