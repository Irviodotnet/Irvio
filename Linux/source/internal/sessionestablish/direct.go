// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessionestablish

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

type DirectService struct {
	host      host.Host
	responder *Responder
	timeout   time.Duration
	slots     chan struct{}
	once      sync.Once
	wg        sync.WaitGroup
	mu        sync.Mutex
	closed    bool
	streams   map[network.Stream]struct{}
}

func NewDirectService(host host.Host, responder *Responder, timeout time.Duration, maxHandlers int) (*DirectService, error) {
	if host == nil || responder == nil {
		return nil, ErrInvalid
	}
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	if maxHandlers == 0 {
		maxHandlers = 256
	}
	if timeout <= 0 || timeout > time.Minute || maxHandlers < 1 || maxHandlers > 65_536 {
		return nil, ErrInvalid
	}
	service := &DirectService{
		host: host, responder: responder, timeout: timeout,
		slots: make(chan struct{}, maxHandlers), streams: make(map[network.Stream]struct{}),
	}
	host.SetStreamHandler(libprotocol.ID(DirectProtocolID), service.handle)
	return service, nil
}

func (s *DirectService) handle(stream network.Stream) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = stream.Reset()
		return
	}
	s.streams[stream] = struct{}{}
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.streams, stream)
		s.mu.Unlock()
		s.wg.Done()
	}()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		_ = stream.Reset()
		return
	}
	deadline := time.Now().Add(s.timeout)
	_ = stream.SetDeadline(deadline)
	request, err := readWire(stream)
	if err != nil {
		_ = stream.Reset()
		return
	}
	verified, err := VerifyForRecipient(
		request, s.host.ID(), s.responder.options.Now(), s.responder.options.Policy,
	)
	if err != nil || verified.Signer != stream.Conn().RemotePeer() {
		_ = stream.Reset()
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	response, err := s.responder.HandleSignal(ctx, stream.Conn().RemotePeer(), request)
	cancel()
	if err != nil || writeWire(stream, response) != nil {
		_ = stream.Reset()
		return
	}
	_ = stream.Close()
}

func (s *DirectService) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.host.RemoveStreamHandler(libprotocol.ID(DirectProtocolID))
		streams := make([]network.Stream, 0, len(s.streams))
		for stream := range s.streams {
			streams = append(streams, stream)
		}
		s.mu.Unlock()
		for _, stream := range streams {
			_ = stream.Reset()
		}
		s.wg.Wait()
	})
}

type DirectCarrier struct {
	host    host.Host
	timeout time.Duration
}

func NewDirectCarrier(host host.Host, timeout time.Duration) (*DirectCarrier, error) {
	if host == nil {
		return nil, ErrInvalid
	}
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, ErrInvalid
	}
	return &DirectCarrier{host: host, timeout: timeout}, nil
}

func (c *DirectCarrier) Exchange(ctx context.Context, target peer.ID, request []byte) ([]byte, error) {
	if ctx == nil || target == "" || len(request) == 0 || len(request) > MaxWireSize {
		return nil, ErrInvalid
	}
	stream, err := c.host.NewStream(ctx, target, libprotocol.ID(DirectProtocolID))
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	if stream.Conn().RemotePeer() != target {
		_ = stream.Reset()
		return nil, ErrWrongPeer
	}
	deadline := time.Now().Add(c.timeout)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	_ = stream.SetDeadline(deadline)
	if err := writeWire(stream, request); err != nil {
		_ = stream.Reset()
		return nil, err
	}
	response, err := readWire(stream)
	if err != nil {
		_ = stream.Reset()
		return nil, err
	}
	return response, nil
}

func writeWire(writer io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > MaxWireSize {
		return ErrInvalid
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeExact(writer, header[:]); err != nil {
		return err
	}
	return writeExact(writer, payload)
}

func readWire(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > MaxWireSize {
		return nil, fmt.Errorf("%w: direct frame size", ErrInvalid)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeExact(writer io.Writer, payload []byte) error {
	for len(payload) != 0 {
		written, err := writer.Write(payload)
		if written <= 0 || written > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[written:]
		if err != nil {
			return err
		}
	}
	return nil
}

var _ Carrier = (*DirectCarrier)(nil)
