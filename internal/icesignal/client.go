// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesignal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/icesession"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

// Client opens authenticated one-shot signaling streams.
type Client struct {
	host      host.Host
	config    Config
	closed    atomic.Bool
	closeOnce sync.Once
	context   context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	streams   map[network.Stream]struct{}
}

// NewClient creates a client without opening streams.
func NewClient(h host.Host, config Config) (*Client, error) {
	if h == nil {
		return nil, fmt.Errorf("create ICE signaling client: nil host")
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("create ICE signaling client: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{host: h, config: config, context: ctx, cancel: cancel, streams: make(map[network.Stream]struct{})}, nil
}

// Exchange validates Offer/Answer without owning the local session or loading remote candidates.
func (c *Client) Exchange(ctx context.Context, remote peer.ID, local *icesession.PreparedSession) (ExchangeResult, error) {
	if ctx == nil {
		return ExchangeResult{}, fmt.Errorf("exchange ICE description: nil context")
	}
	if err := ctx.Err(); err != nil {
		return ExchangeResult{}, err
	}
	if c.closed.Load() {
		return ExchangeResult{}, ErrClosed
	}
	if remote == "" || remote == c.host.ID() {
		return ExchangeResult{}, ErrWrongPeer
	}
	if local == nil {
		return ExchangeResult{}, fmt.Errorf("exchange ICE description: nil prepared session")
	}
	description, err := icesession.ValidateDescription(local.LocalDescription(), c.config.Validation)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("exchange ICE description: %w", err)
	}
	sessionID, err := icesession.NewSessionID()
	if err != nil {
		return ExchangeResult{}, err
	}
	now := time.Now().Truncate(time.Second)
	offer := Offer{
		SessionID: sessionID, Initiator: c.host.ID(), Responder: remote,
		Purpose: icesession.PurposeExit, Role: icesession.RoleControlling,
		Description: description, CreatedAt: now, ExpiresAt: now.Add(c.config.DescriptionTTL),
	}
	result, err := c.exchangeOffer(ctx, remote, offer)
	if err != nil {
		return ExchangeResult{}, err
	}
	if err := local.BindConnectivity(icesession.ConnectivityBinding{
		SessionID: result.SessionID, Initiator: result.Initiator, Responder: result.Responder,
		LocalPeer: c.host.ID(), RemotePeer: result.RemotePeer, Purpose: result.Purpose,
		LocalRole: result.LocalRole, RemoteRole: result.RemoteRole,
	}); err != nil {
		return ExchangeResult{}, fmt.Errorf("bind prepared ICE session to signaling exchange: %w", err)
	}
	return result, nil
}

func (c *Client) exchangeOffer(ctx context.Context, remote peer.ID, offer Offer) (ExchangeResult, error) {
	stream, err := c.host.NewStream(ctx, remote, ProtocolID)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("open ICE signaling stream: %w", err)
	}
	if stream.Conn().RemotePeer() != remote {
		_ = stream.Reset()
		return ExchangeResult{}, ErrWrongPeer
	}
	if !c.track(stream) {
		_ = stream.Reset()
		return ExchangeResult{}, ErrClosed
	}
	defer c.untrack(stream)
	closed := false
	defer func() {
		if !closed {
			_ = stream.Reset()
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	defer stop()
	stopClient := context.AfterFunc(c.context, func() { _ = stream.Reset() })
	defer stopClient()
	deadline := time.Now().Add(c.config.StreamTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := stream.SetDeadline(deadline); err != nil {
		return ExchangeResult{}, fmt.Errorf("set ICE signaling deadline: %w", err)
	}
	if err := writeFrame(stream, offerToProto(offer)); err != nil {
		return ExchangeResult{}, err
	}
	response := new(irviov1.IceSignalResponse)
	if err := readFrame(stream, response); err != nil {
		if ctx.Err() != nil {
			return ExchangeResult{}, ctx.Err()
		}
		if c.context.Err() != nil {
			return ExchangeResult{}, ErrClosed
		}
		return ExchangeResult{}, err
	}
	switch result := response.Result.(type) {
	case *irviov1.IceSignalResponse_Error:
		if result.Error == nil {
			return ExchangeResult{}, ErrInvalidMessage
		}
		message := result.Error.Message
		if len(message) == 0 || len(message) > maxErrorMessageBytes {
			message = "remote request failed"
		}
		return ExchangeResult{}, &RemoteError{Code: errorCodeFromProto(result.Error.Code), Message: message}
	case *irviov1.IceSignalResponse_Answer:
		if err := validateAnswerWireVersion(result.Answer); err != nil {
			return ExchangeResult{}, err
		}
		answer, err := answerFromProto(result.Answer, c.config.Validation)
		if err != nil {
			return ExchangeResult{}, err
		}
		if err := validateAnswer(answer, offer, stream.Conn().RemotePeer(), c.host.ID(), time.Now(), c.config); err != nil {
			return ExchangeResult{}, err
		}
		closed = true
		// Cleanup errors must not invalidate an already authenticated, fully validated Answer.
		_ = stream.Close()
		return ExchangeResult{
			SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: offer.Responder,
			RemotePeer: remote, RemoteDescription: answer.Description.Clone(),
			Purpose: offer.Purpose, LocalRole: offer.Role, RemoteRole: answer.Role,
		}, nil
	default:
		return ExchangeResult{}, ErrInvalidMessage
	}
}

// Close cancels in-flight streams without closing the libp2p host.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.cancel()
		c.mu.Lock()
		streams := make([]network.Stream, 0, len(c.streams))
		for stream := range c.streams {
			streams = append(streams, stream)
		}
		c.mu.Unlock()
		for _, stream := range streams {
			_ = stream.Reset()
		}
	})
	return nil
}

func (c *Client) track(stream network.Stream) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return false
	}
	c.streams[stream] = struct{}{}
	return true
}
func (c *Client) untrack(stream network.Stream) {
	c.mu.Lock()
	delete(c.streams, stream)
	c.mu.Unlock()
}

// Initiate prepares a local session and closes it automatically on exchange failure.
func (c *Client) Initiate(ctx context.Context, remote peer.ID, preparer SessionPreparer) (*icesession.PreparedSession, ExchangeResult, error) {
	if preparer == nil {
		return nil, ExchangeResult{}, fmt.Errorf("initiate ICE description: nil preparer")
	}
	local, err := preparer.Prepare(ctx)
	if err != nil {
		return nil, ExchangeResult{}, err
	}
	result, err := c.Exchange(ctx, remote, local)
	if err != nil {
		return nil, ExchangeResult{}, errors.Join(err, local.Close())
	}
	return local, result, nil
}
