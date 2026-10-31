// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnel

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
)

type windowsBorrowed struct {
	owner     *Windows
	stop      windows.Handle
	events    chan awgtun.Event
	mu        sync.Mutex
	closed    atomic.Bool
	running   sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

var _ awgtun.Device = (*windowsBorrowed)(nil)

func (b *windowsBorrowed) File() *os.File              { return nil }
func (b *windowsBorrowed) Name() (string, error)       { return b.owner.name, nil }
func (b *windowsBorrowed) MTU() (int, error)           { return b.owner.mtu, nil }
func (b *windowsBorrowed) Events() <-chan awgtun.Event { return b.events }
func (b *windowsBorrowed) BatchSize() int              { return 1 }

func (b *windowsBorrowed) begin() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed.Load() {
		return false
	}
	b.running.Add(1)
	return true
}

func (b *windowsBorrowed) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	if !b.begin() {
		return 0, os.ErrClosed
	}
	defer b.running.Done()
	if len(bufs) == 0 || len(sizes) == 0 || offset < 0 || offset >= len(bufs[0]) {
		return 0, os.ErrInvalid
	}
	for !b.closed.Load() {
		packet, err := b.owner.ring.ReceivePacket()
		if err == nil {
			if len(packet) > len(bufs[0])-offset {
				b.owner.ring.ReleaseReceivePacket(packet)
				return 0, io.ErrShortBuffer
			}
			sizes[0] = copy(bufs[0][offset:], packet)
			b.owner.ring.ReleaseReceivePacket(packet)
			return 1, nil
		}
		if errors.Is(err, windows.ERROR_HANDLE_EOF) {
			return 0, os.ErrClosed
		}
		if !errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return 0, fmt.Errorf("Wintun read: %w", err)
		}
		// A bounded wait also guarantees cancellation if signaling the stop handle ever fails.
		_, err = windows.WaitForMultipleObjects([]windows.Handle{b.stop, b.owner.ring.ReadWaitEvent()}, false, 1000)
		if err != nil {
			return 0, fmt.Errorf("wait for Wintun packet: %w", err)
		}
	}
	return 0, os.ErrClosed
}

func (b *windowsBorrowed) Write(bufs [][]byte, offset int) (int, error) {
	if !b.begin() {
		return 0, os.ErrClosed
	}
	defer b.running.Done()
	for i, buf := range bufs {
		if b.closed.Load() {
			return i, os.ErrClosed
		}
		if offset < 0 || offset >= len(buf) || len(buf)-offset > wintun.PacketSizeMax {
			return i, os.ErrInvalid
		}
		packet, err := b.owner.ring.AllocateSendPacket(len(buf) - offset)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if errors.Is(err, windows.ERROR_HANDLE_EOF) {
			return i, os.ErrClosed
		}
		if err != nil {
			return i, fmt.Errorf("Wintun write: %w", err)
		}
		copy(packet, buf[offset:])
		b.owner.ring.SendPacket(packet)
	}
	return len(bufs), nil
}

func (b *windowsBorrowed) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed.Store(true)
		b.closeErr = windows.SetEvent(b.stop)
		b.mu.Unlock()
		b.running.Wait()
		b.closeErr = errors.Join(b.closeErr, windows.CloseHandle(b.stop))
		close(b.events)
		b.owner.release(b)
	})
	return b.closeErr
}
