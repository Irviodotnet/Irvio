// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	"github.com/Irviodotnet/Irvio/internal/runtime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"google.golang.org/protobuf/proto"
)

type fakePrivateController struct {
	calls    atomic.Int32
	fail     bool
	oversize bool
}

func (f *fakePrivateController) PrivateState(context.Context, uint32, uint32) (*localv1.PrivateState, error) {
	f.calls.Add(1)
	state := &localv1.PrivateState{Revision: 7}
	if f.oversize {
		state.LocalPeerId = strings.Repeat("x", MaxFrameSize)
	}
	return state, nil
}
func (f *fakePrivateController) PrivateCommand(context.Context, *localv1.PrivateCommand) (*localv1.PrivateCommandResult, error) {
	f.calls.Add(1)
	if f.fail {
		return nil, errors.New("secret-do-not-expose")
	}
	return &localv1.PrivateCommandResult{}, nil
}

func TestPrivateAPINegotiationAndStrictDispatch(t *testing.T) {
	backend := new(fakePrivateController)
	server, path := startTestServer(t, &fakeController{status: runtime.Status{State: runtime.StateHosting}}, func(config *ServerConfig) { config.Private = backend })
	ctx := context.Background()
	old, err := DialVersion(ctx, path, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if old.SupportsPrivateNetwork() {
		t.Fatal("old version negotiated private support")
	}
	if _, err := old.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_GET_PRIVATE_STATE}); err == nil {
		t.Fatal("old client bypassed server version gate")
	}
	if backend.calls.Load() != 0 {
		t.Fatal("unsupported call reached backend")
	}
	client := dialTestClient(t, path)
	if !client.SupportsPrivateNetwork() {
		t.Fatal("private capability missing")
	}
	state, err := client.GetPrivateState(ctx, 0, 32)
	if err != nil || state.GetRevision() != 7 {
		t.Fatalf("state: %v", err)
	}
	status, err := client.GetStatus(ctx)
	if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_HOSTING {
		t.Fatal("hosting mapped to disconnected")
	}
	valid := &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CREATE_INVITATION, LifetimeSeconds: 3600}
	if _, err := client.PrivateCommand(ctx, valid); err != nil {
		t.Fatal(err)
	}
	before := backend.calls.Load()
	for _, request := range []*localv1.Request{
		{Method: localv1.Method_METHOD_PRIVATE_COMMAND},
		{Method: localv1.Method_METHOD_PRIVATE_COMMAND, CountryCode: "NL", PrivateCommand: valid},
		{Method: localv1.Method_METHOD_GET_PRIVATE_STATE, GetPrivateState: &localv1.GetPrivateStateRequest{Limit: 65}},
		{Method: localv1.Method_METHOD_GET_PRIVATE_STATE, GetPrivateState: &localv1.GetPrivateStateRequest{Offset: 257}},
	} {
		if result := server.handle(request); result.Code != localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT {
			t.Fatal("ambiguous or unbounded request accepted")
		}
	}
	if before != backend.calls.Load() {
		t.Fatal("invalid input reached backend")
	}
}

func TestPrivateCommandValidation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		if err := ValidatePrivateCommand(&localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_SET_PERSISTENT_HOSTING, Hosting: enabled}); err != nil {
			t.Fatal(err)
		}
	}
	valid := &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, Hosting: true}
	if err := ValidatePrivateCommand(valid); err != nil {
		t.Fatal(err)
	}
	for _, command := range []*localv1.PrivateCommand{
		nil, {},
		{Action: localv1.PrivateAction_PRIVATE_ACTION_SET_PERSISTENT_HOSTING, Hosting: true, Name: "unexpected"},
		{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT},
		{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, Hosting: true, Name: "unexpected"},
		{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, PeerId: "not-a-peer"},
		{Action: localv1.PrivateAction_PRIVATE_ACTION_IMPORT_INVITATION, InvitationLink: strings.Repeat("x", 2049)},
		{Action: localv1.PrivateAction_PRIVATE_ACTION_IMPORT_INVITATION, InvitationLink: "link", Name: "bad\nname"},
		{Action: localv1.PrivateAction_PRIVATE_ACTION_CREATE_INVITATION, LifetimeSeconds: 86401},
		{Action: localv1.PrivateAction_PRIVATE_ACTION_APPROVE_INVITATION, InvitationId: "invalid"},
	} {
		if err := ValidatePrivateCommand(command); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
	unknown := proto.Clone(valid).(*localv1.PrivateCommand)
	unknown.ProtoReflect().SetUnknown([]byte{0x80, 0x01, 0x01})
	if err := ValidatePrivateCommand(unknown); err == nil {
		t.Fatal("unknown command fields accepted")
	}
}

func TestPrivateAPISanitizesErrorsAndBoundsResponses(t *testing.T) {
	backend := &fakePrivateController{fail: true, oversize: true}
	server, path := startTestServer(t, &fakeController{}, func(config *ServerConfig) { config.Private = backend })
	client := dialTestClient(t, path)
	_, err := client.PrivateCommand(context.Background(), &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, Hosting: true})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("backend error leaked")
	}
	result := server.handle(&localv1.Request{Method: localv1.Method_METHOD_GET_PRIVATE_STATE})
	if result.Code != localv1.ResultCode_RESULT_CODE_INTERNAL || proto.Size(result) >= MaxFrameSize {
		t.Fatal("oversize page escaped bound")
	}
	if _, err := client.GetStatus(context.Background()); err != nil {
		t.Fatal("bad private request broke session")
	}
	_, noPrivate := startTestServer(t, &fakeController{}, nil)
	unsupported := dialTestClient(t, noPrivate)
	if unsupported.SupportsPrivateNetwork() {
		t.Fatal("missing backend advertised support")
	}
}

func TestPrivateIncompleteLookupIsRetryableWithoutLeakingDetails(t *testing.T) {
	result := privateErrorResponse(errors.Join(rendezvous.ErrPrivateLookupIncomplete, errors.New("secret locator and internal address")))
	if result.Code != localv1.ResultCode_RESULT_CODE_NOT_READY || strings.Contains(result.UserMessage, "secret") {
		t.Fatalf("unexpected incomplete discovery response: %v", result)
	}
}
