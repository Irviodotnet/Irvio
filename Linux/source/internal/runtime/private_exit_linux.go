// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/libp2p/go-libp2p/core/peer"
)

func (p *exitDataPlane) discardPending(token tunnellease.SessionToken, client peer.ID) error {
	p.mu.Lock()
	pending := p.pending[token]
	if pending != nil && pending.remote == client {
		delete(p.pending, token)
	} else {
		pending = nil
	}
	p.mu.Unlock()
	if pending != nil {
		return pending.ice.Close()
	}
	return nil
}
