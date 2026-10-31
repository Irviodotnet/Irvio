// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windivert

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/icmpv4"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/Irviodotnet/Irvio/internal/winthread"
	"golang.org/x/sys/windows"
)

var ErrClosed = errors.New("Exit driver handle is closing")
var ErrPacket = errors.New("Exit driver packet does not match its owned path")

const operationBudget = 2 * time.Second

type nativeAddress struct {
	Timestamp                            int64
	Flags, Reserved, IfIndex, SubIfIndex uint32
	Padding                              [56]byte
}

// Handle has one in-flight operation and no unbounded userspace packet queue.
type Handle struct {
	module      *Module
	fd          windows.Handle
	binding     winroute.Binding
	protocol    uint8
	port        uint16
	echo        *echoCapture
	sender      bool
	gate        chan struct{}
	stop        chan struct{}
	mu, closeMu sync.Mutex
	closing     bool
	drained     chan struct{}
	active      bool
}

func validBinding(b winroute.Binding) error {
	if !b.Source.Is4() || !b.Source.IsGlobalUnicast() {
		return ErrPacket
	}
	current, err := winroute.ForSource(b.Source)
	if err != nil {
		return err
	}
	if current != b {
		return ErrPacket
	}
	return nil
}

// OpenReplyCapture requires the caller to retain exclusive ownership of this exact protocol/source port.
func (m *Module) OpenReplyCapture(b winroute.Binding, protocol uint8, port uint16) (*Handle, error) {
	if port == 0 || (protocol != 6 && protocol != 17) {
		return nil, ErrPacket
	}
	if err := validBinding(b); err != nil {
		return nil, err
	}
	h, err := m.openIO(replyFilter(b, protocol, port), 0, 4)
	if h != nil {
		h.binding, h.protocol, h.port = b, protocol, port
	}
	return h, err
}

func replyFilter(b winroute.Binding, protocol uint8, port uint16) string {
	name := "tcp"
	if protocol == 17 {
		name = "udp"
	}
	source := b.Source.As4()
	return fmt.Sprintf("inbound and ip and !loopback and ifIdx==%d and ip.DstAddr==%s and ((%s and %s.DstPort==%d) or (icmp and ip.HdrLength==5 and ip.Length>=56 and (icmp.Type==3 or icmp.Type==11 or icmp.Type==12) and packet[28]==69 and packet[37]==%d and packet32[40b]==%d and packet16[48b]==%d))", b.Index, b.Source, name, name, port, protocol, binary.BigEndian.Uint32(source[:]), port)
}

// OpenForwardSender exposes only explicit IPv4 physical injection; callers must authorize every packet first.
func (m *Module) OpenForwardSender(b winroute.Binding) (*Handle, error) {
	if err := validBinding(b); err != nil {
		return nil, err
	}
	h, err := m.openIO("false", 1, 8)
	if h != nil {
		h.binding, h.sender = b, true
	}
	return h, err
}

func (m *Module) openIO(filter string, layer, flags uintptr) (*Handle, error) {
	if unsafe.Sizeof(nativeAddress{}) != 80 {
		return nil, ErrPacket
	}
	if err := m.checkService(); err != nil {
		return nil, err
	}
	fd, openErr := winthread.Call(func() (windows.Handle, error) {
		encoded := append([]byte(filter), 0)
		fd, _, err := m.open.Call(uintptr(unsafe.Pointer(&encoded[0])), layer, 0, flags|16)
		runtime.KeepAlive(encoded)
		if windows.Handle(fd) == windows.InvalidHandle {
			return windows.InvalidHandle, err
		}
		for _, param := range [][2]uintptr{{0, 32}, {1, 200}, {2, 65535}} {
			ok, _, err := m.setParam.Call(fd, param[0], param[1])
			if ok == 0 {
				return windows.Handle(fd), err
			}
		}
		return windows.Handle(fd), nil
	})
	if fd == windows.InvalidHandle || fd == 0 {
		return nil, openErr
	}
	h := &Handle{module: m, fd: fd, gate: make(chan struct{}, 1), stop: make(chan struct{}), drained: make(chan struct{})}
	close(h.drained)
	if openErr != nil {
		if err := h.Close(); err != nil {
			return h, errors.Join(openErr, err)
		}
		return nil, openErr
	}
	return h, nil
}

// SendIPv4 finishes kernel ownership of the buffer before returning, including after cancellation.
func (h *Handle) SendIPv4(ctx context.Context, packet []byte) error {
	if h == nil || !h.sender || len(packet) < 28 || len(packet) > 65535 || packet[0] != 0x45 ||
		int(binary.BigEndian.Uint16(packet[2:4])) != len(packet) ||
		netip.AddrFrom4([4]byte(packet[12:16])) != h.binding.Source {
		return ErrPacket
	}
	if err := validBinding(h.binding); err != nil {
		return err
	}
	a := nativeAddress{Flags: 1, IfIndex: h.binding.Index}
	n, err := h.transfer(ctx, packet, &a, true)
	if err == nil && n != len(packet) {
		return ErrPacket
	}
	return err
}

