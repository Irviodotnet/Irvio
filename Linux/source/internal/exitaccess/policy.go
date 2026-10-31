// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitaccess

import (
	"context"
	"errors"

	"github.com/libp2p/go-libp2p/core/peer"
)

var ErrDenied = errors.New("Exit admission denied")

// Permit fences resource mutations against revocation until Release; Context cancels on revoke.
type Permit struct {
	Revision uint64
	Context  context.Context
	Release  func()
}

// Policy implementations must not call lease operations while acquiring an admission permit.
type Policy interface {
	Acquire(context.Context, peer.ID) (Permit, error)
}

type PolicyFunc func(context.Context, peer.ID) (Permit, error)

func (f PolicyFunc) Acquire(ctx context.Context, client peer.ID) (Permit, error) {
	return f(ctx, client)
}
