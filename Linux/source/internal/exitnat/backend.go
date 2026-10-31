// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

// PacketPort transfers ownership of each returned buffer and must interrupt reads when closed.
type PacketPort interface {
	Reservation
	ReceiveIPv4(context.Context) ([]byte, error)
}

type PacketSender interface {
	io.Closer
	SendIPv4(context.Context, []byte) error
}

type OpenPacketPort func(context.Context, Path, uint8) (PacketPort, error)

type capturedReply struct {
	token  *ReplyToken
	packet []byte
}

type packetReader interface {
	io.Closer
	ReceiveIPv4(context.Context) ([]byte, error)
}

type backendFailure struct{ err error }

const replyDeliveryBudget = 2 * time.Second

// Backend owns a fixed physical generation; a route change requires closing it before replacement.
type Backend struct {
	engine  *Engine
	path    Path
	sender  PacketSender
	deliver Emit
	open    OpenPacketPort
	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan capturedReply
	done    chan struct{}
	closing atomic.Bool
	failure atomic.Pointer[backendFailure]
	dropped atomic.Uint64
	closeMu sync.Mutex
}

// NewBackend takes sender ownership only on success; deliver must be bounded and cannot re-enter Backend.
func NewBackend(c Config, sender PacketSender, open OpenPacketPort, deliver Emit) (*Backend, error) {
	if sender == nil || open == nil || deliver == nil {
		return nil, ErrConfig
	}
	b := &Backend{path: c.Path, sender: sender, open: open, deliver: deliver, queue: make(chan capturedReply, 128), done: make(chan struct{})}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	c.Reserve = b.reserve
	var err error
	b.engine, err = New(c)
	if err != nil {
		b.cancel()
		return nil, err
	}
	go b.dispatch()
	return b, nil
}

func (b *Backend) Err() error {
	if f := b.failure.Load(); f != nil {
		return f.err
	}
	if b.closing.Load() {
		return ErrStale
	}
	return nil
}

func (b *Backend) fail(err error) {
	if b.failure.CompareAndSwap(nil, &backendFailure{err}) {
		b.cancel()
	}
}

func (b *Backend) AddLease(v tunnellease.Lease) (*Lease, error) {
	if err := b.Err(); err != nil {
		return nil, err
	}
	return b.engine.AddLease(v)
}

func (b *Backend) Renew(l *Lease, v tunnellease.Lease) error {
	if err := b.Err(); err != nil {
		return err
	}
	return b.engine.Renew(l, v)
}

func (b *Backend) Revoke(l *Lease) error  { return b.engine.Revoke(l) }
func (b *Backend) Stats() Stats           { return b.engine.Stats() }
func (b *Backend) DroppedReplies() uint64 { return b.dropped.Load() }

func (b *Backend) Outbound(ctx context.Context, l *Lease, packet []byte) error {
	if ctx == nil {
		return ErrConfig
	}
	if err := b.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(b.ctx, cancel)
	defer stop()
	defer cancel()
	return b.engine.Outbound(ctx, l, packet, func(ctx context.Context, path Path, packet []byte) error {
		if err := b.Err(); err != nil {
			return err
		}
		if path != b.path {
			return ErrStale
		}
		return b.sender.SendIPv4(ctx, packet)
	})
}

func (b *Backend) reserve(ctx context.Context, path Path, protocol uint8, token *ReplyToken) (Reservation, error) {
	if err := b.Err(); err != nil {
		return nil, err
	}
	p, err := b.open(ctx, path, protocol)
	if err != nil || p == nil {
		return p, errors.Join(err, ErrConfig)
	}
	return &capturedPort{PacketPort: p, reader: b.capture(p, token)}, nil
}

func (b *Backend) capture(p packetReader, token *ReplyToken) *capturedReader {
	r := &capturedReader{packetReader: p, done: make(chan struct{})}
	readerCtx, cancel := context.WithCancel(b.ctx)
	r.cancel = cancel
	go func() {
		defer close(r.done)
		for readerCtx.Err() == nil {
			packet, err := p.ReceiveIPv4(readerCtx)
			if readerCtx.Err() != nil {
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			if err != nil {
				b.fail(err)
				return
			}
			if len(packet) < 28 || len(packet) > 65535 {
				b.dropped.Add(1)
				continue
			}
			select {
			case b.queue <- capturedReply{token, packet}:
			default:
				b.dropped.Add(1)
			}
		}
	}()
	return r
}

func (b *Backend) dispatch() {
	defer close(b.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			if err := b.engine.Sweep(); err != nil {
				b.fail(err)
			}
		case r := <-b.queue:
			ctx, cancel := context.WithTimeout(b.ctx, replyDeliveryBudget)
			if err := b.engine.Inbound(ctx, b.path, r.token, r.packet, b.deliver); err != nil {
				b.dropped.Add(1)
			}
			cancel()
		}
	}
}

// Close cancels packet work before waiting on Engine; failed native resources remain owned for retry.
func (b *Backend) Close() error {
	b.closeMu.Lock()
	defer b.closeMu.Unlock()
	b.closing.Store(true)
	b.cancel()
	err := b.engine.Close()
	select {
	case <-b.done:
	case <-time.After(3 * time.Second):
		return errors.Join(err, context.DeadlineExceeded)
	}
	for len(b.queue) != 0 {
		<-b.queue
	}
	return errors.Join(err, b.sender.Close())
}

type capturedPort struct {
	PacketPort
	reader *capturedReader
}

func (r *capturedPort) Close() error { return r.reader.Close() }

type capturedReader struct {
	packetReader
	cancel context.CancelFunc
	done   chan struct{}
}

// Close joins only the reader, which never takes the engine lock or waits for the dispatcher.
func (r *capturedReader) Close() error {
	r.cancel()
	err := r.packetReader.Close()
	select {
	case <-r.done:
		return err
	case <-time.After(3 * time.Second):
		return errors.Join(err, context.DeadlineExceeded)
	}
}
