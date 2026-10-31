// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package runtime

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// RevokePrivateMember uses the live admission fence, never a store-only shortcut.
func (d *Daemon) RevokePrivateMember(ctx context.Context, client peer.ID) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	if _, err := peer.IDFromBytes([]byte(client)); err != nil {
		return ErrInvalidConfig
	}
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	d.mu.RLock()
	server := d.privateAdminExit
	d.mu.RUnlock()
	if server == nil {
		return ErrNotStarted
	}
	return server.RevokePrivateClient(ctx, client)
}

type privateHostingAccess struct {
	policy     *privateaccess.Policy
	enrollment *privateaccess.EnrollmentServer
	locator    [32]byte
}

// newPrivateHostingAccess binds enrollment and lease admission to one caller-owned durable store.
func newPrivateHostingAccess(store *privateaccess.Store, key crypto.PrivKey) (*privateHostingAccess, error) {
	enrollment, err := privateaccess.NewEnrollmentServer(store, key)
	if err != nil {
		return nil, err
	}
	state, err := store.Snapshot()
	if err != nil {
		return nil, err
	}
	locator, err := hex.DecodeString(state.Locator)
	if err != nil || len(locator) != 32 {
		return nil, ErrInvalidConfig
	}
	var pinned [32]byte
	copy(pinned[:], locator)
	if pinned == ([32]byte{}) {
		return nil, ErrInvalidConfig
	}
	policy, err := privateaccess.NewPolicy(store)
	if err != nil {
		return nil, err
	}
	access := &privateHostingAccess{policy: policy, enrollment: enrollment, locator: pinned}
	return access, nil
}

func (d *Daemon) startPrivateHosting() (resultErr error) {
	if d.privateHosting == nil || d.node == nil || d.m12 == nil || d.exitServer == nil ||
		d.exitServer.config.PrivateAccess != d.privateHosting.policy {
		return ErrInvalidConfig
	}
	endpoint, err := rendezvous.NewPrivateEnrollmentEndpoint(d.node.Host(), d.privateHosting.enrollment.Serve)
	if err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, d.node.SetPrivateExitReady(false), endpoint.Close())
		}
	}()
	if err := d.node.SetPrivateExitReady(true); err != nil {
		return err
	}
	return d.m12.StartPrivateExitRegistrations(d.exitServer.SessionEstablishmentHandler(), d.privateHosting.locator, endpoint)
}
