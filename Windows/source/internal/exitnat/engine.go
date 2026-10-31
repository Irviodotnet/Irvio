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
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitfirewall"
	"github.com/Irviodotnet/Irvio/internal/icmpv4"
	"github.com/Irviodotnet/Irvio/internal/safetypolicy"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

type Path struct {
	Generation uint64
	Interface  uint64
	Source     netip.Addr
}

// Reservation must own an OS-exclusive protocol/source port until Close succeeds.
type Reservation interface {
	io.Closer
	Port() uint16
}

// ReplyToken identifies one reservation lifetime, including queued packets after port reuse.
type ReplyToken struct{ identity byte }

// Reserve must attach the supplied token to every captured reply; it must not re-enter Engine.
type Reserve func(context.Context, Path, uint8, *ReplyToken) (Reservation, error)

// Emit must finish its bounded native send before returning and must not call back into Engine.
type Emit func(context.Context, Path, []byte) error

type Config struct {
	Path                                                                    Path
	Reserve                                                                 Reserve
	ReserveEcho                                                             ReserveEcho
	Blocked                                                                 []netip.Prefix
	LocalAddresses                                                          []netip.Addr
	MaxLeases, MaxFlows, MaxPerLease                                        int
	UDPIdle, TCPOpeningIdle, TCPEstablishedIdle, TCPClosingIdle, Quarantine time.Duration
	Now                                                                     func() time.Time
}

func DefaultConfig(path Path, reserve Reserve) Config {
	return Config{Path: path, Reserve: reserve, MaxLeases: 64, MaxFlows: 4096, MaxPerLease: 256,
		UDPIdle: 2 * time.Minute, TCPOpeningIdle: 4 * time.Minute, TCPEstablishedIdle: 2*time.Hour + 4*time.Minute, TCPClosingIdle: 4 * time.Minute, Quarantine: 2 * time.Minute, Now: time.Now}
}

type Lease struct {
	engine *Engine
	value  tunnellease.Lease
	active bool
	flows  int
	echoes int
}
type tuple struct {
	lease      *Lease
	protocol   uint8
	sourcePort uint16
	remote     netip.AddrPort
}
type external struct {
	protocol uint8
	port     uint16
}
type internal struct {
	lease    *Lease
	protocol uint8
	port     uint16
}
type mapping struct {
	key    internal
	socket Reservation
	reply  *ReplyToken
	refs   int
}
type flow struct {
	key                                                 tuple
	mapping                                             *mapping
	expires                                             time.Time
	established, closing                                bool
	sequenceKnown, ackKnown, ackSeen, finSeen, finAcked [2]bool
	next, acked, finEnd                                 [2]uint32
}
type retired struct {
	socket Reservation
	until  time.Time
}

type Engine struct {
	mu       sync.Mutex
	c        Config
	leases   map[netip.Addr]*Lease
	flows    map[tuple]*flow
	mappings map[internal]*mapping
	reverse  map[external]*mapping
	retired  map[external]retired
	failed   []io.Closer
	echoes   map[*ReplyToken]*echoRequest
	closed   bool
}

func New(c Config) (*Engine, error) {
	if !validPath(c.Path) || c.Reserve == nil || c.Now == nil || c.MaxLeases < 1 || c.MaxLeases > 4096 || c.MaxFlows < 1 || c.MaxFlows > 65536 || c.MaxPerLease < 1 || c.MaxPerLease > c.MaxFlows {
		return nil, ErrConfig
	}
	for _, d := range []time.Duration{c.UDPIdle, c.TCPOpeningIdle, c.TCPEstablishedIdle, c.TCPClosingIdle, c.Quarantine} {
		if d <= 0 || d > 24*time.Hour {
			return nil, ErrConfig
		}
	}
	c.Blocked = append(exitfirewall.BlockedPrefixes(), c.Blocked...)
	for _, p := range c.Blocked {
		if !p.IsValid() || p.Addr().Is4In6() || p.Bits() == 0 {
			return nil, ErrConfig
		}
	}
	c.LocalAddresses = slices.Clone(c.LocalAddresses)
	for _, a := range c.LocalAddresses {
		if !a.IsValid() || a.Is4In6() {
			return nil, ErrConfig
		}
	}
	return &Engine{c: c, leases: make(map[netip.Addr]*Lease), flows: make(map[tuple]*flow), mappings: make(map[internal]*mapping), reverse: make(map[external]*mapping), retired: make(map[external]retired), echoes: make(map[*ReplyToken]*echoRequest)}, nil
}

