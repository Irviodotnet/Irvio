// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package underlay

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	multiaddr "github.com/multiformats/go-multiaddr"
)

const libp2pChallengeProtocol libprotocol.ID = "/hydra/underlay/1.0.0"

// Libp2pChallengeProbe borrows the authenticated host and its socket-mark ownership.
type Libp2pChallengeProbe struct {
	host   host.Host
	random io.Reader
}

func NewLibp2pChallengeProbe(owner host.Host) (*Libp2pChallengeProbe, error) {
	if owner == nil {
		return nil, ErrInvalidConfig
	}
	return &Libp2pChallengeProbe{host: owner, random: rand.Reader}, nil
}

func (p *Libp2pChallengeProbe) Probe(ctx context.Context, target Target) error {
	if ctx == nil || target.PeerID == "" || target.PeerID == p.host.ID() || !target.Endpoint.IsValid() || target.Endpoint.Port() == 0 {
		return ErrInvalidTarget
	}
	address, err := challengeMultiaddr(target.Endpoint)
	if err != nil {
		return err
	}
	p.host.Peerstore().AddAddr(target.PeerID, address, time.Minute)
	if err := p.host.Connect(ctx, peer.AddrInfo{ID: target.PeerID, Addrs: []multiaddr.Multiaddr{address}}); err != nil {
		return err
	}
	stream, err := p.host.NewStream(ctx, target.PeerID, libp2pChallengeProtocol)
	if err != nil {
		return err
	}
	defer stream.Close()
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := stream.SetDeadline(deadline); err != nil {
			return err
		}
	}
	challenge := make([]byte, challengeSize)
	copy(challenge, challengeMagic[:])
	if _, err := io.ReadFull(p.random, challenge[len(challengeMagic):]); err != nil {
		return err
	}
	if err := writeAll(stream, challenge); err != nil {
		return err
	}
	response := make([]byte, len(challenge))
	if _, err := io.ReadFull(stream, response); err != nil {
		return err
	}
	if !bytes.Equal(response, challenge) {
		return ErrChallengeMismatch
	}
	return nil
}

func challengeMultiaddr(endpoint netip.AddrPort) (multiaddr.Multiaddr, error) {
	protocolName := "ip6"
	if endpoint.Addr().Is4() {
		protocolName = "ip4"
	}
	value, err := multiaddr.NewMultiaddr(fmt.Sprintf("/%s/%s/tcp/%d", protocolName, endpoint.Addr(), endpoint.Port()))
	if err != nil {
		return nil, ErrInvalidTarget
	}
	return value, nil
}

// Libp2pChallengeService emits one bounded nonce response per authenticated request.
type Libp2pChallengeService struct {
	host      host.Host
	mu        sync.Mutex
	closed    bool
	wait      sync.WaitGroup
	closeOnce sync.Once
}

func NewLibp2pChallengeService(owner host.Host) (*Libp2pChallengeService, error) {
	if owner == nil {
		return nil, ErrInvalidConfig
	}
	service := &Libp2pChallengeService{host: owner}
	owner.SetStreamHandler(libp2pChallengeProtocol, service.handle)
	return service, nil
}

func (s *Libp2pChallengeService) handle(stream network.Stream) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = stream.Reset()
		return
	}
	s.wait.Add(1)
	s.mu.Unlock()
	defer s.wait.Done()
	defer stream.Close()
	request := make([]byte, challengeSize)
	if _, err := io.ReadFull(stream, request); err != nil || !bytes.Equal(request[:len(challengeMagic)], challengeMagic[:]) {
		_ = stream.Reset()
		return
	}
	_ = writeAll(stream, request)
}

func (s *Libp2pChallengeService) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.host.RemoveStreamHandler(libp2pChallengeProtocol)
		s.mu.Unlock()
		s.wait.Wait()
	})
	return nil
}
