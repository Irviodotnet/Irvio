// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func TestReleaseKeepsRouteUntilNoiseClientClosesOrDeadline(t *testing.T) {
	for _, closeClient := range []bool{true, false} {
		name := "deadline"
		if closeClient {
			name = "client_close"
		}
		t.Run(name, func(t *testing.T) {
			exitKey, exitPeer := sessionIdentity(t)
			clientKey, clientPeer := sessionIdentity(t)
			authority := &testAuthority{lease: testLease(exitPeer, clientPeer), releaseDone: make(chan struct{})}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server, err := NewServer(listener, exitKey, authority, 500*time.Millisecond)
			if err != nil {
				_ = listener.Close()
				t.Fatal(err)
			}
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client, err := Dial(ctx, listener.Addr().String(), clientKey, exitPeer, authority.lease,
				(&net.Dialer{}).DialContext, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			id, err := NewRequestID()
			if err != nil {
				t.Fatal(err)
			}
			lease := authority.lease
			request := &irviov1.SessionControlRequest{
				ProtocolVersion: ProtocolVersion, Operation: irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE,
				RequestId: id[:], LeaseId: lease.ID[:], SessionToken: lease.Session[:], ExpectedLeaseRevision: lease.Generation,
			}
			if err := writeFrame(client.conn, requestFrame(request)); err != nil {
				t.Fatal(err)
			}
			response, err := client.readResponse(id)
			if err != nil || response.GetOperation() != irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE_ACCEPTED {
				t.Fatalf("release acknowledgment: response=%v err=%v", response, err)
			}
			select {
			case <-authority.releaseDone:
				t.Fatal("Exit revoked the carrying route before client close or deadline")
			case <-time.After(100 * time.Millisecond):
			}
			if closeClient {
				_ = client.Close()
			}
			select {
			case <-authority.releaseDone:
			case <-ctx.Done():
				t.Fatal("Exit failed to bound release after client close/deadline")
			}
		})
	}
}

type releaseCloseErrorConn struct{ net.Conn }

func (c releaseCloseErrorConn) Close() error {
	_ = c.Conn.Close()
	return errors.New("injected transport close error after authenticated release response")
}

func TestAcceptedReleaseDoesNotRetryOnTransportCloseError(t *testing.T) {
	exitKey, exitPeer := sessionIdentity(t)
	clientKey, clientPeer := sessionIdentity(t)
	authority := &testAuthority{lease: testLease(exitPeer, clientPeer), releaseDone: make(chan struct{})}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(listener, exitKey, authority, 500*time.Millisecond)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	dials := 0
	client, err := DialReconnecting(ctx, ReconnectingConfig{
		Address: listener.Addr().String(), PrivateKey: clientKey, ExpectedExit: exitPeer, Lease: authority.lease,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			dials++
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}, Timeout: 500 * time.Millisecond, RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.client.conn = releaseCloseErrorConn{client.client.conn}
	if err := client.Release(ctx, authority.lease); err != nil {
		t.Fatalf("authenticated RELEASE_ACCEPTED was treated as unconfirmed: %v", err)
	}
	select {
	case <-authority.releaseDone:
	case <-ctx.Done():
		t.Fatal("Exit did not revoke the acknowledged lease")
	}
	if dials != 1 {
		t.Fatalf("acknowledged release retried against a removed lease: dials=%d", dials)
	}
}