// ReceiveIPv4 diverts only the reserved tuple; it never reinjects unknown host traffic.
func (h *Handle) ReceiveIPv4(ctx context.Context) ([]byte, error) {
	if h == nil || h.sender {
		return nil, ErrPacket
	}
	var a nativeAddress
	b := make([]byte, 65535)
	n, err := h.transfer(ctx, b, &a, false)
	if err != nil {
		return nil, err
	}
	if n < 28 || n > len(b) || a.Flags&0x001fFFFF != 0 || a.IfIndex != h.binding.Index ||
		!h.matchesReply(b[:n]) {
		return nil, ErrPacket
	}
	if err := validBinding(h.binding); err != nil {
		return nil, err
	}
	return b[:n], nil
}

func (h *Handle) matchesReply(b []byte) bool {
	if len(b) < 28 || b[0] != 0x45 || int(binary.BigEndian.Uint16(b[2:4])) != len(b) ||
		netip.AddrFrom4([4]byte(b[16:20])) != h.binding.Source {
		return false
	}
	if h.echo != nil {
		return h.echo.matches(b)
	}
	if b[9] == 1 {
		p, err := icmpv4.ParseError(b)
		return err == nil && p.Protocol == h.protocol && p.OriginalSource == h.binding.Source && p.SourcePort == h.port
	}
	return b[9] == h.protocol && binary.BigEndian.Uint16(b[22:24]) == h.port
}

func (h *Handle) transfer(ctx context.Context, b []byte, a *nativeAddress, send bool) (int, error) {
	if ctx == nil {
		return 0, ErrPacket
	}
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	select {
	case <-h.stop:
		return 0, ErrClosed
	case <-ctx.Done():
		return 0, ctx.Err()
	case h.gate <- struct{}{}:
	}
	defer func() { <-h.gate }()
	active := false
	defer func() {
		if active {
			h.mu.Lock()
			h.active = false
			close(h.drained)
			h.mu.Unlock()
		}
	}()
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	ov := &windows.Overlapped{HEvent: event}
	var n uint32
	addressLength := uint32(unsafe.Sizeof(*a))
	var pin runtime.Pinner
	pin.Pin(&b[0])
	pin.Pin(a)
	pin.Pin(ov)
	pin.Pin(&n)
	pin.Pin(&addressLength)
	defer pin.Unpin()
	h.mu.Lock()
	if h.closing || ctx.Err() != nil {
		h.mu.Unlock()
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, ErrClosed
	}
	h.active = true
	active = true
	h.drained = make(chan struct{})
	fd := h.fd
	var ok uintptr
	if send {
		ok, _, err = h.module.send.Call(uintptr(fd), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&n)), 0, uintptr(unsafe.Pointer(a)), uintptr(addressLength), uintptr(unsafe.Pointer(ov)))
	} else {
		ok, _, err = h.module.recv.Call(uintptr(fd), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&n)), 0, uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(&addressLength)), uintptr(unsafe.Pointer(ov)))
	}
	h.mu.Unlock()
	if ok != 0 {
		err = nil
	} else if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = awaitCompletion(ctx, fd, ov, &n)
	}
	if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
		select {
		case <-h.stop:
			err = ErrClosed
		default:
		}
	}
	if err == nil && addressLength != uint32(unsafe.Sizeof(*a)) {
		err = ErrPacket
	}
	return int(n), err
}

func awaitCompletion(ctx context.Context, fd windows.Handle, ov *windows.Overlapped, n *uint32) error {
	for {
		if err := ctx.Err(); err != nil {
			cancelErr := windows.CancelIoEx(fd, ov)
			if errors.Is(cancelErr, windows.ERROR_NOT_FOUND) {
				cancelErr = nil
			}
			completion := windows.GetOverlappedResult(fd, ov, n, true)
			if errors.Is(completion, windows.ERROR_OPERATION_ABORTED) {
				completion = nil
			}
			return errors.Join(err, cancelErr, completion)
		}
		status, err := windows.WaitForSingleObject(ov.HEvent, 10)
		if err == nil && status == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		if err != nil || status != windows.WAIT_OBJECT_0 {
			cancelErr := windows.CancelIoEx(fd, ov)
			completion := windows.GetOverlappedResult(fd, ov, n, true)
			return errors.Join(fmt.Errorf("wait for Exit I/O: status=%d: %w", status, err), cancelErr, completion)
		}
		return windows.GetOverlappedResult(fd, ov, n, false)
	}
}

// Close fences new operations and retains ownership for retry if cancellation or close cannot finish.
func (h *Handle) Close() error {
	if h == nil {
		return nil
	}
	h.closeMu.Lock()
	defer h.closeMu.Unlock()
	h.mu.Lock()
	if h.fd == windows.InvalidHandle {
		h.mu.Unlock()
		return nil
	}
	if !h.closing {
		h.closing = true
		close(h.stop)
	}
	var cancelErr error
	if h.active {
		cancelErr = windows.CancelIoEx(h.fd, nil)
		if errors.Is(cancelErr, windows.ERROR_NOT_FOUND) {
			cancelErr = nil
		}
	}
	drained := h.drained
	h.mu.Unlock()
	select {
	case <-drained:
	case <-time.After(operationBudget):
		return errors.Join(context.DeadlineExceeded, cancelErr)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	ok, _, err := h.module.close.Call(uintptr(h.fd))
	if ok == 0 {
		return errors.Join(err, cancelErr)
	}
	h.fd = windows.InvalidHandle
	return cancelErr
}
