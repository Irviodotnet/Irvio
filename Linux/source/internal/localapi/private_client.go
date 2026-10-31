// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func (client *Client) SupportsPrivateNetwork() bool {
	return client.hello != nil && client.hello.NegotiatedApiMinor >= 6 && client.hello.PrivateNetworkSupported
}

func (client *Client) GetPrivateState(ctx context.Context, offset, limit uint32) (*localv1.PrivateState, error) {
	if !client.SupportsPrivateNetwork() {
		return nil, &ResponseError{Code: localv1.ResultCode_RESULT_CODE_UNSUPPORTED, Message: "Private IRVIO is unavailable"}
	}
	result, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_GET_PRIVATE_STATE, GetPrivateState: &localv1.GetPrivateStateRequest{Offset: offset, Limit: limit}})
	if err != nil {
		return nil, err
	}
	if result.GetPrivateState() == nil {
		return nil, ErrUnexpectedMessage
	}
	return result.GetPrivateState(), nil
}

func (client *Client) PrivateCommand(ctx context.Context, command *localv1.PrivateCommand) (*localv1.PrivateCommandResult, error) {
	if !client.SupportsPrivateNetwork() {
		return nil, &ResponseError{Code: localv1.ResultCode_RESULT_CODE_UNSUPPORTED, Message: "Private IRVIO is unavailable"}
	}
	if err := ValidatePrivateCommand(command); err != nil {
		return nil, err
	}
	result, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_PRIVATE_COMMAND, PrivateCommand: command})
	if err != nil {
		return nil, err
	}
	if result.GetPrivateResult() == nil {
		return nil, ErrUnexpectedMessage
	}
	return result.GetPrivateResult(), nil
}
