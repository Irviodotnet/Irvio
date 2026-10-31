// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

var _ net.PacketConn = (*packetConn)(nil)

type connState struct {
	adapter *Adapter
	key     connKey
	local   netip.AddrPort
	queue   chan inboundPacket
	done    chan struct{}
	context context.Context
	cancel  context.CancelFunc
	refs    int

	closeOnce sync.Once
	errMu     sync.Mutex
	err       error
}

func newConnState(adapter *Adapter, key connKey, local netip.AddrPort, queueCapacity int) *connState {
	ctx, cancel := context.WithCancel(adapter.context)
	return &connState{
		adapter: adapter, key: key, local: local,
		queue: make(chan inboundPacket, queueCapacity), done: make(chan struct{}), context: ctx, cancel: cancel,
	}
}

func (s *connState) closeWith(err error) {
	s.closeOnce.Do(func() {
		s.errMu.Lock()
		s.err = err
		s.errMu.Unlock()
		s.cancel()
		close(s.done)
	})
}

func (s *connState) terminalError() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.err == nil {
		return ErrClosed
	}
	return s.err
}

type packetConn struct {
	state     *connState
	closed    atomic.Bool
	closeOnce sync.Once
	closeCh   chan struct{}
	context   context.Context
	cancel    context.CancelFunc

	deadlineMu    sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time
	readChanged   chan struct{}
}

func newPacketConn(state *connState) *packetConn {
	ctx, cancel := context.WithCancel(state.context)
	return &packetConn{
		state: state, closeCh: make(chan struct{}), context: ctx, cancel: cancel,
		readChanged: make(chan struct{}),
	}
}

func (c *packetConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	for {
		if c.closed.Load() {
			return 0, nil, ErrClosed
		}
		select {
		case <-c.state.done:
			return 0, nil, c.state.terminalError()
		default:
		}
		deadline, changed := c.readDeadlineSnapshot()
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return 0, nil, os.ErrDeadlineExceeded
		}
		var timer *time.Timer
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			timer = time.NewTimer(time.Until(deadline))
			timeout = timer.C
		}
		select {
		case <-c.closeCh:
			stopTimer(timer)
			return 0, nil, ErrClosed
		case <-c.state.done:
			stopTimer(timer)
			return 0, nil, c.state.terminalError()
		case <-changed:
			stopTimer(timer)
			continue
		case <-timeout:
			return 0, nil, os.ErrDeadlineExceeded
		case packet := <-c.state.queue:
			stopTimer(timer)
			if c.closed.Load() {
				return 0, nil, ErrClosed
			}
			select {
			case <-c.state.done:
				return 0, nil, c.state.terminalError()
			default:
			}
			n := copy(buffer, packet.payload)
			if n < len(packet.payload) {
				return n, net.UDPAddrFromAddrPort(packet.source), io.ErrShortBuffer
			}
			return n, net.UDPAddrFromAddrPort(packet.source), nil
		}
	}
}

func (c *packetConn) WriteTo(payload []byte, destination net.Addr) (int, error) {
	if c.closed.Load() {
		return 0, ErrClosed
	}
	select {
	case <-c.state.done:
		return 0, c.state.terminalError()
	default:
	}
	remote, family, err := addrPortFromNetAddr(destination)
	if err != nil {
		return 0, fmt.Errorf("write ICE datagram: %w", err)
	}
	if family != c.state.key.family {
		return 0, fmt.Errorf("write ICE datagram: destination family differs from logical connection")
	}
	message, status := decodeSTUN(payload)
	if status != decodeValid || message.Type.Method != stun.MethodBinding ||
		(message.Type.Class != stun.ClassRequest && message.Type.Class != stun.ClassSuccessResponse && message.Type.Class != stun.ClassErrorResponse) {
		c.state.adapter.registry.stats.unsupportedOutboundPayloads.Add(1)
		return 0, ErrNonICEPayloadUnsupported
	}
	var id transactionID
	registered := false
	if message.Type.Class == stun.ClassRequest {
		_, local, ok := usernameParts(message)
		if !ok || local != c.state.key.ufrag {
			c.state.adapter.registry.stats.unsupportedOutboundPayloads.Add(1)
			return 0, ErrNonICEPayloadUnsupported
		}
		id = transactionID(message.TransactionID)
		c.state.adapter.registry.stats.outboundICERequestsObserved.Add(1)
		if err := c.state.adapter.registry.registerTransaction(id, c.state.key.ufrag, remote, family, time.Now()); err != nil {
			return 0, fmt.Errorf("register outbound ICE transaction: %w", err)
		}
		registered = true
	}
	deadline := c.writeDeadlineSnapshot()
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		if registered {
			c.state.adapter.registry.retireTransaction(id, time.Now())
		}
		return 0, os.ErrDeadlineExceeded
	}
	ctx := c.context
	cancel := func() {}
	if !deadline.IsZero() {
		ctx, cancel = context.WithDeadline(ctx, deadline)
	}
	defer cancel()
	n, err := c.state.adapter.endpoint.WriteTo(ctx, payload, remote)
	if err != nil {
		if registered {
			c.state.adapter.registry.retireTransaction(id, time.Now())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return n, os.ErrDeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			if c.closed.Load() || c.state.adapter.closed.Load() {
				return n, ErrClosed
			}
			select {
			case <-c.state.done:
				return n, c.state.terminalError()
			default:
			}
		}
		if errors.Is(err, socketmux.ErrClosed) {
			return n, fmt.Errorf("%w: %v", ErrTransportClosed, err)
		}
		return n, fmt.Errorf("write ICE datagram through Irvio mux: %w", err)
	}
	return n, nil
}

func (c *packetConn) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.cancel()
		close(c.closeCh)
		c.state.adapter.release(c.state)
	})
	return nil
}

func (c *packetConn) LocalAddr() net.Addr {
	return net.UDPAddrFromAddrPort(c.state.local)
}

func (c *packetConn) SetDeadline(deadline time.Time) error {
	if err := c.SetReadDeadline(deadline); err != nil {
		return err
	}
	return c.SetWriteDeadline(deadline)
}

func (c *packetConn) SetReadDeadline(deadline time.Time) error {
	if c.closed.Load() {
		return ErrClosed
	}
	c.deadlineMu.Lock()
	if c.closed.Load() {
		c.deadlineMu.Unlock()
		return ErrClosed
	}
	old := c.readChanged
	c.readDeadline = deadline
	c.readChanged = make(chan struct{})
	close(old)
	c.deadlineMu.Unlock()
	return nil
}

func (c *packetConn) SetWriteDeadline(deadline time.Time) error {
	if c.closed.Load() {
		return ErrClosed
	}
	c.deadlineMu.Lock()
	if c.closed.Load() {
		c.deadlineMu.Unlock()
		return ErrClosed
	}
	c.writeDeadline = deadline
	c.deadlineMu.Unlock()
	return nil
}

func (c *packetConn) readDeadlineSnapshot() (time.Time, <-chan struct{}) {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return c.readDeadline, c.readChanged
}

func (c *packetConn) writeDeadlineSnapshot() time.Time {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return c.writeDeadline
}

func stopTimer(timer *time.Timer) {
	if timer != nil && !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
