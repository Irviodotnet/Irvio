// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package exitbridge separates authenticated AWG plaintext from host and downstream forwarding.
package exitbridge

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

var (
	ErrConfig = errors.New("invalid plaintext bridge configuration")
	ErrStale  = errors.New("inactive plaintext bridge attachment")
	ErrFull   = errors.New("plaintext bridge capacity exhausted")
)

// Forward must honor cancellation, avoid lifecycle re-entry, and enforce the active lease policy synchronously.
type Forward func(context.Context, *Peer, []byte) error

type Config struct {
	TUN     tun.Device
	Control netip.AddrPort
	Forward Forward
	Limit   int
	Traffic *Traffic
}

// Peer is a local lifetime token, not a protocol state or a replacement for Core admission.
type Peer struct {
	bridge   *Bridge
	address  netip.Addr
	upstream bool
	control  netip.AddrPort
	active   bool
	users    int
	changed  chan struct{}
}

type queued struct {
	peer   *Peer
	packet []byte
}

type Bridge struct {
	c         Config
	ctx       context.Context
	cancel    context.CancelFunc
	queue     chan queued
	pumpDone  chan struct{}
	mu        sync.Mutex
	peers     map[netip.Addr]*Peer
	own       *Peer
	inFlight  *Peer
	closed    bool
	err       error
	readMu    sync.Mutex
	closeOnce sync.Once
	closeErr  error
	dropped   atomic.Uint64
	users     sync.WaitGroup
}

var _ tun.Device = (*Bridge)(nil)

// New owns one borrowed TUN on success; the adapter owner remains responsible for deleting its interface.
func New(c Config) (*Bridge, error) {
	if c.TUN == nil || c.TUN.BatchSize() != 1 || c.Forward == nil || !validAddress(c.Control.Addr()) || c.Control.Port() == 0 || c.Limit < 1 || c.Limit > 4096 {
		return nil, ErrConfig
	}
	b := &Bridge{c: c, queue: make(chan queued, 128), pumpDone: make(chan struct{}), peers: make(map[netip.Addr]*Peer)}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	go b.pump()
	return b, nil
}

func validAddress(a netip.Addr) bool       { return a.Is4() && a.IsGlobalUnicast() }
func (b *Bridge) File() *os.File           { return b.c.TUN.File() }
func (b *Bridge) Name() (string, error)    { return b.c.TUN.Name() }
func (b *Bridge) MTU() (int, error)        { return b.c.TUN.MTU() }
func (b *Bridge) Events() <-chan tun.Event { return b.c.TUN.Events() }
func (b *Bridge) BatchSize() int           { return 1 }
func (b *Bridge) Dropped() uint64          { return b.dropped.Load() }

func (b *Bridge) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	if b.closed {
		return os.ErrClosed
	}
	return nil
}

// Attach must follow authenticated AWG ownership; downstream data remains subject to Forward's lease gate.
func (b *Bridge) Attach(address netip.Addr, upstream bool, control ...netip.AddrPort) (*Peer, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.ctx.Err() != nil {
		return nil, ErrStale
	}
	if !validAddress(address) || address == b.c.Control.Addr() || b.peers[address] != nil || upstream && b.own != nil {
		return nil, ErrConfig
	}
	if len(b.peers) >= b.c.Limit {
		return nil, ErrFull
	}
	if len(control) > 1 || len(control) == 1 && (!upstream || !validAddress(control[0].Addr()) || control[0].Port() == 0) {
		return nil, ErrConfig
	}
	p := &Peer{bridge: b, address: address, upstream: upstream, active: true, changed: make(chan struct{}, 1)}
	if len(control) == 1 {
		p.control = control[0]
	}
	b.peers[address] = p
	if upstream {
		b.own = p
	}
	return p, nil
}

func (p *Peer) Address() netip.Addr { return p.address }
func signal(p *Peer) {
	if p != nil {
		select {
		case p.changed <- struct{}{}:
		default:
		}
	}
}

// Retire fences queued data and waits for the next AWG Read boundary before peer removal/address reuse.
func (b *Bridge) Retire(ctx context.Context, p *Peer) error {
	if ctx == nil || p == nil || p.bridge != b {
		return ErrConfig
	}
	for {
		b.mu.Lock()
		p.active = false
		closed := b.closed || b.ctx.Err() != nil
		ready := p.users == 0 && b.inFlight != p
		b.mu.Unlock()
		if closed {
			return os.ErrClosed
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.ctx.Done():
			return os.ErrClosed
		case <-p.changed:
		}
	}
}

