// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

// Hold the caller after its successful write until the reader has processed both reply and EOF.
type terminalResponseBarrier struct {
	net.Conn
	done <-chan struct{}
}

func (c terminalResponseBarrier) SetWriteDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		<-c.done
	}
	return c.Conn.SetWriteDeadline(deadline)
}

func TestClientTerminalResponsePrecedesEOF(t *testing.T) {
	for _, code := range []localv1.ResultCode{localv1.ResultCode_RESULT_CODE_OK, localv1.ResultCode_RESULT_CODE_UNSUPPORTED_API} {
		for iteration := 0; iteration < 32; iteration++ {
			left, right := net.Pipe()
			client := &Client{major: APIMajor, minor: APIMinor, pending: make(map[uint64]chan callResult),
				events: make(chan *localv1.Event, 1), done: make(chan struct{})}
			client.conn = terminalResponseBarrier{Conn: left, done: client.done}
			go client.readLoop()
			serverDone := make(chan error, 1)
			go func() {
				defer right.Close()
				request, err := ReadFrame(right)
				if err == nil {
					err = WriteFrame(right, responseEnvelope(request.RequestId, code, "terminal reply"))
				}
				serverDone <- err
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			response, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_HELLO})
			cancel()
			client.Close()
			if serverErr := <-serverDone; serverErr != nil {
				t.Fatal(serverErr)
			}
			if code == localv1.ResultCode_RESULT_CODE_OK {
				if err != nil || response == nil || response.UserMessage != "terminal reply" {
					t.Fatalf("lost terminal success: %v %v", response, err)
				}
			} else {
				var apiErr *ResponseError
				if !errors.As(err, &apiErr) || apiErr.Code != code {
					t.Fatalf("lost terminal error: %v", err)
				}
			}
		}
	}
}

func TestClientEOFWithoutReplyCompletesPendingCall(t *testing.T) {
	left, right := net.Pipe()
	client := &Client{conn: left, major: APIMajor, minor: APIMinor, pending: make(map[uint64]chan callResult),
		events: make(chan *localv1.Event, 1), done: make(chan struct{})}
	defer client.Close()
	go client.readLoop()
	go func() { defer right.Close(); _, _ = ReadFrame(right) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_HELLO}); !errors.Is(err, io.EOF) {
		t.Fatalf("closed server did not complete the pending call: %v", err)
	}
}
