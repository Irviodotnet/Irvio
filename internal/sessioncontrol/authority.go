// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

type Authority interface {
	Lookup(peer.ID, tunnellease.ID, uint64, tunnellease.SessionToken) (tunnellease.Lease, error)
	Renew(peer.ID, tunnellease.ID, uint64, tunnellease.SessionToken) (tunnellease.Lease, error)
	Release(context.Context, peer.ID, tunnellease.ID, uint64, tunnellease.SessionToken) error
}

// RotationAuthority coordinates an Exit's planned AWG key change over an authenticated tunnel.
type RotationAuthority interface {
	RotationProposal(peer.ID, tunnellease.ID, uint64, tunnellease.SessionToken) (RotationProposal, error)
	ValidateAWGRotation(peer.ID, tunnellease.ID, uint64, tunnellease.SessionToken, uint64, [32]byte) error
	CommitAWGRotation(context.Context, peer.ID, tunnellease.ID, uint64, tunnellease.SessionToken, uint64, [32]byte) (tunnellease.Lease, error)
}

type LifecycleAuthority struct {
	Lifecycle *tunnellease.Lifecycle
}

func (a LifecycleAuthority) Lookup(client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken) (tunnellease.Lease, error) {
	if a.Lifecycle == nil {
		return tunnellease.Lease{}, ErrClosed
	}
	return a.Lifecycle.LookupSession(client, id, revision, token)
}

func (a LifecycleAuthority) Renew(client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken) (tunnellease.Lease, error) {
	if a.Lifecycle == nil {
		return tunnellease.Lease{}, ErrClosed
	}
	return a.Lifecycle.RenewSession(client, id, revision, token)
}

func (a LifecycleAuthority) Release(ctx context.Context, client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken) error {
	if a.Lifecycle == nil {
		return ErrClosed
	}
	return a.Lifecycle.ReleaseSession(ctx, client, id, revision, token)
}