func validPath(p Path) bool {
	return p.Generation != 0 && p.Interface != 0 && p.Source.Is4() && p.Source.IsGlobalUnicast()
}

// AddLease is called only by the resource activator after existing Core admission checks.
func (e *Engine) AddLease(v tunnellease.Lease) (*Lease, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrStale
	}
	if tunnellease.Validate(v, e.c.Now()) != nil || v.Session.IsZero() || !v.IPv4.IsValid() || v.IPv6.IsValid() {
		return nil, ErrConfig
	}
	if e.leases[v.IPv4.Addr()] != nil {
		return nil, ErrStale
	}
	for _, existing := range e.leases {
		if existing.value.ID == v.ID {
			return nil, ErrStale
		}
	}
	if len(e.leases) >= e.c.MaxLeases {
		return nil, ErrCapacity
	}
	l := &Lease{engine: e, value: v, active: true}
	e.leases[v.IPv4.Addr()] = l
	return l, nil
}

func (e *Engine) current(l *Lease) bool {
	return l != nil && l.engine == e && l.active && e.leases[l.value.IPv4.Addr()] == l && l.value.ValidUntil.After(e.c.Now()) && !e.closed
}

func (e *Engine) Renew(l *Lease, v tunnellease.Lease) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.current(l) {
		return ErrStale
	}
	o := l.value
	if v.ID != o.ID || v.ClientPeer != o.ClientPeer || v.ExitPeer != o.ExitPeer || v.IPv4 != o.IPv4 || v.IPv6 != o.IPv6 || v.Session != o.Session || v.Generation < o.Generation || tunnellease.Validate(v, e.c.Now()) != nil {
		return ErrStale
	}
	if v.Generation == o.Generation && !v.ValidUntil.Equal(o.ValidUntil) {
		return ErrStale
	}
	l.value = v
	return nil
}

func (e *Engine) blocked(a netip.Addr) bool {
	if !a.Is4() || !a.IsGlobalUnicast() || a == e.c.Path.Source || slices.Contains(e.c.LocalAddresses, a) || e.leases[a] != nil {
		return true
	}
	for _, prefix := range e.c.Blocked {
		if prefix.Contains(a) {
			return true
		}
	}
	return false
}