// Forget is allowed only after successful Retire and removal of the old authenticated AWG peer.
func (b *Bridge) Forget(p *Peer) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p == nil || p.bridge != b || p.active || p.users != 0 || b.inFlight == p {
		return ErrStale
	}
	if b.peers[p.address] == p {
		delete(b.peers, p.address)
	}
	if b.own == p {
		b.own = nil
	}
	return nil
}

func (b *Bridge) acquire(p *Peer) bool {
	if p == nil || !p.active || b.closed || b.ctx.Err() != nil {
		return false
	}
	p.users++
	b.users.Add(1)
	return true
}
func (b *Bridge) release(p *Peer) {
	b.mu.Lock()
	p.users--
	signal(p)
	b.mu.Unlock()
	b.users.Done()
}

func (b *Bridge) enqueue(ctx context.Context, p *Peer, packet []byte) error {
	if ctx == nil || p == nil || p.bridge != b {
		return ErrConfig
	}
	b.mu.Lock()
	active := b.acquire(p)
	b.mu.Unlock()
	if !active {
		return ErrStale
	}
	defer b.release(p)
	q := queued{p, append([]byte(nil), packet...)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.ctx.Done():
		return os.ErrClosed
	case b.queue <- q:
		return nil
	default:
		b.dropped.Add(1)
		return ErrFull
	}
}

// Reply accepts only an already-authorized NAT reply for this exact downstream lifetime.
func (b *Bridge) Reply(ctx context.Context, p *Peer, packet []byte) error {
	m, ok := frame(packet)
	if !ok || p == nil || p.upstream || m.dst != p.address {
		return ErrConfig
	}
	return b.enqueue(ctx, p, packet)
}

func (b *Bridge) fail(err error) {
	b.mu.Lock()
	if b.err == nil {
		b.err = err
	}
	b.mu.Unlock()
	b.cancel()
}

func (b *Bridge) pump() {
	defer close(b.pumpDone)
	buffer := make([]byte, 65535)
	sizes := make([]int, 1)
	for b.ctx.Err() == nil {
		n, err := b.c.TUN.Read([][]byte{buffer}, sizes, 0)
		if err != nil {
			if b.ctx.Err() == nil {
				b.fail(err)
			}
			return
		}
		if n != 1 || sizes[0] < 0 || sizes[0] > len(buffer) {
			b.fail(io.ErrUnexpectedEOF)
			return
		}
		packet := buffer[:sizes[0]]
		m, ok := frame(packet)
		if !ok {
			b.dropped.Add(1)
			continue
		}
		b.mu.Lock()
		var p *Peer
		if b.own != nil && m.src == b.own.address {
			if destination := b.peers[m.dst]; destination == nil || destination.upstream {
				p = b.own
			}
		} else if m.src == b.c.Control.Addr() && m.tcp && m.sport == b.c.Control.Port() {
			p = b.peers[m.dst]
			if p != nil && p.upstream {
				p = nil
			}
		}
		b.mu.Unlock()
		if err := b.enqueue(b.ctx, p, packet); err != nil && !errors.Is(err, ErrFull) {
			b.dropped.Add(1)
		}
	}
}

// Read's next invocation proves that the pinned AWG reader has associated the previous packet with a peer.
func (b *Bridge) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	b.readMu.Lock()
	defer b.readMu.Unlock()
	b.mu.Lock()
	signal(b.inFlight)
	b.inFlight = nil
	b.mu.Unlock()
	if len(bufs) < 1 || len(sizes) < 1 || offset < 0 || offset > len(bufs[0]) {
		return 0, ErrConfig
	}
	for {
		select {
		case <-b.ctx.Done():
			return 0, os.ErrClosed
		case q := <-b.queue:
			b.mu.Lock()
			valid := q.peer.active && !b.closed && b.ctx.Err() == nil
			if valid && len(q.packet) <= len(bufs[0])-offset {
				b.inFlight = q.peer
			}
			b.mu.Unlock()
			if !valid {
				b.dropped.Add(1)
				continue
			}
			if len(q.packet) > len(bufs[0])-offset {
				return 0, io.ErrShortBuffer
			}
			copy(bufs[0][offset:], q.packet)
			sizes[0] = len(q.packet)
			if b.c.Traffic != nil && q.peer.upstream {
				m, _ := frame(q.packet)
				if !q.peer.isControl(m, true) {
					b.c.Traffic.myUpload.Add(uint64(len(q.packet)))
				}
			}
			return 1, nil
		}
	}
}

