// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package reachability

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/proto"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type ClientConfig struct {
	Host       host.Host
	Requester  RequestStreamOpener
	AttemptTTL time.Duration
	Now        func() time.Time
	Random     io.Reader
	MaxPending int
}

type RequestStreamOpener interface {
	Open(context.Context, peer.ID, irviov1.ReachabilityFamily) (network.Stream, io.Closer, error)
}

type pendingAttempt struct {
	verifier peer.ID
	expires  time.Time
	used     bool
}

type Client struct {
	config ClientConfig

	mu      sync.Mutex
	pending map[AttemptID]*pendingAttempt
	closed  bool
	wait    sync.WaitGroup
	once    sync.Once
}

func NewClient(config ClientConfig) (*Client, error) {
	if config.Host == nil {
		return nil, ErrInvalidConfig
	}
	if config.AttemptTTL == 0 {
		config.AttemptTTL = DefaultAttemptTTL
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.MaxPending == 0 {
		config.MaxPending = DefaultVerifierLimit
	}
	if config.AttemptTTL <= 0 || config.AttemptTTL > time.Minute || config.MaxPending <= 0 || config.MaxPending > DefaultMaxActive {
		return nil, ErrInvalidConfig
	}
	client := &Client{config: config, pending: make(map[AttemptID]*pendingAttempt)}
	config.Host.SetStreamHandler(libprotocol.ID(ChallengeProtocolID), client.handleChallenge)
	return client, nil
}

func (c *Client) Request(ctx context.Context, verifier peer.ID, family irviov1.ReachabilityFamily, externalPort uint16, networkGeneration, publicEpoch uint64) (*irviov1.SignedReachabilityAttestation, error) {
	if ctx == nil || verifier == "" || verifier == c.config.Host.ID() || externalPort == 0 ||
		networkGeneration == 0 || publicEpoch == 0 ||
		family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4 && family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6 {
		return nil, ErrInvalidConfig
	}
	id, err := c.newAttempt(verifier)
	if err != nil {
		return nil, fmt.Errorf("open verifier request: %w", err)
	}
	defer c.remove(id)
	var stream network.Stream
	var owner io.Closer
	if c.config.Requester != nil {
		stream, owner, err = c.config.Requester.Open(ctx, verifier, family)
	} else {
		stream, err = c.config.Host.NewStream(ctx, verifier, libprotocol.ID(ProtocolID))
	}
	if err != nil {
		return nil, err
	}
	if owner != nil {
		defer owner.Close()
	}
	defer stream.Close()
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	defer stop()
	deadline := c.config.Now().Add(c.config.AttemptTTL)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := stream.SetDeadline(deadline); err != nil {
		return nil, err
	}
	request := &irviov1.ReachabilityRequest{
		ProtocolVersion: ProtocolVersion, SubjectPeerId: []byte(c.config.Host.ID()),
		AttemptId: append([]byte(nil), id[:]...), Family: family,
		ExternalControlPort: uint32(externalPort), NetworkGeneration: networkGeneration, PublicEpoch: publicEpoch,
	}
	if err := writeFrame(stream, request); err != nil {
		return nil, err
	}
	response := new(irviov1.ReachabilityResponse)
	if err := readFrame(stream, response); err != nil {
		return nil, err
	}
	if response.ProtocolVersion != ProtocolVersion {
		return nil, ErrInvalidMessage
	}
	if code := response.GetError(); code != irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_UNSPECIFIED {
		return nil, fmt.Errorf("fresh inbound challenge: %w", remoteError(code))
	}
	signed := response.GetAttestation()
	value, err := VerifyAttestation(signed, c.config.Now().UTC())
	if err != nil {
		return nil, err
	}
	subject, _ := peer.IDFromBytes(value.SubjectPeerId)
	valueVerifier, _ := peer.IDFromBytes(value.VerifierPeerId)
	if subject != c.config.Host.ID() || valueVerifier != verifier || !bytes.Equal(value.AttemptId, id[:]) ||
		value.Family != family || value.ExternalControlPort != uint32(externalPort) ||
		value.NetworkGeneration != networkGeneration || value.PublicEpoch != publicEpoch || !c.challengeConsumed(id) {
		return nil, ErrIdentityMismatch
	}
	return proto.Clone(signed).(*irviov1.SignedReachabilityAttestation), nil
}

func (c *Client) newAttempt(verifier peer.ID) (AttemptID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || len(c.pending) >= c.config.MaxPending {
		return AttemptID{}, ErrRateLimited
	}
	for tries := 0; tries < 4; tries++ {
		var id AttemptID
		if _, err := io.ReadFull(c.config.Random, id[:]); err != nil {
			return AttemptID{}, err
		}
		if id.IsZero() {
			continue
		}
		if _, exists := c.pending[id]; !exists {
			c.pending[id] = &pendingAttempt{verifier: verifier, expires: c.config.Now().Add(c.config.AttemptTTL)}
			return id, nil
		}
	}
	return AttemptID{}, ErrReplay
}

func (c *Client) remove(id AttemptID) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) handleChallenge(stream network.Stream) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = stream.Reset()
		return
	}
	c.wait.Add(1)
	c.mu.Unlock()
	defer c.wait.Done()
	defer stream.Close()
	_ = stream.SetDeadline(c.config.Now().Add(c.config.AttemptTTL))
	challenge := new(irviov1.ReachabilityChallenge)
	if err := readFrame(stream, challenge); err != nil || challenge.ProtocolVersion != ProtocolVersion ||
		len(challenge.AttemptId) != AttemptIDSize || len(challenge.Nonce) != NonceSize {
		_ = stream.Reset()
		return
	}
	var id AttemptID
	copy(id[:], challenge.AttemptId)
	verifier, err := peer.IDFromBytes(challenge.VerifierPeerId)
	if err != nil || verifier != stream.Conn().RemotePeer() {
		_ = stream.Reset()
		return
	}
	valid := c.consumeChallenge(id, verifier, c.config.Now())
	if !valid {
		_ = stream.Reset()
		return
	}
	_ = writeFrame(stream, &irviov1.ReachabilityChallengeResponse{
		ProtocolVersion: ProtocolVersion, AttemptId: append([]byte(nil), id[:]...), Nonce: append([]byte(nil), challenge.Nonce...),
	})
}

func (c *Client) consumeChallenge(id AttemptID, verifier peer.ID, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	pending := c.pending[id]
	valid := pending != nil && pending.verifier == verifier && !pending.used && pending.expires.After(now)
	if valid {
		pending.used = true
	}
	return valid
}

func (c *Client) challengeConsumed(id AttemptID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	pending := c.pending[id]
	return pending != nil && pending.used
}

func remoteError(code irviov1.ReachabilityErrorCode) error {
	switch code {
	case irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_INVALID_REQUEST:
		return ErrInvalidMessage
	case irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_WRONG_PEER:
		return ErrWrongPeer
	case irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_RATE_LIMITED:
		return ErrRateLimited
	case irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_UNREACHABLE:
		return ErrUnreachable
	case irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_IDENTITY_MISMATCH:
		return ErrIdentityMismatch
	default:
		return ErrInvalidMessage
	}
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.config.Host.RemoveStreamHandler(libprotocol.ID(ChallengeProtocolID))
		c.pending = nil
		c.mu.Unlock()
		c.wait.Wait()
	})
	return nil
}
