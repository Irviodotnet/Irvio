// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/tunnellease"
)

type ReconnectingConfig struct {
	Address      string
	PrivateKey   crypto.PrivKey
	ExpectedExit peer.ID
	Lease        tunnellease.Lease
	Dial         DialFunc
	Timeout      time.Duration
	RetryDelay   time.Duration
}

// ReconnectingClient retries exact mutations over the same AWG lease, never through DHT or public control.
type ReconnectingClient struct {
	config ReconnectingConfig

	mu        sync.Mutex
	client    *Client
	lease     tunnellease.Lease
	closed    bool
	closeOnce sync.Once
}

func DialReconnecting(ctx context.Context, config ReconnectingConfig) (*ReconnectingClient, error) {
	if ctx == nil || config.Address == "" || config.PrivateKey == nil || config.ExpectedExit == "" || config.Dial == nil {
		return nil, ErrInvalid
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = 50 * time.Millisecond
	}
	if config.Timeout <= 0 || config.Timeout > time.Minute || config.RetryDelay <= 0 || config.RetryDelay > time.Second {
		return nil, ErrInvalid
	}
	owner := &ReconnectingClient{config: config, lease: config.Lease}
	owner.mu.Lock()
	err := owner.connectLocked(ctx)
	owner.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return owner, nil
}

func (c *ReconnectingClient) connectLocked(ctx context.Context) error {
	if c.closed {
		return ErrClosed
	}
	deadline := time.Now().Add(c.config.Timeout)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	attemptContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var lastErr error
	for {
		client, err := Dial(
			attemptContext, c.config.Address, c.config.PrivateKey, c.config.ExpectedExit,
			c.lease, c.config.Dial, c.config.Timeout,
		)
		if err == nil {
			c.client = client
			return nil
		}
		lastErr = err
		timer := time.NewTimer(c.config.RetryDelay)
		select {
		case <-attemptContext.Done():
			timer.Stop()
			return errors.Join(lastErr, attemptContext.Err())
		case <-timer.C:
		}
	}
}

func (c *ReconnectingClient) reconnectLocked(ctx context.Context) error {
	if c.client != nil {
		_ = c.client.Close()
		c.client = nil
	}
	return c.connectLocked(ctx)
}

func (c *ReconnectingClient) Renew(ctx context.Context, lease tunnellease.Lease) (tunnellease.Lease, error) {
	if ctx == nil {
		return tunnellease.Lease{}, ErrInvalid
	}
	id, err := NewRequestID()
	if err != nil {
		return tunnellease.Lease{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return tunnellease.Lease{}, ErrClosed
	}
	if c.client == nil {
		if err := c.connectLocked(ctx); err != nil {
			return tunnellease.Lease{}, err
		}
	}
	renewed, firstErr := c.client.RenewWithRequestID(ctx, lease, id)
	if firstErr == nil {
		c.lease = renewed
		return renewed, nil
	}
	if err := c.reconnectLocked(ctx); err != nil {
		return tunnellease.Lease{}, errors.Join(firstErr, err)
	}
	renewed, err = c.client.RenewWithRequestID(ctx, lease, id)
	if err == nil {
		c.lease = renewed
	}
	return renewed, err
}

func (c *ReconnectingClient) PendingRotation() RotationProposal {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		return RotationProposal{}
	}
	return c.client.PendingRotation()
}

// Rotate retries one proposal/request ID; stage and commit must tolerate the new key already owning the path.
func (c *ReconnectingClient) Rotate(ctx context.Context, lease tunnellease.Lease, proposal RotationProposal, stage, commit func() error) (tunnellease.Lease, error) {
	if ctx == nil {
		return tunnellease.Lease{}, ErrInvalid
	}
	id, err := NewRequestID()
	if err != nil {
		return tunnellease.Lease{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return tunnellease.Lease{}, ErrClosed
	}
	if c.client == nil {
		if err := c.connectLocked(ctx); err != nil {
			return tunnellease.Lease{}, err
		}
	}
	// Give rotation its full remaining budget to avoid stranding peers on different keys.
	rotated, firstErr := c.client.RotateWithRequestID(ctx, lease, proposal, id, stage, commit)
	if firstErr == nil {
		c.lease = rotated
		return rotated, nil
	}
	if err := c.reconnectLocked(ctx); err != nil {
		return tunnellease.Lease{}, errors.Join(firstErr, err)
	}
	rotated, err = c.client.RotateWithRequestID(ctx, lease, proposal, id, stage, commit)
	if err == nil {
		c.lease = rotated
	}
	return rotated, err
}

func (c *ReconnectingClient) Release(ctx context.Context, lease tunnellease.Lease) error {
	if ctx == nil {
		return ErrInvalid
	}
	id, err := NewRequestID()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	operationContext, operationCancel := c.operationContext(ctx)
	defer operationCancel()
	if c.client == nil {
		if err := c.connectLocked(operationContext); err != nil {
			return err
		}
	}
	firstContext, firstCancel := firstAttemptContext(operationContext)
	firstErr := c.client.ReleaseWithRequestID(firstContext, lease, id)
	firstCancel()
	if firstErr == nil {
		return nil
	}
	if err := c.reconnectLocked(operationContext); err != nil {
		return errors.Join(firstErr, err)
	}
	return c.client.ReleaseWithRequestID(operationContext, lease, id)
}

func (c *ReconnectingClient) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(c.config.Timeout)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	return context.WithDeadline(ctx, deadline)
}

func firstAttemptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return context.WithDeadline(ctx, deadline)
	}
	// Reserve one third of the operation budget for fresh BIND and exact retry after a silent blackhole.
	return context.WithDeadline(ctx, time.Now().Add(remaining*2/3))
}

func (c *ReconnectingClient) Current() tunnellease.Lease {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lease
}

func (c *ReconnectingClient) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		if c.client != nil {
			_ = c.client.Close()
			c.client = nil
		}
		c.mu.Unlock()
	})
	return nil
}

var _ tunnellease.RenewalClient = (*ReconnectingClient)(nil)
