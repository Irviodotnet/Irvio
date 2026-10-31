// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/Irviodotnet/Irvio/internal/sessionestablish"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/libp2p/go-libp2p/core/peer"
)

const m12ProvisionalLifetime = 30 * time.Second

// Signaling bounds ANSWER delivery; ICE/AWG/BIND use a separate bounded provisional lifetime.
func m12ActivationDeadline(_ context.Context, now time.Time) time.Time {
	return now.Add(m12ProvisionalLifetime)
}

func m12LeaseTokenFor(attempt sessionestablish.AttemptID, owner [sessionestablish.OwnerTokenSize]byte, client peer.ID) tunnellease.SessionToken {
	hash := sha256.New()
	_, _ = hash.Write([]byte("hydra/m12/lease-token/v1\x00"))
	_, _ = hash.Write(attempt[:])
	_, _ = hash.Write(owner[:])
	_, _ = hash.Write([]byte(client))
	var token tunnellease.SessionToken
	copy(token[:], hash.Sum(nil))
	return token
}