// Write receives only AWG-authenticated plaintext after cryptokey source validation, never untrusted raw input.
func (b *Bridge) Write(bufs [][]byte, offset int) (int, error) {
	if b.ctx.Err() != nil {
		return 0, os.ErrClosed
	}
	for i, buffer := range bufs {
		if offset < 0 || offset > len(buffer) {
			return i, ErrConfig
		}
		packet := buffer[offset:]
		m, ok := frame(packet)
		if !ok {
			b.dropped.Add(1)
			continue
		}
		b.mu.Lock()
		p := b.peers[m.src]
		forward := p != nil && !p.upstream
		toHost := forward && m.dst == b.c.Control.Addr() && m.tcp && m.dport == b.c.Control.Port()
		if !forward {
			p = b.own
			toHost = p != nil && m.dst == p.address
		}
		accepted := b.acquire(p)
		b.mu.Unlock()
		if !accepted || !forward && !toHost {
			if accepted {
				b.release(p)
			}
			b.dropped.Add(1)
			continue
		}
		var err error
		if toHost {
			var n int
			n, err = b.c.TUN.Write([][]byte{packet}, 0)
			if err == nil && n != 1 {
				err = io.ErrShortWrite
			}
		} else {
			ctx, cancel := context.WithTimeout(b.ctx, 2*time.Second)
			err = b.c.Forward(ctx, p, packet)
			cancel()
		}
		if err == nil && b.c.Traffic != nil {
			if forward && !toHost {
				b.c.Traffic.sharedUpload.Add(uint64(len(packet)))
			} else if !forward && toHost && !p.isControl(m, false) {
				b.c.Traffic.myDownload.Add(uint64(len(packet)))
			}
		}
		b.release(p)
		if err != nil {
			b.dropped.Add(1)
			if toHost {
				b.fail(err)
				return i, err
			}
		}
	}
	return len(bufs), nil
}

func (b *Bridge) Close() error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		for _, p := range b.peers {
			p.active = false
			signal(p)
		}
		b.mu.Unlock()
		b.cancel()
		b.closeErr = b.c.TUN.Close()
		<-b.pumpDone
		b.users.Wait()
		b.readMu.Lock()
		b.mu.Lock()
		b.inFlight = nil
		b.mu.Unlock()
		b.readMu.Unlock()
		for len(b.queue) > 0 {
			<-b.queue
		}
	})
	return b.closeErr
}

type packetFrame struct {
	src, dst     netip.Addr
	udp, tcp     bool
	sport, dport uint16
}

func frame(p []byte) (packetFrame, bool) {
	if len(p) < 20 || len(p) > 65535 || p[0] != 0x45 || int(binary.BigEndian.Uint16(p[2:4])) != len(p) || binary.BigEndian.Uint16(p[6:8])&0xbfff != 0 {
		return packetFrame{}, false
	}
	m := packetFrame{src: netip.AddrFrom4([4]byte(p[12:16])), dst: netip.AddrFrom4([4]byte(p[16:20]))}
	if p[9] == 17 && len(p) >= 28 && int(binary.BigEndian.Uint16(p[24:26])) == len(p)-20 {
		m.sport = binary.BigEndian.Uint16(p[20:22])
		m.dport = binary.BigEndian.Uint16(p[22:24])
		m.udp = m.sport != 0 && m.dport != 0
	}
	if p[9] == 6 && len(p) >= 40 {
		header := int(p[32]>>4) * 4
		if header >= 20 && header <= len(p)-20 {
			m.sport = binary.BigEndian.Uint16(p[20:22])
			m.dport = binary.BigEndian.Uint16(p[22:24])
			m.tcp = m.sport != 0 && m.dport != 0
		}
	}
	return m, true
}
