// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package nativeecho is a lab-only API experiment, not a ProtectedMode-safe Exit backend.
package nativeecho

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

var ErrReply = errors.New("invalid Windows echo reply")

type StatusError uint32

func (s StatusError) Error() string { return fmt.Sprintf("Windows echo status %d", uint32(s)) }

type Options struct {
	TTL, TOS     uint8
	DontFragment bool
	Timeout      time.Duration
}

type Result struct {
	Source   netip.Addr
	TTL, TOS uint8
	Status   uint32
	Data     []byte
}

var api = windows.NewLazySystemDLL("iphlpapi.dll")
var create = api.NewProc("IcmpCreateFile")
var closeEcho = api.NewProc("IcmpCloseHandle")
var send = api.NewProc("IcmpSendEcho2Ex")
var parse = api.NewProc("IcmpParseReplies")

type Request struct {
	mu             sync.Mutex
	handle, event  windows.Handle
	binding        winroute.Binding
	target         netip.Addr
	payload, reply []byte
	options        [16]byte
	pin            runtime.Pinner
	pending, done  bool
	result         Result
	err            error
}

// Begin starts one bounded native operation; its owner must call Finish even after cancellation.
func Begin(ctx context.Context, binding winroute.Binding, target netip.Addr, payload []byte, options Options) (*Request, error) {
	if ctx == nil || runtime.GOARCH != "amd64" || !target.Is4() || !target.IsGlobalUnicast() || target.IsPrivate() || target.IsLoopback() ||
		len(payload) > 65507 || options.TTL == 0 || options.Timeout < time.Millisecond || options.Timeout > 2*time.Second {
		return nil, ErrReply
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validPath(binding, target); err != nil {
		return nil, err
	}
	r := &Request{binding: binding, target: target, payload: append([]byte(nil), payload...), reply: make([]byte, 128+len(payload))}
	if len(r.payload) == 0 {
		r.payload = make([]byte, 0, 1)
	}
	h, _, err := create.Call()
	if h == 0 || windows.Handle(h) == windows.InvalidHandle {
		return nil, err
	}
	r.handle = windows.Handle(h)
	fail := func(cause error) (*Request, error) {
		r.err = cause
		if cleanup := r.close(); cleanup != nil {
			return r, errors.Join(cause, cleanup)
		}
		return nil, cause
	}
	r.event, err = windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return fail(err)
	}
	r.options[0], r.options[1] = options.TTL, options.TOS
	if options.DontFragment {
		r.options[2] = 2
	}
	data := r.payload[:cap(r.payload)]
	r.pin.Pin(&data[0])
	r.pin.Pin(&r.reply[0])
	r.pin.Pin(&r.options[0])
	source, destination := binding.Source.As4(), target.As4()
	n, _, err := send.Call(h, uintptr(r.event), 0, 0, uintptr(binary.LittleEndian.Uint32(source[:])), uintptr(binary.LittleEndian.Uint32(destination[:])),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(payload)), uintptr(unsafe.Pointer(&r.options[0])),
		uintptr(unsafe.Pointer(&r.reply[0])), uintptr(len(r.reply)), uintptr((options.Timeout+time.Millisecond-1)/time.Millisecond))
	runtime.KeepAlive(r)
	if n == 0 && errors.Is(err, windows.ERROR_IO_PENDING) {
		r.pending = true
		return r, nil
	}
	if n == 0 {
		return fail(err)
	}
	if n != 1 {
		return fail(ErrReply)
	}
	return r, nil
}

func validPath(binding winroute.Binding, target netip.Addr) error {
	b, err := winroute.ForSource(binding.Source)
	if err != nil || b != binding {
		return errors.Join(ErrReply, err)
	}
	p, err := winroute.ResolveUnderlay(target, binding.LUID, nil)
	if err != nil || p.Binding != binding {
		return errors.Join(ErrReply, err)
	}
	return nil
}

// Finish drains kernel buffer ownership before returning cancellation; the native timeout remains bounded.
func (r *Request) Finish(ctx context.Context) (Result, error) {
	if r == nil || ctx == nil {
		return Result{}, ErrReply
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return cloneResult(r.result), r.err
	}
	var canceled error
	if r.pending {
		for {
			if err := ctx.Err(); err != nil {
				canceled = err
			}
			status, err := windows.WaitForSingleObject(r.event, 25)
			if err != nil || status != windows.WAIT_OBJECT_0 && status != uint32(windows.WAIT_TIMEOUT) {
				// Keep the native buffers pinned and the owner retryable when completion cannot be established.
				return Result{}, errors.Join(ErrReply, err)
			}
			if status == windows.WAIT_OBJECT_0 {
				break
			}
		}
		n, _, err := parse.Call(uintptr(unsafe.Pointer(&r.reply[0])), uintptr(len(r.reply)))
		runtime.KeepAlive(r)
		if n != 1 {
			r.err = errors.Join(ErrReply, err)
		}
	}
	if r.err == nil {
		r.result, r.err = decodeReply(r.reply, r.target, r.payload)
	}
	if r.err == nil {
		r.err = validPath(r.binding, r.target)
	}
	r.err = errors.Join(r.err, canceled, ctx.Err(), r.close())
	r.done = true
	return cloneResult(r.result), r.err
}

// Close drains an admitted request and retries native cleanup without treating a network timeout as residue.
func (r *Request) Close() error {
	if r == nil {
		return nil
	}
	_, err := r.Finish(context.Background())
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.done {
		return err
	}
	return r.close()
}

func cloneResult(r Result) Result { r.Data = bytes.Clone(r.Data); return r }

func decodeReply(b []byte, target netip.Addr, payload []byte) (Result, error) {
	if len(b) < 40 {
		return Result{}, ErrReply
	}
	r := Result{Source: netip.AddrFrom4([4]byte(b[:4])), Status: binary.LittleEndian.Uint32(b[4:8]), TTL: b[24], TOS: b[25]}
	if r.Status != 0 {
		return r, StatusError(r.Status)
	}
	if r.Source != target || r.TTL == 0 {
		return Result{}, ErrReply
	}
	size := int(binary.LittleEndian.Uint16(b[12:14]))
	pointer := uintptr(binary.LittleEndian.Uint64(b[16:24]))
	base := uintptr(unsafe.Pointer(&b[0]))
	if size != len(payload) || pointer < base || pointer-base < 40 || pointer-base > uintptr(len(b)) || uintptr(size) > uintptr(len(b))-(pointer-base) {
		return Result{}, ErrReply
	}
	offset := int(pointer - base)
	if !bytes.Equal(b[offset:offset+size], payload) {
		return Result{}, ErrReply
	}
	r.Data = bytes.Clone(b[offset : offset+size])
	return r, nil
}

func (r *Request) close() error {
	var err error
	if r.handle != 0 {
		ok, _, native := closeEcho.Call(uintptr(r.handle))
		if ok == 0 {
			err = native
		} else {
			r.handle = 0
		}
	}
	if r.event != 0 {
		if native := windows.CloseHandle(r.event); native != nil {
			err = errors.Join(err, native)
		} else {
			r.event = 0
		}
	}
	r.pin.Unpin()
	return err
}
