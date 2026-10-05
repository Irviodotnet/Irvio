// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnelsignal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/tunnellease"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

type RemoteError struct {
	Code irviov1.TunnelLeaseErrorCode
}

func (e *RemoteError) Error() string {
	if e == nil {
		return ErrRemote.Error()
	}
	return fmt.Sprintf("%s: code %d", ErrRemote, e.Code)
}

func (e *RemoteError) Unwrap() error { return ErrRemote }

func IsTerminalOwnershipError(err error) bool {
	var remote *RemoteError
	if !errors.As(err, &remote) {
		return false
	}
	switch remote.Code {
	case irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_STALE,
		irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_EXPIRED,
		irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_NOT_FOUND:
		return true
	default:
		return false
	}
}

type Client struct {
	host    host.Host
	timeout time.Duration
}

func NewClient(host host.Host, timeout time.Duration) (*Client, error) {
	if host == nil {
		return nil, ErrInvalidMessage
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, ErrInvalidMessage
	}
	return &Client{host: host, timeout: timeout}, nil
}

func (c *Client) Acquire(ctx context.Context, exit peer.ID) (tunnellease.Lease, error) {
	return c.acquire(ctx, exit, tunnellease.SessionToken{})
}

func (c *Client) AcquireForSession(ctx context.Context, exit peer.ID, token tunnellease.SessionToken) (tunnellease.Lease, error) {
	if token.IsZero() {
		return tunnellease.Lease{}, ErrInvalidMessage
	}
	return c.acquire(ctx, exit, token)
}

func (c *Client) acquire(ctx context.Context, exit peer.ID, token tunnellease.SessionToken) (tunnellease.Lease, error) {
	response, err := c.exchange(ctx, exit, request(
		irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_ACQUIRE, exit, c.host.ID(), tunnellease.ID{}, 0, token,
	))
	if err != nil {
		return tunnellease.Lease{}, err
	}
	lease, err := leaseFromProto(response.GetLease(), exit, c.host.ID(), time.Now().UTC())
	if err == nil && lease.Session != token {
		return tunnellease.Lease{}, ErrInvalidMessage
	}
	return lease, err
}

// Renew extends a live authenticated lease without changing its addresses.
func (c *Client) Renew(ctx context.Context, lease tunnellease.Lease) (tunnellease.Lease, error) {
	if lease.ClientPeer != c.host.ID() || lease.ExitPeer == "" || lease.Generation == 0 {
		return tunnellease.Lease{}, ErrWrongPeer
	}
	response, err := c.exchange(ctx, lease.ExitPeer, request(
		irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_RENEW,
		lease.ExitPeer, c.host.ID(), lease.ID, lease.Generation, lease.Session,
	))
	if err != nil {
		return tunnellease.Lease{}, err
	}
	return leaseFromProto(response.GetLease(), lease.ExitPeer, c.host.ID(), time.Now().UTC())
}

func (c *Client) Release(ctx context.Context, lease tunnellease.Lease) error {
	if lease.ClientPeer != c.host.ID() || lease.ExitPeer == "" || lease.Generation == 0 {
		return ErrWrongPeer
	}
	response, err := c.exchange(ctx, lease.ExitPeer, request(
		irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_RELEASE,
		lease.ExitPeer, c.host.ID(), lease.ID, lease.Generation, lease.Session,
	))
	if err != nil {
		return err
	}
	ack := response.GetLease()
	if ack == nil || len(ack.LeaseId) != len(lease.ID) || string(ack.LeaseId) != string(lease.ID[:]) ||
		ack.Generation != lease.Generation {
		return ErrInvalidMessage
	}
	if len(ack.SessionToken) != len(lease.Session) || string(ack.SessionToken) != string(lease.Session[:]) {
		if !lease.Session.IsZero() {
			return ErrInvalidMessage
		}
	}
	return nil
}

func (c *Client) exchange(ctx context.Context, remote peer.ID, request *irviov1.TunnelLeaseRequest) (*irviov1.TunnelLeaseResponse, error) {
	if ctx == nil || remote == "" || remote == c.host.ID() {
		return nil, ErrWrongPeer
	}
	stream, err := c.host.NewStream(ctx, remote, ProtocolID)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	if stream.Conn().RemotePeer() != remote {
		_ = stream.Reset()
		return nil, ErrWrongPeer
	}
	deadline := time.Now().Add(c.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := stream.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	defer stop()
	if err := writeFrame(stream, request); err != nil {
		return nil, err
	}
	response := new(irviov1.TunnelLeaseResponse)
	if err := readFrame(stream, response); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if response.ProtocolVersion != ProtocolVersion {
		return nil, ErrInvalidMessage
	}
	if remoteError := response.GetError(); remoteError != nil {
		return nil, &RemoteError{Code: remoteError.Code}
	}
	if response.GetLease() == nil {
		return nil, ErrInvalidMessage
	}
	return response, nil
}
