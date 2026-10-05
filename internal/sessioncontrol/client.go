// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"google.golang.org/protobuf/proto"

	"hydra.local/hydra/internal/tunnellease"
	"hydra.local/hydra/internal/tunnelsignal"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

type DialFunc func(context.Context, string, string) (net.Conn, error)

type Client struct {
	conn     net.Conn
	noise    *noise.SessionTransport
	local    peer.ID
	remote   peer.ID
	lease    tunnellease.Lease
	rotation RotationProposal
	timeout  time.Duration

	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

func Dial(ctx context.Context, address string, privateKey crypto.PrivKey, expectedExit peer.ID, lease tunnellease.Lease, dial DialFunc, timeout time.Duration) (*Client, error) {
	if ctx == nil || address == "" || expectedExit == "" || dial == nil ||
		lease.ExitPeer != expectedExit || lease.Session.IsZero() {
		return nil, ErrInvalid
	}
	transport, err := noiseTransport(privateKey)
	if err != nil {
		return nil, err
	}
	local, err := peer.IDFromPrivateKey(privateKey)
	if err != nil || lease.ClientPeer != local {
		return nil, ErrWrongPeer
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, ErrInvalid
	}
	raw, err := dial(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	secure, err := transport.SecureOutbound(ctx, raw, expectedExit)
	if err != nil {
		raw.Close()
		return nil, err
	}
	client := &Client{conn: secure, noise: transport, local: local, remote: expectedExit, lease: lease, timeout: timeout}
	if _, err := client.exchange(ctx, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_BIND, lease, nil); err != nil {
		secure.Close()
		return nil, err
	}
	return client, nil
}

func (c *Client) Renew(ctx context.Context, lease tunnellease.Lease) (tunnellease.Lease, error) {
	return c.exchange(ctx, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RENEW, lease, nil)
}

// RenewWithRequestID retries the exact request across channels using the Exit's cached result.
func (c *Client) RenewWithRequestID(ctx context.Context, lease tunnellease.Lease, id RequestID) (tunnellease.Lease, error) {
	if id == (RequestID{}) {
		return tunnellease.Lease{}, ErrInvalid
	}
	return c.exchange(ctx, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RENEW, lease, id[:])
}

func (c *Client) State(ctx context.Context, lease tunnellease.Lease) (tunnellease.Lease, error) {
	return c.exchange(ctx, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_STATE_QUERY, lease, nil)
}

func (c *Client) Release(ctx context.Context, lease tunnellease.Lease) error {
	_, err := c.exchange(ctx, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE, lease, nil)
	return err
}

// ReleaseWithRequestID preserves idempotency when a response is lost across Noise reconnection.
func (c *Client) ReleaseWithRequestID(ctx context.Context, lease tunnellease.Lease, id RequestID) error {
	if id == (RequestID{}) {
		return ErrInvalid
	}
	_, err := c.exchange(ctx, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE, lease, id[:])
	return err
}

// RotateWithRequestID stages after READY and moves routes only after old-path COMMIT and a fresh handshake.
func (c *Client) RotateWithRequestID(ctx context.Context, lease tunnellease.Lease, proposal RotationProposal, id RequestID, stage, commit func() error) (tunnellease.Lease, error) {
	if id == (RequestID{}) || stage == nil || commit == nil || proposal.KeyEpoch == 0 || len(proposal.SignedBinding) == 0 || len(proposal.SignedBinding) > MaxSignedAWGBinding {
		return tunnellease.Lease{}, ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || lease.ID != c.lease.ID || lease.Session != c.lease.Session || lease.Generation > c.lease.Generation {
		return tunnellease.Lease{}, ErrStale
	}
	digest := sha256.Sum256(proposal.SignedBinding)
	request := &irviov1.SessionControlRequest{
		ProtocolVersion:       ProtocolVersion,
		Operation:             irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE,
		RequestId:             append([]byte(nil), id[:]...),
		LeaseId:               append([]byte(nil), lease.ID[:]...),
		SessionToken:          append([]byte(nil), lease.Session[:]...),
		ExpectedLeaseRevision: lease.Generation,
		AwgBindingDigest:      append([]byte(nil), digest[:]...),
		AwgKeyEpoch:           proposal.KeyEpoch,
	}
	if err := c.setDeadline(ctx); err != nil {
		return tunnellease.Lease{}, err
	}
	started := time.Now()
	if err := writeFrame(c.conn, requestFrame(request)); err != nil {
		return tunnellease.Lease{}, err
	}
	response, err := c.readResponse(id)
	if err != nil {
		return tunnellease.Lease{}, err
	}
	if response.Operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_ERROR {
		return tunnellease.Lease{}, fmt.Errorf("%w: code %d", ErrRemote, response.GetError().GetCode())
	}
	if response.Operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATED {
		if err := stage(); err != nil {
			return tunnellease.Lease{}, err
		}
		if err := commit(); err != nil {
			return tunnellease.Lease{}, err
		}
		return c.acceptLeaseResponse(response, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATED, lease, started)
	}
	if response.Operation != irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE_READY {
		return tunnellease.Lease{}, ErrInvalid
	}
	if err := stage(); err != nil {
		return tunnellease.Lease{}, err
	}
	commitRequest := proto.Clone(request).(*irviov1.SessionControlRequest)
	commitRequest.Operation = irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE_COMMIT
	if err := writeFrame(c.conn, requestFrame(commitRequest)); err != nil {
		return tunnellease.Lease{}, err
	}
	if err := commit(); err != nil {
		return tunnellease.Lease{}, err
	}
	response, err = c.readResponse(id)
	if err != nil {
		return tunnellease.Lease{}, err
	}
	return c.acceptLeaseResponse(response, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATED, lease, started)
}

func (c *Client) exchange(ctx context.Context, operation irviov1.SessionControlOperation, lease tunnellease.Lease, fixedID []byte) (tunnellease.Lease, error) {
	if ctx == nil {
		return tunnellease.Lease{}, ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return tunnellease.Lease{}, ErrClosed
	}
	if operation != irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_BIND && fixedID == nil &&
		(lease.ID != c.lease.ID || lease.Session != c.lease.Session || lease.Generation != c.lease.Generation) {
		return tunnellease.Lease{}, ErrStale
	}
	if fixedID != nil && (lease.ID != c.lease.ID || lease.Session != c.lease.Session || lease.Generation > c.lease.Generation) {
		return tunnellease.Lease{}, ErrStale
	}
	id, err := NewRequestID()
	if err != nil {
		return tunnellease.Lease{}, err
	}
	if fixedID != nil {
		if len(fixedID) != len(id) {
			return tunnellease.Lease{}, ErrInvalid
		}
		copy(id[:], fixedID)
	}
	request := &irviov1.SessionControlRequest{
		ProtocolVersion: ProtocolVersion, Operation: operation,
		RequestId: append([]byte(nil), id[:]...), LeaseId: append([]byte(nil), lease.ID[:]...),
		SessionToken: append([]byte(nil), lease.Session[:]...), ExpectedLeaseRevision: lease.Generation,
	}
	if err := c.setDeadline(ctx); err != nil {
		return tunnellease.Lease{}, err
	}
	started := time.Now()
	if err := writeFrame(c.conn, requestFrame(request)); err != nil {
		return tunnellease.Lease{}, err
	}
	response, err := c.readResponse(id)
	if err != nil {
		if operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE {
			// Preserve uncertainty so the reconnecting owner can retry RELEASE within its existing budget.
			c.closed = true
			_ = c.conn.Close()
		}
		return tunnellease.Lease{}, err
	}
	expected := map[irviov1.SessionControlOperation]irviov1.SessionControlOperation{
		irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_BIND:        irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_BOUND,
		irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RENEW:       irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RENEWED,
		irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_STATE_QUERY: irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_STATE,
		irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE:     irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE_ACCEPTED,
	}[operation]
	if operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE {
		if response.Operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_ERROR {
			return tunnellease.Lease{}, fmt.Errorf("%w: code %d", ErrRemote, response.GetError().GetCode())
		}
		if response.Operation != expected {
			return tunnellease.Lease{}, ErrInvalid
		}
		// Close the authenticated channel after RELEASE_ACCEPTED so the Exit can revoke its carrying route.
		c.closed = true
		return tunnellease.Lease{}, c.conn.Close()
	}
	return c.acceptLeaseResponse(response, expected, lease, started)
}

func (c *Client) setDeadline(ctx context.Context) error {
	deadline := time.Now().Add(c.timeout)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	return c.conn.SetDeadline(deadline)
}

func (c *Client) readResponse(id RequestID) (*irviov1.SessionControlResponse, error) {
	frame, _, err := readFrame(c.conn)
	if err != nil {
		return nil, err
	}
	response := frame.GetResponse()
	if response == nil || response.ProtocolVersion != ProtocolVersion || !bytes.Equal(response.RequestId, id[:]) {
		return nil, ErrInvalid
	}
	return response, nil
}

func (c *Client) acceptLeaseResponse(response *irviov1.SessionControlResponse, expected irviov1.SessionControlOperation, lease tunnellease.Lease, started time.Time) (tunnellease.Lease, error) {
	if response.Operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_ERROR {
		return tunnellease.Lease{}, fmt.Errorf("%w: code %d", ErrRemote, response.GetError().GetCode())
	}
	if response.Operation != expected {
		return tunnellease.Lease{}, ErrInvalid
	}
	renewed, err := tunnelsignal.LeaseFromProto(response.Lease, c.remote, c.local, time.Now().UTC())
	if err != nil || renewed.ID != lease.ID || renewed.Session != lease.Session || renewed.Generation < lease.Generation {
		return tunnellease.Lease{}, ErrInvalid
	}
	remaining := time.Duration(response.AuthoritativeRemainingNanos) - time.Since(started)
	if remaining <= 0 {
		return tunnellease.Lease{}, ErrStale
	}
	conservative := time.Now().Add(remaining)
	if conservative.Before(renewed.ValidUntil) {
		renewed.ValidUntil = conservative
	}
	c.lease = renewed
	if expected == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATED {
		c.rotation = RotationProposal{}
	} else if len(response.SignedAwgBinding) != 0 || response.AwgKeyEpoch != 0 {
		if expected != irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RENEWED ||
			len(response.SignedAwgBinding) == 0 || len(response.SignedAwgBinding) > MaxSignedAWGBinding || response.AwgKeyEpoch == 0 {
			return tunnellease.Lease{}, ErrInvalid
		}
		c.rotation = RotationProposal{SignedBinding: append([]byte(nil), response.SignedAwgBinding...), KeyEpoch: response.AwgKeyEpoch}
	}
	return renewed, nil
}

func requestFrame(request *irviov1.SessionControlRequest) *irviov1.SessionControlFrame {
	return &irviov1.SessionControlFrame{Message: &irviov1.SessionControlFrame_Request{Request: request}}
}

func (c *Client) PendingRotation() RotationProposal {
	c.mu.Lock()
	defer c.mu.Unlock()
	return RotationProposal{SignedBinding: append([]byte(nil), c.rotation.SignedBinding...), KeyEpoch: c.rotation.KeyEpoch}
}

func (c *Client) Current() tunnellease.Lease {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lease
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		_ = c.conn.Close()
	})
	return nil
}
