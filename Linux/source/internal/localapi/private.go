// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"encoding/hex"
	"errors"
	"unicode"
	"unicode/utf8"

	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type PrivateController interface {
	PrivateState(context.Context, uint32, uint32) (*localv1.PrivateState, error)
	PrivateCommand(context.Context, *localv1.PrivateCommand) (*localv1.PrivateCommandResult, error)
}

func ValidatePrivateCommand(command *localv1.PrivateCommand) error {
	if command == nil || len(command.ProtoReflect().GetUnknown()) != 0 {
		return privateaccess.ErrInvalid
	}
	var fields uint64
	switch command.Action {
	case localv1.PrivateAction_PRIVATE_ACTION_SET_PERSISTENT_HOSTING:
		fields = 1 << 7
	case localv1.PrivateAction_PRIVATE_ACTION_CREATE_INVITATION:
		fields = 1<<6 | 1<<8
		if command.LifetimeSeconds == 0 || command.LifetimeSeconds > 86400 {
			return privateaccess.ErrInvalid
		}
	case localv1.PrivateAction_PRIVATE_ACTION_IMPORT_INVITATION:
		fields = 1<<4 | 1<<5
		if command.InvitationLink == "" || len(command.InvitationLink) > privateaccess.MaxInvitationLinkBytes {
			return privateaccess.ErrInvalid
		}
	case localv1.PrivateAction_PRIVATE_ACTION_APPROVE_INVITATION:
		fields = 1<<3 | 1<<4
		if command.InvitationId == "" {
			return privateaccess.ErrInvalid
		}
	case localv1.PrivateAction_PRIVATE_ACTION_CANCEL_INVITATION:
		fields = 1 << 3
		if command.InvitationId == "" {
			return privateaccess.ErrInvalid
		}
	case localv1.PrivateAction_PRIVATE_ACTION_RESUME_ENROLLMENT, localv1.PrivateAction_PRIVATE_ACTION_REVOKE_MEMBER,
		localv1.PrivateAction_PRIVATE_ACTION_REMOVE_TARGET, localv1.PrivateAction_PRIVATE_ACTION_CANCEL_ENROLLMENT:
		fields = 1 << 2
		if command.PeerId == "" {
			return privateaccess.ErrInvalid
		}
	case localv1.PrivateAction_PRIVATE_ACTION_CONNECT:
		fields = 1<<2 | 1<<7
		if command.PeerId == "" && !command.Hosting {
			return privateaccess.ErrInvalid
		}
	default:
		return privateaccess.ErrInvalid
	}
	valid := true
	command.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if field.Number() != 1 && fields&(1<<uint(field.Number())) == 0 {
			valid = false
		}
		return valid
	})
	if !valid || len(command.Name) > 80 || !utf8.ValidString(command.Name) {
		return privateaccess.ErrInvalid
	}
	for _, ch := range command.Name {
		if unicode.IsControl(ch) {
			return privateaccess.ErrInvalid
		}
	}
	if command.PeerId != "" {
		id, err := peer.Decode(command.PeerId)
		if err != nil || id.String() != command.PeerId || len(command.PeerId) > 128 {
			return privateaccess.ErrInvalid
		}
	}
	if command.InvitationId != "" {
		id, err := hex.DecodeString(command.InvitationId)
		if err != nil || len(id) != 16 || hex.EncodeToString(id) != command.InvitationId {
			return privateaccess.ErrInvalid
		}
	}
	return nil
}

func (server *Server) handlePrivate(parent context.Context, request *localv1.Request) *localv1.Response {
	if server.config.Private == nil {
		return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "Private IRVIO is unavailable")
	}
	valid := len(request.ProtoReflect().GetUnknown()) == 0
	request.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if field.Number() != 1 && !(request.Method == localv1.Method_METHOD_GET_PRIVATE_STATE && field.Number() == 17) &&
			!(request.Method == localv1.Method_METHOD_PRIVATE_COMMAND && field.Number() == 16) {
			valid = false
		}
		return valid
	})
	if !valid {
		return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "invalid private request")
	}
	ctx, cancel := context.WithTimeout(parent, server.config.RequestTimeout)
	defer cancel()
	result := response(localv1.ResultCode_RESULT_CODE_OK, "")
	if request.Method == localv1.Method_METHOD_GET_PRIVATE_STATE {
		page := request.GetGetPrivateState()
		if page != nil && len(page.ProtoReflect().GetUnknown()) != 0 {
			return privateErrorResponse(privateaccess.ErrInvalid)
		}
		limit := page.GetLimit()
		if limit == 0 {
			limit = 32
		}
		if limit > 64 || page.GetOffset() > 256 {
			return privateErrorResponse(privateaccess.ErrInvalid)
		}
		state, err := server.config.Private.PrivateState(ctx, page.GetOffset(), limit)
		if err != nil {
			return privateErrorResponse(err)
		}
		if state == nil {
			return privateErrorResponse(errors.New("missing state"))
		}
		result.Payload = &localv1.Response_PrivateState{PrivateState: state}
	} else {
		if err := ValidatePrivateCommand(request.PrivateCommand); err != nil {
			return privateErrorResponse(err)
		}
		value, err := server.config.Private.PrivateCommand(ctx, request.PrivateCommand)
		if err != nil {
			return privateErrorResponse(err)
		}
		if value == nil {
			return privateErrorResponse(errors.New("missing result"))
		}
		result.Accepted = true
		result.Payload = &localv1.Response_PrivateResult{PrivateResult: value}
	}
	if proto.Size(result) > MaxFrameSize-512 {
		return response(localv1.ResultCode_RESULT_CODE_INTERNAL, "private response exceeds page limit")
	}
	return result
}

func privateErrorResponse(err error) *localv1.Response {
	switch {
	case errors.Is(err, privateaccess.ErrInvalid):
		return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "invalid private parameters")
	case errors.Is(err, privateaccess.ErrDenied):
		return response(localv1.ResultCode_RESULT_CODE_UNAUTHORIZED, "private access denied")
	case errors.Is(err, privateaccess.ErrExpired):
		return response(localv1.ResultCode_RESULT_CODE_NOT_READY, "private invitation expired")
	case errors.Is(err, privateaccess.ErrCapacity):
		return response(localv1.ResultCode_RESULT_CODE_BUSY, "private storage capacity reached")
	case errors.Is(err, rendezvous.ErrPrivateLookupIncomplete):
		return response(localv1.ResultCode_RESULT_CODE_NOT_READY, "private discovery incomplete; retry or disconnect")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return response(localv1.ResultCode_RESULT_CODE_NOT_READY, "private operation did not finish; retry or disconnect")
	default:
		return commandResponse(err)
	}
}
