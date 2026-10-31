// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"time"
)

// EchoCapture is an exact ephemeral reply capability, never an OS ICMP identifier reservation.
type EchoCapture struct {
	Remote               netip.Addr
	Identifier, Sequence uint16
	Nonce                [16]byte
}

// ReserveEcho must capture only this nonce/tuple and attach token to every reply until Close completes.
type ReserveEcho func(context.Context, Path, EchoCapture, *ReplyToken) (io.Closer, error)

const maxEchoRequests = 128
const maxEchoPerLease = 16
const echoLifetime = 5 * time.Second

type echoRequest struct {
	lease    *Lease
	capture  EchoCapture
	resource io.Closer
	expires  time.Time
	original [44]byte
	size     int
}

func (e *Engine) outboundEcho(ctx context.Context, l *Lease, p packet, emit Emit) error {
	if p.raw[20] != 8 {
		return ErrDenied
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !e.current(l) {
		return ErrStale
	}
	if p.source != l.value.IPv4.Addr() || e.blocked(p.destination) {
		return ErrDenied
	}
	if e.c.ReserveEcho == nil {
		return ErrUnsupported
	}
	if len(e.echoes) >= maxEchoRequests || l.echoes >= maxEchoPerLease || l.flows >= e.c.MaxPerLease ||
		len(e.flows)+len(e.echoes)+len(e.retired)+len(e.failed) >= e.c.MaxFlows {
		return ErrCapacity
	}
	var entropy [20]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return err
	}
	request := &echoRequest{lease: l, size: len(p.raw), expires: e.c.Now().Add(min(echoLifetime, e.c.UDPIdle))}
	request.capture = EchoCapture{Remote: p.destination, Identifier: binary.BigEndian.Uint16(entropy[16:18]), Sequence: binary.BigEndian.Uint16(entropy[18:20])}
	copy(request.capture.Nonce[:], entropy[:16])
	copy(request.original[:], p.raw)
	token := &ReplyToken{identity: 1}
	resource, err := e.c.ReserveEcho(ctx, e.c.Path, request.capture, token)
	if err != nil || resource == nil {
		return errors.Join(err, ErrConfig, e.discard(resource))
	}
	request.resource = resource
	e.echoes[token] = request
	l.flows++
	l.echoes++
	if err := ctx.Err(); err != nil {
		e.retireEcho(token)
		return err
	}
	if !e.current(l) || !request.expires.After(e.c.Now()) {
		e.retireEcho(token)
		return ErrStale
	}
	// Replace a saved prefix rather than expanding MTU-sized requests; tiny payloads grow only to 16 bytes.
	wire := make([]byte, max(44, len(p.raw)))
	copy(wire, p.raw)
	copy(wire[12:16], e.c.Path.Source.AsSlice())
	binary.BigEndian.PutUint16(wire[2:4], uint16(len(wire)))
	binary.BigEndian.PutUint16(wire[24:26], request.capture.Identifier)
	binary.BigEndian.PutUint16(wire[26:28], request.capture.Sequence)
	copy(wire[28:44], request.capture.Nonce[:])
	wire[8]--
	echoChecksums(wire)
	if err := emit(ctx, e.c.Path, wire); err != nil {
		e.retireEcho(token)
		return err
	}
	return nil
}

func (e *Engine) inboundEcho(ctx context.Context, path Path, token *ReplyToken, b []byte, emit Emit) error {
	p, err := parse(b)
	if err != nil {
		return err
	}
	if p.protocol != ICMP || b[20] != 0 || len(b) < 44 {
		return ErrPacket
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	request := e.echoes[token]
	if e.closed || path != e.c.Path || p.destination != path.Source || request == nil ||
		!e.current(request.lease) || !request.expires.After(e.c.Now()) {
		return ErrStale
	}
	if p.source != request.capture.Remote || p.sourcePort != request.capture.Identifier || p.destinationPort != request.capture.Sequence ||
		len(b) != max(44, request.size) || !bytes.Equal(b[28:44], request.capture.Nonce[:]) || e.blocked(p.source) {
		return ErrDenied
	}
	out := append([]byte(nil), b[:request.size]...)
	copy(out[16:20], request.lease.value.IPv4.Addr().AsSlice())
	binary.BigEndian.PutUint16(out[2:4], uint16(len(out)))
	copy(out[24:min(44, len(out))], request.original[24:min(44, len(out))])
	out[8]--
	echoChecksums(out)
	// One response consumes the capability even if downstream delivery fails.
	defer e.retireEcho(token)
	return emit(ctx, path, out)
}

func echoChecksums(b []byte) {
	b[10], b[11], b[22], b[23] = 0, 0, 0, 0
	binary.BigEndian.PutUint16(b[10:12], checksum(b[:20]))
	binary.BigEndian.PutUint16(b[22:24], checksum(b[20:]))
}

func (e *Engine) retireEcho(token *ReplyToken) {
	request := e.echoes[token]
	if request == nil {
		return
	}
	delete(e.echoes, token)
	request.lease.flows--
	request.lease.echoes--
	_ = e.discard(request.resource)
}
