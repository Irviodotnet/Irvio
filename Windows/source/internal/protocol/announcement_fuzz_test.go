// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package protocol

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func FuzzVerifySignedAnnouncementDoesNotPanic(f *testing.F) {
	privateKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	provider, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte(nil))
	f.Add([]byte{0, 1, 2, 3})
	f.Add(make([]byte, MaxSignedEnvelopeSize+1))
	f.Fuzz(func(t *testing.T, envelope []byte) {
		_, _ = VerifySignedAnnouncement(envelope, provider, time.Unix(1_800_000_000, 0))
	})
}
