// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnelsignal

import (
	"errors"
	"time"

	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	ProtocolVersion uint32      = 1
	ProtocolID      protocol.ID = "/hydra/tunnel/1.0.0"
	MaxFrameSize                = 4096
	DefaultTimeout              = 5 * time.Second
)

var (
	ErrInvalidMessage = errors.New("invalid tunnel lease message")
	ErrWrongPeer      = errors.New("authenticated tunnel lease peer mismatch")
	ErrRemote         = errors.New("remote tunnel lease request rejected")
	ErrClosed         = errors.New("tunnel lease signaling is closed")
)
