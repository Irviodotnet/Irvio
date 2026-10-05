// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import "errors"

var (
	ErrClosed                      = errors.New("AWG transport is closed")
	ErrInvalidConfig               = errors.New("invalid AWG configuration")
	ErrInvalidEndpoint             = errors.New("invalid AWG endpoint")
	ErrWrongGeneration             = errors.New("AWG endpoint belongs to another network generation")
	ErrStaleSession                = errors.New("AWG endpoint belongs to a stale ICE session generation")
	ErrWrongPeer                   = errors.New("AWG endpoint belongs to another peer")
	ErrPeerNotFound                = errors.New("AWG peer not found")
	ErrPeerExists                  = errors.New("AWG peer already exists")
	ErrTooManyPeers                = errors.New("too many AWG peers")
	ErrTransportPaddingUnsupported = errors.New("nonzero AWG S4 is disabled for the pinned upstream revision")
)