func (e *Engine) Outbound(ctx context.Context, l *Lease, b []byte, emit Emit) error {
	if ctx == nil || emit == nil {
		return ErrConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := parse(b)
	if err != nil {
		return err
	}
	if p.protocol == ICMP {
		return e.outboundEcho(ctx, l, p, emit)
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
	transport := safetypolicy.TransportTCP
	if p.protocol == UDP {
		transport = safetypolicy.TransportUDP
	}
	if safetypolicy.Blocks(safetypolicy.V0, transport, p.destinationPort) {
		return ErrDenied
	}
	key := tuple{l, p.protocol, p.sourcePort, netip.AddrPortFrom(p.destination, p.destinationPort)}
	f := e.flows[key]
	if f != nil && !f.expires.After(e.c.Now()) {
		e.retire(f)
		f = nil
	}
	created := false
	if f == nil {
		if p.protocol == TCP && (p.flags&0x17 != 2) {
			return ErrDenied
		}
		if len(e.flows)+len(e.echoes)+len(e.retired)+len(e.failed) >= e.c.MaxFlows || l.flows >= e.c.MaxPerLease {
			return ErrCapacity
		}
		ik := internal{l, p.protocol, p.sourcePort}
		m := e.mappings[ik]
		if m == nil {
			reply := &ReplyToken{identity: 1}
			socket, err := e.c.Reserve(ctx, e.c.Path, p.protocol, reply)
			if err != nil {
				return errors.Join(err, e.discard(socket))
			}
			if socket == nil {
				return ErrConfig
			}
			ex := external{p.protocol, socket.Port()}
			if ex.port == 0 || e.reverse[ex] != nil || e.retired[ex].socket != nil {
				return errors.Join(ErrConfig, e.discard(socket))
			}
			m = &mapping{key: ik, socket: socket, reply: reply}
			e.mappings[ik] = m
			e.reverse[ex] = m
		}
		f = &flow{key: key, mapping: m}
		m.refs++
		e.flows[key] = f
		l.flows++
		created = true
	}
	if err := emit(ctx, e.c.Path, rewrite(p, e.c.Path.Source, f.mapping.socket.Port(), true)); err != nil {
		if created {
			e.retire(f)
		}
		return err
	}
	if e.touch(f, p, false) {
		e.retire(f)
	}
	return nil
}

// Inbound requires the original reservation token, physical path, remote tuple and active lease.
func (e *Engine) Inbound(ctx context.Context, path Path, token *ReplyToken, b []byte, emit Emit) error {
	if ctx == nil || emit == nil {
		return ErrConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(b) >= 20 && b[9] == 1 {
		if len(b) >= 28 && b[20] == 0 {
			return e.inboundEcho(ctx, path, token, b, emit)
		}
		return e.inboundError(ctx, path, token, b, emit)
	}
	p, err := parse(b)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.closed || path != e.c.Path || p.destination != path.Source {
		return ErrStale
	}
	m := e.reverse[external{p.protocol, p.destinationPort}]
	if m == nil || token == nil || token != m.reply {
		return ErrStale
	}
	f := e.flows[tuple{m.key.lease, p.protocol, m.key.port, netip.AddrPortFrom(p.source, p.sourcePort)}]
	if f == nil || !e.current(f.key.lease) || !f.expires.After(e.c.Now()) {
		return ErrStale
	}
	if f.key.remote != netip.AddrPortFrom(p.source, p.sourcePort) || e.blocked(p.source) {
		return ErrDenied
	}
	if p.protocol == TCP && !f.established && p.flags&6 == 0 && !f.sequenceKnown[1] {
		return ErrDenied
	}
	if err := emit(ctx, path, rewrite(p, f.key.lease.value.IPv4.Addr(), f.key.sourcePort, false)); err != nil {
		return err
	}
	if e.touch(f, p, true) {
		e.retire(f)
	}
	return nil
}

// ICMP errors may describe existing flows but cannot create or extend their lifetime.
func (e *Engine) inboundError(ctx context.Context, path Path, token *ReplyToken, b []byte, emit Emit) error {
	p, err := icmpv4.ParseError(b)
	if err != nil {
		return errors.Join(ErrPacket, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.closed || path != e.c.Path || p.Recipient != path.Source || p.OriginalSource != path.Source {
		return ErrStale
	}
	m := e.reverse[external{p.Protocol, p.SourcePort}]
	if m == nil || token == nil || token != m.reply {
		return ErrStale
	}
	remote := netip.AddrPortFrom(p.OriginalDestination, p.DestinationPort)
	f := e.flows[tuple{m.key.lease, p.Protocol, m.key.port, remote}]
	if f == nil || !e.current(f.key.lease) || !f.expires.After(e.c.Now()) {
		return ErrStale
	}
	if e.blocked(remote.Addr()) {
		return ErrDenied
	}
	translated, err := p.Translate(f.key.lease.value.IPv4.Addr(), f.key.sourcePort)
	if err != nil {
		return errors.Join(ErrPacket, err)
	}
	return emit(ctx, path, translated)
}

func seqAfter(a, b uint32) bool { return int32(a-b) > 0 }

func (e *Engine) touch(f *flow, p packet, inbound bool) bool {
	d := e.c.UDPIdle
	if p.protocol == TCP {
		direction := 0
		if inbound {
			direction = 1
		}
		other := direction ^ 1
		if p.flags&4 != 0 {
			return f.ackKnown[direction] && p.sequence == f.acked[direction] ||
				!f.established && p.flags&16 != 0 && f.sequenceKnown[other] && p.ack == f.next[other]
		}
		if p.flags&16 != 0 && f.sequenceKnown[other] && !seqAfter(p.ack, f.next[other]) {
			f.ackSeen[direction] = true
			if !f.ackKnown[other] || seqAfter(p.ack, f.acked[other]) {
				f.acked[other] = p.ack
				f.ackKnown[other] = true
			}
			if f.finSeen[other] && !seqAfter(f.finEnd[other], p.ack) {
				f.finAcked[other] = true
			}
		}
		if !f.sequenceKnown[direction] || seqAfter(p.end, f.next[direction]) {
			f.next[direction] = p.end
			f.sequenceKnown[direction] = true
		}
		if p.flags&1 != 0 && !f.finAcked[direction] {
			f.finSeen[direction] = true
			f.finEnd[direction] = p.end
		}
		f.established = f.ackSeen[0] && f.ackSeen[1]
		f.closing = f.finAcked[0] && f.finAcked[1]
		d = e.c.TCPOpeningIdle
		if f.established {
			d = e.c.TCPEstablishedIdle
		}
		if f.closing {
			d = e.c.TCPClosingIdle
		}
	}
	f.expires = e.c.Now().Add(d)
	return false
}

func (e *Engine) retire(f *flow) {
	delete(e.flows, f.key)
	f.key.lease.flows--
	m := f.mapping
	m.refs--
	if m.refs == 0 {
		key := external{m.key.protocol, m.socket.Port()}
		delete(e.reverse, key)
		delete(e.mappings, m.key)
		e.retired[key] = retired{m.socket, e.c.Now().Add(e.c.Quarantine)}
	}
}

func (e *Engine) discard(r io.Closer) error {
	if r == nil {
		return nil
	}
	err := r.Close()
	if err != nil {
		e.failed = append(e.failed, r)
	}
	return err
}

func (e *Engine) retryFailed() error {
	var result error
	remaining := e.failed[:0]
	for _, r := range e.failed {
		if err := r.Close(); err != nil {
			result = errors.Join(result, err)
			remaining = append(remaining, r)
		}
	}
	clear(e.failed[len(remaining):])
	e.failed = remaining
	return result
}

func (e *Engine) remove(l *Lease) {
	l.active = false
	delete(e.leases, l.value.IPv4.Addr())
	for token, request := range e.echoes {
		if request.lease == l {
			e.retireEcho(token)
		}
	}
	for _, f := range e.flows {
		if f.key.lease == l {
			e.retire(f)
		}
	}
}

// Revoke waits for admitted sends, then prevents all subsequent packets and reply delivery.
func (e *Engine) Revoke(l *Lease) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if l == nil || l.engine != e {
		return ErrStale
	}
	if !l.active {
		return nil
	}
	e.remove(l)
	return nil
}

func (e *Engine) Sweep() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.c.Now()
	for token, request := range e.echoes {
		if !request.expires.After(now) {
			e.retireEcho(token)
		}
	}
	for _, l := range e.leases {
		if !l.value.ValidUntil.After(now) {
			e.remove(l)
		}
	}
	for _, f := range e.flows {
		if !f.expires.After(now) {
			e.retire(f)
		}
	}
	result := e.retryFailed()
	for k, r := range e.retired {
		if !r.until.After(now) {
			if err := r.socket.Close(); err != nil {
				result = errors.Join(result, err)
			} else {
				delete(e.retired, k)
			}
		}
	}
	return result
}

// Reconcile invalidates old-path mappings; no flow may silently follow a new default route.
func (e *Engine) Reconcile(path Path, local []netip.Addr, blocked []netip.Prefix) error {
	local = slices.Clone(local)
	blocked = append(exitfirewall.BlockedPrefixes(), blocked...)
	for _, a := range local {
		if !a.IsValid() || a.Is4In6() {
			return ErrConfig
		}
	}
	for _, p := range blocked {
		if !p.IsValid() || p.Addr().Is4In6() || p.Bits() == 0 {
			return ErrConfig
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || !validPath(path) || path.Generation <= e.c.Path.Generation {
		return ErrStale
	}
	for token := range e.echoes {
		e.retireEcho(token)
	}
	for _, f := range e.flows {
		e.retire(f)
	}
	e.c.Path = path
	e.c.LocalAddresses, e.c.Blocked = local, blocked
	return nil
}

type Stats struct{ Leases, Flows, Mappings, Reserved int }

func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Stats{len(e.leases), len(e.flows) + len(e.echoes), len(e.mappings) + len(e.echoes), len(e.mappings) + len(e.echoes) + len(e.retired) + len(e.failed)}
}

// Close retains failed reservations for an explicit cleanup retry, never reporting them released.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	for _, l := range e.leases {
		e.remove(l)
	}
	result := e.retryFailed()
	for k, r := range e.retired {
		if err := r.socket.Close(); err != nil {
			result = errors.Join(result, err)
		} else {
			delete(e.retired, k)
		}
	}
	return result
}
