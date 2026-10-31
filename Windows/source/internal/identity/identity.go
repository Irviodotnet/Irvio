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
	if err := ensureDataDirectory(dataDir); err != nil {
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
	f, release, err := openIdentityFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("inspect identity file: %w", err)
	}
	defer release()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat identity file: %w", err)
	}
	if info.Size() <= 0 || info.Size() > maxKeyBytes {
		return nil, fmt.Errorf("identity file size %d is invalid", info.Size())
	}
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
