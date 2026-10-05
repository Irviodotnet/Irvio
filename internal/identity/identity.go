// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package identity manages the persistent libp2p identity of a IRVIO node.
package identity

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
	cryptopb "github.com/libp2p/go-libp2p/core/crypto/pb"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/sys/unix"
)

const (
	// FileName is the identity file name within a node data directory.
	FileName    = "identity.key"
	maxKeyBytes = 4096
)

// Identity contains a node's private key and derived libp2p PeerID.
type Identity struct {
	PrivateKey crypto.PrivKey
	PeerID     peer.ID
	Created    bool
}

// LoadOrCreate loads an Ed25519 identity or atomically creates one.
func LoadOrCreate(dataDir string) (*Identity, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dataDir, FileName)
	id, err := load(path)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	privateKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		return nil, fmt.Errorf("generate Ed25519 identity: %w", err)
	}
	encoded, err := crypto.MarshalPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal Ed25519 identity: %w", err)
	}
	if err := persist(path, encoded); err != nil {
		if errors.Is(err, os.ErrExist) {
			return load(path)
		}
		return nil, err
	}
	peerID, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("derive PeerID: %w", err)
	}
	return &Identity{PrivateKey: privateKey, PeerID: peerID, Created: true}, nil
}

func load(path string) (*Identity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("inspect identity file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("identity file is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("identity file permissions are %04o, want 0600", info.Mode().Perm())
	}
	if info.Size() <= 0 || info.Size() > maxKeyBytes {
		return nil, fmt.Errorf("identity file size %d is invalid", info.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open identity file: %w", err)
	}
	defer f.Close()
	encoded, err := io.ReadAll(io.LimitReader(f, maxKeyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read identity file: %w", err)
	}
	privateKey, err := crypto.UnmarshalPrivateKey(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode identity file: %w", err)
	}
	if privateKey.Type() != cryptopb.KeyType_Ed25519 {
		return nil, fmt.Errorf("identity key type %s is not Ed25519", privateKey.Type())
	}
	peerID, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("derive PeerID: %w", err)
	}
	return &Identity{PrivateKey: privateKey, PeerID: peerID}, nil
}

func persist(path string, encoded []byte) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".identity-*")
	if err != nil {
		return fmt.Errorf("create temporary identity: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary identity: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary identity: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary identity: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary identity: %w", err)
	}
	if err := unix.Renameat2(unix.AT_FDCWD, temporaryPath, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return os.ErrExist
		}
		return fmt.Errorf("install identity file: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open identity directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync identity directory: %w", err)
	}
	return nil
}
