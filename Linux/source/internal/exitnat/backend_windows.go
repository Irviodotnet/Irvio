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

	"github.com/Irviodotnet/Irvio/internal/windivert"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

// NewWindowsBackend requires an installer-owned running driver and an authorized physical path.
func NewWindowsBackend(c Config, module *windivert.Module, deliver Emit) (*Backend, error) {
	if module == nil {
		return nil, ErrConfig
	}
	binding, err := winroute.ForSource(c.Path.Source)
	if err != nil {
		return nil, err
	}
	if binding.LUID != c.Path.Interface {
		return nil, ErrStale
	}
	sender := &windowsSender{}
	var b *Backend
	c.ReserveEcho = func(ctx context.Context, path Path, spec EchoCapture, token *ReplyToken) (io.Closer, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if path != c.Path || b == nil {
			return nil, ErrStale
		}
		if err := b.Err(); err != nil {
			return nil, err
		}
		h, err := module.OpenEchoReplyCapture(binding, spec.Remote, spec.Identifier, spec.Sequence, spec.Nonce)
		if err != nil || h == nil {
			if h != nil {
				return h, err
			}
			return nil, err
		}
		return b.capture(&windowsEchoReader{Handle: h, binding: binding}, token), nil
	}
	b, err = NewBackend(c, sender, func(ctx context.Context, path Path, protocol uint8) (PacketPort, error) {
		port, err := ReserveWindows(ctx, path, protocol)
		if port == nil {
			return nil, err
		}
		r := &windowsPacketPort{Reservation: port, binding: binding}
		if err != nil {
			return r, err
		}
		r.capture, err = module.OpenReplyCapture(binding, protocol, port.Port())
		return r, err
	}, deliver)
	if err != nil {
		return nil, err
	}
	sender.handle, err = module.OpenForwardSender(binding)
	if err != nil {
		b.fail(err)
		if cleanup := b.Close(); cleanup != nil {
			return b, errors.Join(err, cleanup)
		}
		return nil, err
	}
	return b, nil
}

type windowsSender struct{ handle *windivert.Handle }

type windowsEchoReader struct {
	*windivert.Handle
	binding winroute.Binding
}

func (r *windowsEchoReader) ReceiveIPv4(ctx context.Context) ([]byte, error) {
	for {
		packet, err := r.Handle.ReceiveIPv4(ctx)
		if !errors.Is(err, windivert.ErrPacket) {
			return packet, err
		}
		current, err := winroute.ForSource(r.binding.Source)
		if err != nil || current != r.binding {
			return nil, errors.Join(ErrStale, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

func (s *windowsSender) SendIPv4(ctx context.Context, packet []byte) error {
	if s.handle == nil {
		return ErrStale
	}
	return s.handle.SendIPv4(ctx, packet)
}
func (s *windowsSender) Close() error {
	if s.handle == nil {
		return nil
	}
	return s.handle.Close()
}

type windowsPacketPort struct {
	Reservation
	binding winroute.Binding
	capture *windivert.Handle
	mu      sync.Mutex
}

func (r *windowsPacketPort) ReceiveIPv4(ctx context.Context) ([]byte, error) {
	for {
		packet, err := r.capture.ReceiveIPv4(ctx)
		if !errors.Is(err, windivert.ErrPacket) {
			return packet, err
		}
		binding, err := winroute.ForSource(r.binding.Source)
		if err != nil || binding != r.binding {
			return nil, errors.Join(ErrStale, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// Close keeps the exclusive socket until capture is gone, including retry after native close failure.
func (r *windowsPacketPort) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.capture != nil {
		if err := r.capture.Close(); err != nil {
			return err
		}
	}
	return r.Reservation.Close()
}
