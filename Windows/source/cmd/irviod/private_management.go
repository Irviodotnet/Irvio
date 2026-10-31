// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Irviodotnet/Irvio/internal/identity"
	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/libp2p/go-libp2p/core/peer"
)

type privateTargetView struct{ PeerID, Name string }
type privateInvitationView struct {
	ID, Client, State string
	ExpiresAt         int64
}
type privateManagementView struct {
	Revision      uint64
	HostPeerID    string
	HostingWanted bool
	Target        peer.ID
	Targets       []privateTargetView
	Members       []privateaccess.Member
	Invitations   []privateInvitationView
	Enrollments   []privateaccess.EnrollmentSummary
	Connected     map[peer.ID]bool
}

// lockPrivateManagement serializes administrative changes with full leave and profile deletion.
func (d *desktopController) lockPrivateManagement(ctx context.Context) error {
	if ctx == nil {
		return irvioruntime.ErrInvalidConfig
	}
	d.lockCommand()
	err := ctx.Err()
	if err == nil && (d.closed || d.teardownPending) {
		err = irvioruntime.ErrInvalidState
	}
	if err == nil {
		err = d.openPrivateStoreLocked()
	}
	if err != nil {
		d.mu.Unlock()
	}
	return err
}

// PrivateManagement returns no locator, invitation secret or signed invitation link.
func (d *desktopController) PrivateManagement(ctx context.Context) (privateManagementView, error) {
	if ctx == nil {
		return privateManagementView{}, irvioruntime.ErrInvalidConfig
	}
	if !d.mu.TryLock() {
		return privateManagementView{}, irvioruntime.ErrInvalidState
	}
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return privateManagementView{}, err
	}
	if d.closed {
		return privateManagementView{}, irvioruntime.ErrClosed
	}
	if err := d.openPrivateStoreLocked(); err != nil {
		return privateManagementView{}, err
	}
	state, err := d.privateStore.Snapshot()
	if err != nil {
		return privateManagementView{}, err
	}
	view := privateManagementView{Revision: state.Revision, HostPeerID: state.HostPeerID, HostingWanted: d.desiredPrivate.Hosting,
		Target: d.desiredPrivate.Target, Members: state.Members}
	view.Connected = map[peer.ID]bool{}
	if d.daemon != nil && d.desiredPrivate.Hosting {
		view.Connected = d.daemon.PrivateConnectedPeers()
	}
	view.Enrollments, err = state.EnrollmentSummaries()
	if err != nil {
		return privateManagementView{}, err
	}
	for _, target := range state.Targets {
		view.Targets = append(view.Targets, privateTargetView{PeerID: target.PeerID, Name: target.Name})
	}
	for _, invite := range state.Invitations {
		view.Invitations = append(view.Invitations, privateInvitationView{ID: invite.ID, Client: invite.Client, State: invite.State, ExpiresAt: invite.ExpiresAt})
	}
	return view, nil
}

func (d *desktopController) ImportPrivateInvitation(ctx context.Context, link, name string) error {
	if err := d.lockPrivateManagement(ctx); err != nil {
		return err
	}
	defer d.mu.Unlock()
	return d.privateStore.ImportInvitation(link, name, time.Now())
}

func (d *desktopController) CancelPrivateEnrollment(ctx context.Context, host peer.ID) error {
	if err := d.lockPrivateManagement(ctx); err != nil {
		return err
	}
	defer d.mu.Unlock()
	return d.privateStore.CancelEnrollment(host)
}

func (d *desktopController) CreatePrivateInvitation(ctx context.Context, lifetime time.Duration) (string, error) {
	return d.createPrivateInvitation(ctx, lifetime, false)
}

func (d *desktopController) createPrivateInvitation(ctx context.Context, lifetime time.Duration, automatic bool) (string, error) {
	if err := d.lockPrivateManagement(ctx); err != nil {
		return "", err
	}
	defer d.mu.Unlock()
	id, err := identity.LoadOrCreate(d.dataDir)
	if err != nil {
		return "", err
	}
	state, err := d.privateStore.Snapshot()
	if err != nil {
		return "", err
	}
	if id.PeerID.String() != state.HostPeerID {
		return "", privateaccess.ErrDenied
	}
	locator, err := hex.DecodeString(state.Locator)
	if err != nil || len(locator) != 32 {
		return "", privateaccess.ErrInvalid
	}
	now := time.Now()
	create := d.privateStore.CreateInvitation
	if automatic {
		create = d.privateStore.CreateAccessInvitation
	}
	invite, err := create(now, lifetime)
	if err != nil {
		return "", err
	}
	token := privateaccess.InvitationToken{Host: id.PeerID, Invitation: invite}
	copy(token.Locator[:], locator)
	link, err := privateaccess.EncodeInvitation(id.PrivateKey, token, now)
	if err != nil {
		return "", errors.Join(err, d.privateStore.CancelInvitation(invite.ID))
	}
	return link, nil
}

func (d *desktopController) ApprovePrivateInvitation(ctx context.Context, id, name string) error {
	if err := d.lockPrivateManagement(ctx); err != nil {
		return err
	}
	defer d.mu.Unlock()
	return d.privateStore.Approve(id, name, time.Now())
}

func (d *desktopController) CancelPrivateInvitation(ctx context.Context, id string) error {
	if err := d.lockPrivateManagement(ctx); err != nil {
		return err
	}
	defer d.mu.Unlock()
	if err := d.privateStore.CancelInvitation(id); err != nil {
		return err
	}
	state, err := d.privateStore.Snapshot()
	if err != nil {
		return err
	}
	for _, invite := range state.Invitations {
		if invite.ID == id && invite.AutoApprove && invite.Client != "" {
			client, err := peer.Decode(invite.Client)
			if err != nil {
				return err
			}
			if d.daemon != nil && d.desiredPrivate.Hosting {
				return d.daemon.RevokePrivateMember(ctx, client)
			}
			return d.privateStore.Revoke(client)
		}
	}
	return nil
}

func (d *desktopController) RevokePrivateMember(ctx context.Context, client peer.ID) error {
	if _, err := peer.IDFromBytes([]byte(client)); err != nil {
		return privateaccess.ErrInvalid
	}
	if err := d.lockPrivateManagement(ctx); err != nil {
		return err
	}
	defer d.mu.Unlock()
	if d.daemon != nil && d.desiredPrivate.Hosting {
		return d.daemon.RevokePrivateMember(ctx, client)
	}
	return d.privateStore.Revoke(client)
}

func (d *desktopController) RemovePrivateTarget(ctx context.Context, target peer.ID) error {
	if _, err := peer.IDFromBytes([]byte(target)); err != nil {
		return privateaccess.ErrInvalid
	}
	if err := d.lockPrivateManagement(ctx); err != nil {
		return err
	}
	defer d.mu.Unlock()
	if d.desiredPrivate.Target == target {
		return irvioruntime.ErrInvalidState
	}
	return d.privateStore.RemoveTarget(target)
}
