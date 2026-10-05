// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/discovery"
	"hydra.local/hydra/internal/icesession"
	"hydra.local/hydra/internal/protocol"
)

// CoreAuthorizer permits current authenticated EXIT requests, not token entitlement or reputation.
type CoreAuthorizer struct {
	Node      *discovery.Node
	Available func(peer.ID) bool
	Now       func() time.Time
}

func (a CoreAuthorizer) AuthorizeICE(ctx context.Context, remote peer.ID, purpose icesession.SessionPurpose) error {
	if ctx == nil || a.Node == nil || remote == "" || remote == a.Node.PeerID() ||
		purpose != icesession.PurposeExit {
		return errors.New("ICE request is outside Irvio Core authorization")
	}
	if a.Available != nil && !a.Available(remote) {
		return errors.New("ICE resource capacity is unavailable")
	}
	if err := a.Node.AuthenticatePeer(ctx, remote); err != nil {
		return fmt.Errorf("authenticate ICE peer announcement: %w", err)
	}
	record, err := a.Node.Directory().Peer(ctx, remote.String())
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	if record.ProtocolVersion != protocol.ProtocolVersion ||
		record.Capabilities&protocol.CapDiscovery == 0 ||
		len(record.AWGPublicKey) != 32 || record.AWGKeyEpoch == 0 ||
		!record.ExpiresAt.After(now) {
		return errors.New("authenticated peer is incompatible or expired")
	}
	return nil
}
