// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitaccess"
	"github.com/libp2p/go-libp2p/core/peer"
)

func (s *ExitServer) registerAuthorizedPending(ctx context.Context, revision uint64, pending *pendingExitSession) error {
	if s.config.PrivateAccess != nil {
		permit, err := s.config.PrivateAccess.Acquire(ctx, pending.remote)
		if err != nil {
			return err
		}
		defer permit.Release()
		if permit.Revision != revision {
			return exitaccess.ErrDenied
		}
	}
	return s.plane.registerPending(pending)
}

// RevokePrivateClient fences all admissions until leases and pending resources are drained.
func (s *ExitServer) RevokePrivateClient(ctx context.Context, client peer.ID) error {
	if s == nil || ctx == nil || s.config.PrivateAccess == nil {
		return ErrInvalidConfig
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.config.PrivateAccess.Revoke(ctx, client, func(ctx context.Context, client peer.ID) error {
		if s.m12 != nil {
			s.m12.responder.RevokePeer(client)
		}
		return s.lifecycle.RevokeClient(ctx, client)
	})
}
