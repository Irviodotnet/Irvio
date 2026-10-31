// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package main

import (
	"context"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"github.com/libp2p/go-libp2p/core/peer"
)

func privatePage[T any](values []T, offset, limit uint32) []T {
	start := min(int(offset), len(values))
	return values[start:min(start+int(limit), len(values))]
}

func (d *desktopController) PrivateState(ctx context.Context, offset, limit uint32) (*localv1.PrivateState, error) {
	if limit == 0 || limit > 64 || offset > 256 {
		return nil, privateaccess.ErrInvalid
	}
	view, err := d.PrivateManagement(ctx)
	if err != nil {
		return nil, err
	}
	state := &localv1.PrivateState{LocalPeerId: view.HostPeerID, HostingWanted: view.HostingWanted, Revision: view.Revision,
		TargetCount: uint32(len(view.Targets)), MemberCount: uint32(len(view.Members)),
		InvitationCount: uint32(len(view.Invitations)), EnrollmentCount: uint32(len(view.Enrollments))}
	state.PersistentHosting, err = d.persistentHosting()
	if err != nil {
		return nil, err
	}
	if view.Target != "" {
		state.TargetPeerId = view.Target.String()
	}
	for _, target := range privatePage(view.Targets, offset, limit) {
		state.Targets = append(state.Targets, &localv1.PrivateTarget{PeerId: target.PeerID, Name: target.Name})
	}
	for _, member := range privatePage(view.Members, offset, limit) {
		value := &localv1.PrivateMember{PeerId: member.PeerID, Name: member.Name, Allowed: member.Allowed}
		if view.Connected != nil {
			id, _ := peer.Decode(member.PeerID)
			connected := member.Allowed && view.Connected[id]
			value.Connected = &connected
		}
		state.Members = append(state.Members, value)
	}
	for _, invite := range privatePage(view.Invitations, offset, limit) {
		state.Invitations = append(state.Invitations, &localv1.PrivateInvitation{InvitationId: invite.ID, ClientPeerId: invite.Client, State: invite.State, ExpiresAtUnix: invite.ExpiresAt})
	}
	for _, entry := range privatePage(view.Enrollments, offset, limit) {
		state.Enrollments = append(state.Enrollments, &localv1.PrivateEnrollment{HostPeerId: entry.HostPeerID, Name: entry.Name, State: entry.State, ExpiresAtUnix: entry.ExpiresAt})
	}
	return state, nil
}

func (d *desktopController) PrivateCommand(ctx context.Context, command *localv1.PrivateCommand) (*localv1.PrivateCommandResult, error) {
	if err := localapi.ValidatePrivateCommand(command); err != nil {
		return nil, err
	}
	var target peer.ID
	if command.PeerId != "" {
		target, _ = peer.Decode(command.PeerId)
	}
	result := new(localv1.PrivateCommandResult)
	var err error
	switch command.Action {
	case localv1.PrivateAction_PRIVATE_ACTION_SET_PERSISTENT_HOSTING:
		err = d.setPersistentHosting(command.Hosting)
	case localv1.PrivateAction_PRIVATE_ACTION_CREATE_INVITATION:
		result.InvitationLink, err = d.createPrivateInvitation(ctx, time.Duration(command.LifetimeSeconds)*time.Second, command.AutoApprove)
	case localv1.PrivateAction_PRIVATE_ACTION_IMPORT_INVITATION:
		err = d.ImportPrivateInvitation(ctx, command.InvitationLink, command.Name)
		if err == nil {
			token, decodeErr := privateaccess.DecodeInvitation(command.InvitationLink, time.Now())
			err = decodeErr
			result.TargetPeerId = token.Host.String()
		}
	case localv1.PrivateAction_PRIVATE_ACTION_RESUME_ENROLLMENT:
		status, resumeErr := d.ResumePrivateEnrollment(ctx, target)
		err = resumeErr
		result.EnrollmentState = status.String()
	case localv1.PrivateAction_PRIVATE_ACTION_APPROVE_INVITATION:
		err = d.ApprovePrivateInvitation(ctx, command.InvitationId, command.Name)
	case localv1.PrivateAction_PRIVATE_ACTION_CANCEL_INVITATION:
		err = d.CancelPrivateInvitation(ctx, command.InvitationId)
	case localv1.PrivateAction_PRIVATE_ACTION_REVOKE_MEMBER:
		err = d.RevokePrivateMember(ctx, target)
	case localv1.PrivateAction_PRIVATE_ACTION_REMOVE_TARGET:
		err = d.RemovePrivateTarget(ctx, target)
	case localv1.PrivateAction_PRIVATE_ACTION_CANCEL_ENROLLMENT:
		err = d.CancelPrivateEnrollment(ctx, target)
	case localv1.PrivateAction_PRIVATE_ACTION_CONNECT:
		err = d.ConnectPrivate(ctx, target, command.Hosting)
	default:
		err = privateaccess.ErrInvalid
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

var _ localapi.PrivateController = (*desktopController)(nil)
