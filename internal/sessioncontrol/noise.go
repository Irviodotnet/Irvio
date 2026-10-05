// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"fmt"

	"github.com/libp2p/go-libp2p/core/crypto"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
)

func noiseTransport(privateKey crypto.PrivKey) (*noise.SessionTransport, error) {
	if privateKey == nil {
		return nil, ErrInvalid
	}
	base, err := noise.New(noise.ID, privateKey, nil)
	if err != nil {
		return nil, fmt.Errorf("create session-control Noise transport: %w", err)
	}
	session, err := base.WithSessionOptions(noise.Prologue([]byte(ProtocolID)))
	if err != nil {
		return nil, fmt.Errorf("bind session-control Noise prologue: %w", err)
	}
	return session, nil
}

var _ = libprotocol.ID(ProtocolID)
