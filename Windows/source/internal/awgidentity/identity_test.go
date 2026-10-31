// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awgidentity

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/awg"
)

func TestIdentityEncodingCompatibility(t *testing.T) {
	var key awg.Key
	for i := range key {
		key[i] = byte(i + 1)
	}
	const epoch = uint64(0x0102030405060708)
	payload, err := encodeIdentity(key, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 48 || string(payload[:8]) != "HYDRAWG1" || binary.BigEndian.Uint64(payload[8:16]) != epoch || !bytes.Equal(payload[16:], key[:]) {
		t.Fatal("AWG durable format changed")
	}
	decoded, actual, err := decodeIdentity(payload)
	if err != nil || decoded != key || actual != epoch {
		t.Fatal("AWG durable roundtrip failed", err)
	}
	for _, invalid := range [][]byte{nil, payload[:47], append(append([]byte{}, payload...), 0), make([]byte, 48)} {
		if _, _, err := decodeIdentity(invalid); err == nil {
			t.Fatal("invalid AWG identity decoded")
		}
	}
	zeroEpoch := append([]byte{}, payload...)
	clear(zeroEpoch[8:16])
	if _, _, err := decodeIdentity(zeroEpoch); err == nil {
		t.Fatal("zero epoch decoded")
	}
	if _, err := encodeIdentity(key, 0); err == nil {
		t.Fatal("zero epoch encoded")
	}
	if _, err := encodeIdentity(awg.Key{}, 1); err == nil {
		t.Fatal("zero private key encoded")
	}
}

func TestPersistentIdentityAndRotation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	first, err := LoadOrCreate(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Epoch != 1 || first.PrivateKey == first.PublicKey {
		t.Fatalf("unexpected first identity: created=%v epoch=%d", first.Created, first.Epoch)
	}
	reloaded, err := LoadOrCreate(directory)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Created || reloaded.Epoch != first.Epoch || reloaded.PrivateKey != first.PrivateKey || reloaded.PublicKey != first.PublicKey {
		t.Fatal("AWG identity did not persist")
	}
	oldPrivate, oldPublic := reloaded.PrivateKey, reloaded.PublicKey
	publicKey, epoch, err := reloaded.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if epoch != 2 || publicKey == oldPublic || reloaded.PrivateKey == oldPrivate {
		t.Fatal("AWG rotation did not create a fresh epoch and key")
	}
	after, err := LoadOrCreate(directory)
	if err != nil {
		t.Fatal(err)
	}
	if after.Epoch != 2 || after.PublicKey != publicKey || after.PrivateKey != reloaded.PrivateKey {
		t.Fatal("rotated AWG identity did not persist")
	}
	assertIdentityPermissions(t, directory)
}
