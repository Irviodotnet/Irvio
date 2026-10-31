// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

type PrivateEnrollmentEndpoint struct {
	host    host.Host
	serve   func(context.Context, net.Conn) error
	mu      sync.Mutex
	closed  bool
	owners  map[peer.ID]struct{}
	streams map[network.Stream]context.CancelFunc
	wg      sync.WaitGroup
	once    sync.Once
}

func NewPrivateEnrollmentEndpoint(h host.Host, serve func(context.Context, net.Conn) error) (*PrivateEnrollmentEndpoint, error) {
	if h == nil || serve == nil {
		return nil, ErrInvalid
	}
	e := &PrivateEnrollmentEndpoint{host: h, serve: serve, owners: make(map[peer.ID]struct{}), streams: make(map[network.Stream]context.CancelFunc)}
	h.SetStreamHandler(libprotocol.ID(PrivateEnrollmentEndpointProtocol), e.handle)
	return e, nil
}

// SetOwners atomically fences new admission and cancels carriers belonging to removed owners.
func (e *PrivateEnrollmentEndpoint) SetOwners(owners []peer.ID) error {
	if len(owners) > MaxOwnerRoutes {
		return ErrInvalid
	}
	next := make(map[peer.ID]struct{})
	for _, owner := range owners {
		if owner.Validate() != nil || owner == e.host.ID() {
			return ErrInvalid
		}
		next[owner] = struct{}{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	e.owners = next
	for stream, cancel := range e.streams {
		if _, allowed := next[stream.Conn().RemotePeer()]; !allowed {
			cancel()
		}
	}
	return nil
}

func (e *PrivateEnrollmentEndpoint) handle(stream network.Stream) {
	e.mu.Lock()
	_, allowed := e.owners[stream.Conn().RemotePeer()]
	if e.closed || !allowed || len(e.streams) >= 8 {
		e.mu.Unlock()
		stream.Reset()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	e.streams[stream] = cancel
	e.wg.Add(1)
	e.mu.Unlock()
	defer func() {
		cancel()
		stream.Reset()
		e.mu.Lock()
		delete(e.streams, stream)
		e.mu.Unlock()
		e.wg.Done()
	}()
	stop := context.AfterFunc(ctx, func() { stream.Reset() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := stream.SetDeadline(deadline); err != nil {
		return
	}
	_ = e.serve(ctx, &EnrollmentConn{Stream: stream})
}

func (e *PrivateEnrollmentEndpoint) Close() error {
	if e == nil {
		return nil
	}
	e.once.Do(func() {
		e.mu.Lock()
		e.closed = true
		e.host.RemoveStreamHandler(libprotocol.ID(PrivateEnrollmentEndpointProtocol))
		clear(e.owners)
		for _, cancel := range e.streams {
			cancel()
		}
		e.mu.Unlock()
		e.wg.Wait()
	})
	return nil
}
