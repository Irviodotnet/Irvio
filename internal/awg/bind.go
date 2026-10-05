// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	awgconn "github.com/amnezia-vpn/amneziawg-go/v3/conn"

	"hydra.local/hydra/internal/networkstate"
	"hydra.local/hydra/internal/socketmux"
)

// IrvioBind borrows the mux Data endpoint without owning OS sockets.
type IrvioBind struct {
	mux        *socketmux.Mux
	data       *socketmux.Endpoint
	generation networkstate.Generation

	mu       sync.Mutex
	open     bool
	shutdown bool
	context  context.Context
	cancel   context.CancelFunc
	counters bindCounters
}

var _ awgconn.Bind = (*IrvioBind)(nil)

// NewIrvioBind constructs a closed logical AWG bind for one transport generation.
func NewIrvioBind(mux *socketmux.Mux, generation networkstate.Generation) (*IrvioBind, error) {
	if mux == nil || generation == 0 || mux.Port() == 0 {
		return nil, fmt.Errorf("create IrvioBind: %w", ErrInvalidConfig)
	}
	return &IrvioBind{mux: mux, data: mux.Data(), generation: generation}, nil
}

// Open attaches one receive function to Mux.Data without opening a socket.
func (b *IrvioBind) Open(port uint16) ([]awgconn.ReceiveFunc, uint16, error) {
	if b == nil {
		return nil, 0, ErrClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.shutdown {
		return nil, 0, ErrClosed
	}
	if b.open {
		return nil, 0, awgconn.ErrBindAlreadyOpen
	}
	if port != 0 && port != b.mux.Port() {
		return nil, 0, fmt.Errorf("open IrvioBind on port %d, mux port %d: %w", port, b.mux.Port(), ErrInvalidConfig)
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.open = true
	b.context = ctx
	b.cancel = cancel
	b.counters.openCalls.Add(1)
	receive := func(packets [][]byte, sizes []int, endpoints []awgconn.Endpoint) (int, error) {
		return b.receive(ctx, packets, sizes, endpoints)
	}
	return []awgconn.ReceiveFunc{receive}, b.mux.Port(), nil
}

func (b *IrvioBind) receive(ctx context.Context, packets [][]byte, sizes []int, endpoints []awgconn.Endpoint) (int, error) {
	if len(packets) == 0 || len(sizes) < len(packets) || len(endpoints) < len(packets) {
		return 0, fmt.Errorf("receive AWG datagram: %w", ErrInvalidConfig)
	}
	datagram, err := b.data.Read(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, socketmux.ErrClosed) {
			return 0, net.ErrClosed
		}
		return 0, err
	}
	if len(datagram.Payload) > len(packets[0]) {
		b.counters.receiveTooLarge.Add(1)
		return 0, fmt.Errorf("receive AWG datagram of %d bytes into %d-byte buffer: %w", len(datagram.Payload), len(packets[0]), ErrInvalidConfig)
	}
	copy(packets[0], datagram.Payload)
	sizes[0] = len(datagram.Payload)
	endpoints[0] = &Endpoint{destination: datagram.Source, owner: b}
	b.counters.lastReceive.Store(datagram.Source)
	b.counters.receivePackets.Add(1)
	b.counters.receiveBytes.Add(uint64(len(datagram.Payload)))
	if datagram.Family == socketmux.IPv4 {
		b.counters.receiveIPv4.Add(1)
	} else if datagram.Family == socketmux.IPv6 {
		b.counters.receiveIPv6.Add(1)
	}
	return 1, nil
}

// Close detaches the current receive function but never closes IrvioSocketMux.
func (b *IrvioBind) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if !b.open {
		b.mu.Unlock()
		return nil
	}
	cancel := b.cancel
	b.open = false
	b.context = nil
	b.cancel = nil
	b.counters.closeCalls.Add(1)
	b.mu.Unlock()
	cancel()
	return nil
}

// Shutdown permanently prevents the bind from being reopened.
func (b *IrvioBind) Shutdown() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	b.shutdown = true
	open := b.open
	cancel := b.cancel
	if open {
		b.open = false
		b.context = nil
		b.cancel = nil
		b.counters.closeCalls.Add(1)
	}
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// SetMark delegates physical-socket marking to IrvioSocketMux.
func (b *IrvioBind) SetMark(mark uint32) error {
	if b == nil || b.mux == nil {
		return ErrClosed
	}
	if err := b.mux.SetMark(mark); err != nil {
		return fmt.Errorf("set IrvioBind mark %#x: %w", mark, err)
	}
	return nil
}

// Send writes an AWG batch through Mux.Data and the mux-owned family socket.
func (b *IrvioBind) Send(buffers [][]byte, endpoint awgconn.Endpoint) error {
	if b == nil {
		return ErrClosed
	}
	if len(buffers) > b.BatchSize() {
		return fmt.Errorf("send %d AWG datagrams, batch limit %d: %w", len(buffers), b.BatchSize(), ErrInvalidConfig)
	}
	ep, ok := endpoint.(*Endpoint)
	if !ok || ep == nil || ep.owner != b {
		return awgconn.ErrWrongEndpointType
	}
	if err := validateAddrPort(ep.destination); err != nil {
		return err
	}
	b.mu.Lock()
	if !b.open || b.shutdown || b.cancel == nil {
		b.mu.Unlock()
		return net.ErrClosed
	}
	ctx := b.context
	b.mu.Unlock()
	for _, payload := range buffers {
		written, err := b.data.WriteTo(ctx, payload, ep.destination)
		if err != nil {
			return fmt.Errorf("send AWG datagram to %s: %w", ep.destination, err)
		}
		if written != len(payload) {
			return fmt.Errorf("send AWG datagram to %s: short write %d/%d", ep.destination, written, len(payload))
		}
		b.counters.sendPackets.Add(1)
		b.counters.sendBytes.Add(uint64(written))
		b.counters.lastSend.Store(ep.destination)
		if ep.destination.Addr().Is4() {
			b.counters.sendIPv4.Add(1)
		} else {
			b.counters.sendIPv6.Add(1)
		}
	}
	return nil
}

// ParseEndpoint creates a generation-local endpoint without resolving DNS.
func (b *IrvioBind) ParseEndpoint(value string) (awgconn.Endpoint, error) {
	endpoint, err := netip.ParseAddrPort(value)
	if err != nil {
		return nil, fmt.Errorf("parse AWG endpoint: %w", err)
	}
	if err := validateAddrPort(endpoint); err != nil {
		return nil, err
	}
	return &Endpoint{destination: endpoint, owner: b}, nil
}

// BatchSize supports bounded AWG preludes; datagrams remain sequential.
func (*IrvioBind) BatchSize() int { return awgconn.IdealBatchSize }

// Port returns the unchanged Irvio physical source port.
func (b *IrvioBind) Port() uint16 {
	if b == nil || b.mux == nil {
		return 0
	}
	return b.mux.Port()
}

// Generation returns the immutable owner generation.
func (b *IrvioBind) Generation() networkstate.Generation {
	if b == nil {
		return 0
	}
	return b.generation
}

// Stats returns race-safe transport counters.
func (b *IrvioBind) Stats() BindStats {
	if b == nil {
		return BindStats{}
	}
	return b.counters.snapshot()
}
