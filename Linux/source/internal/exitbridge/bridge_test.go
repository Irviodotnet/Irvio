// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitbridge

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

type hostTUN struct {
	read, write chan []byte
	closed      chan struct{}
	events      chan tun.Event
	once        sync.Once
}

func newHostTUN() *hostTUN {
	return &hostTUN{read: make(chan []byte, 16), write: make(chan []byte, 16), closed: make(chan struct{}), events: make(chan tun.Event)}
}
func (h *hostTUN) File() *os.File           { return nil }
func (h *hostTUN) Name() (string, error)    { return "fixture", nil }
func (h *hostTUN) MTU() (int, error)        { return 1420, nil }
func (h *hostTUN) BatchSize() int           { return 1 }
func (h *hostTUN) Events() <-chan tun.Event { return h.events }
func (h *hostTUN) Close() error             { h.once.Do(func() { close(h.closed); close(h.events) }); return nil }
func (h *hostTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case <-h.closed:
		return 0, os.ErrClosed
	case p := <-h.read:
		sizes[0] = copy(bufs[0][offset:], p)
		return 1, nil
	}
}
func (h *hostTUN) Write(bufs [][]byte, offset int) (int, error) {
	for i, p := range bufs {
		select {
		case <-h.closed:
			return i, os.ErrClosed
		case h.write <- append([]byte(nil), p[offset:]...):
		}
	}
	return len(bufs), nil
}

var control = netip.MustParseAddrPort("10.17.0.1:7890")
var downstream = netip.MustParseAddr("10.17.0.2")
var own = netip.MustParseAddr("10.23.0.2")
var internet = netip.MustParseAddr("1.1.1.1")

func udp(src, dst netip.Addr, sport, dport uint16, tag byte) []byte {
	p := make([]byte, 29)
	p[0] = 0x45
	p[8] = 64
	p[9] = 17
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	copy(p[12:16], src.AsSlice())
	copy(p[16:20], dst.AsSlice())
	binary.BigEndian.PutUint16(p[20:22], sport)
	binary.BigEndian.PutUint16(p[22:24], dport)
	binary.BigEndian.PutUint16(p[24:26], 9)
	p[28] = tag
	return p
}

func tcp(src, dst netip.Addr, sport, dport uint16, tag byte) []byte {
	p := make([]byte, 41)
	copy(p, udp(src, dst, sport, dport, tag))
	p[9] = 6
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	clear(p[24:])
	p[32], p[33], p[40] = 0x50, 0x18, tag
	return p
}

func fixture(t *testing.T, forward Forward) (*Bridge, *hostTUN, *Peer, *Peer) {
	t.Helper()
	h := newHostTUN()
	if forward == nil {
		forward = func(context.Context, *Peer, []byte) error { return ErrStale }
	}
	b, err := New(Config{TUN: h, Control: control, Forward: forward, Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	p, err := b.Attach(downstream, false)
	if err != nil {
		t.Fatal(err)
	}
	u, err := b.Attach(own, true)
	if err != nil {
		t.Fatal(err)
	}
	return b, h, p, u
}

func readAsync(b *Bridge) <-chan queued {
	out := make(chan queued, 1)
	go func() {
		buffer := make([]byte, 1600)
		sizes := make([]int, 1)
		n, err := b.Read([][]byte{buffer}, sizes, 7)
		if err != nil || n != 1 {
			out <- queued{}
			return
		}
		out <- queued{packet: append([]byte(nil), buffer[7:7+sizes[0]]...)}
	}()
	return out
}
func receive(t *testing.T, c <-chan queued) []byte {
	t.Helper()
	select {
	case r := <-c:
		if r.packet == nil {
			t.Fatal("read failed")
		}
		return r.packet
	case <-time.After(time.Second):
		t.Fatal("read blocked")
		return nil
	}
}
func wait(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for !ready() {
		select {
		case <-deadline.C:
			t.Fatal("condition not reached")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestPlaintextSeparation(t *testing.T) {
	forwarded := make(chan queued, 8)
	b, h, p, _ := fixture(t, func(_ context.Context, p *Peer, packet []byte) error {
		forwarded <- queued{p, append([]byte(nil), packet...)}
		return nil
	})
	data := udp(downstream, internet, 30000, 53, 1)
	if n, err := b.Write([][]byte{data}, 0); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if q := <-forwarded; q.peer != p || !bytes.Equal(q.packet, data) {
		t.Fatal("wrong forwarding owner")
	}
	if len(h.write) != 0 {
		t.Fatal("downstream data entered host")
	}
	if _, err := b.Write([][]byte{tcp(downstream, control.Addr(), 30000, control.Port(), 2)}, 0); err != nil {
		t.Fatal(err)
	}
	if packet := <-h.write; packet[40] != 2 {
		t.Fatal("missing exact control packet")
	}
	b.Write([][]byte{udp(downstream, control.Addr(), 30000, control.Port()+1, 3)}, 0)
	b.Write([][]byte{udp(downstream, own, 30000, 53, 4)}, 0)
	if len(h.write) != 0 || len(forwarded) != 2 {
		t.Fatal("downstream traffic bypassed NAT policy")
	}
	b.Write([][]byte{udp(internet, own, 53, 30000, 5)}, 0)
	if packet := <-h.write; packet[28] != 5 {
		t.Fatal("own upstream reply not delivered")
	}
	b.Write([][]byte{udp(internet, control.Addr(), 53, control.Port(), 6)}, 0)
	b.Write([][]byte{[]byte{0x60}}, 0)
	if len(h.write) != 0 || b.Dropped() != 2 {
		t.Fatal("unknown traffic accepted")
	}
}

func TestHostCannotSendUserTrafficToDownstream(t *testing.T) {
	b, h, _, _ := fixture(t, nil)
	h.read <- udp(own, downstream, 1000, 80, 1)
	wait(t, func() bool { return b.Dropped() != 0 })
	if len(b.queue) != 0 {
		t.Fatal("host user packet could select downstream AWG peer")
	}
	h.read <- tcp(control.Addr(), downstream, control.Port(), 30000, 2)
	if p := receive(t, readAsync(b)); p[40] != 2 {
		t.Fatal("control path unavailable")
	}
	h.read <- udp(own, internet, 1000, 443, 3)
	if p := receive(t, readAsync(b)); p[28] != 3 {
		t.Fatal("own upstream path unavailable")
	}
}

func TestControlExceptionRequiresExactWellFormedTCP(t *testing.T) {
	forwarded := 0
	b, h, _, _ := fixture(t, func(context.Context, *Peer, []byte) error { forwarded++; return ErrStale })
	malformed := tcp(downstream, control.Addr(), 30000, control.Port(), 1)
	malformed[32] = 0x40
	for _, packet := range [][]byte{
		udp(downstream, control.Addr(), 30000, control.Port(), 1),
		tcp(downstream, control.Addr(), 30000, control.Port()+1, 1),
		tcp(downstream, own, 30000, control.Port(), 1), malformed,
	} {
		if _, err := b.Write([][]byte{packet}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.write) != 0 || forwarded != 4 {
		t.Fatal("control exception bypassed protocol/address/port validation")
	}
	before := b.Dropped()
	h.read <- udp(control.Addr(), downstream, control.Port(), 30000, 1)
	wait(t, func() bool { return b.Dropped() > before })
	if len(b.queue) != 0 {
		t.Fatal("UDP entered control reply lane")
	}
}

func TestTCPControlReachesHostAndReturns(t *testing.T) {
	b, h, _, _ := fixture(t, nil)
	request := tcp(downstream, control.Addr(), 30000, control.Port(), 9)
	if _, err := b.Write([][]byte{request}, 0); err != nil {
		t.Fatal(err)
	}
	select {
	case packet := <-h.write:
		if !bytes.Equal(packet, request) {
			t.Fatal("TCP control changed")
		}
	default:
		t.Fatal("production TCP control was not delivered to host")
	}
	reply := tcp(control.Addr(), downstream, control.Port(), 30000, 10)
	h.read <- reply
	if packet := receive(t, readAsync(b)); !bytes.Equal(packet, reply) {
		t.Fatal("TCP control reply changed")
	}
}

func TestFullHostQueueCountsEachDropOnce(t *testing.T) {
	b, h, p, _ := fixture(t, nil)
	for i := 0; i < cap(b.queue); i++ {
		if err := b.Reply(context.Background(), p, udp(internet, downstream, 53, 30000, 1)); err != nil {
			t.Fatal(err)
		}
	}
	h.read <- udp(own, internet, 30000, 53, 2)
	wait(t, func() bool { return b.Dropped() != 0 })
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if b.Dropped() != 1 {
		t.Fatalf("one dropped packet counted %d times", b.Dropped())
	}
}

func TestRetireFencesReturnedAndQueuedPackets(t *testing.T) {
	b, _, old, _ := fixture(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	packet := udp(internet, downstream, 53, 30000, 1)
	if err := b.Reply(ctx, old, packet); err != nil {
		t.Fatal(err)
	}
	if p := receive(t, readAsync(b)); p[28] != 1 {
		t.Fatal("first reply")
	}
	if err := b.Reply(ctx, old, packet); err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(ctx, 10*time.Millisecond)
	if err := b.Retire(short, old); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("retired before AWG Read boundary", err)
	}
	stop()
	if _, err := b.Attach(downstream, false); !errors.Is(err, ErrConfig) {
		t.Fatal("address reused before fencing", err)
	}
	if err := b.Forget(old); !errors.Is(err, ErrStale) {
		t.Fatal("forgot in-flight ownership", err)
	}
	reading := readAsync(b)
	if err := b.Retire(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := b.Forget(old); err != nil {
		t.Fatal(err)
	}
	fresh, err := b.Attach(downstream, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Reply(ctx, old, packet); !errors.Is(err, ErrStale) {
		t.Fatal("old token reused", err)
	}
	packet[28] = 2
	if err := b.Reply(ctx, fresh, packet); err != nil {
		t.Fatal(err)
	}
	if p := receive(t, reading); p[28] != 2 {
		t.Fatal("old queued reply crossed ownership")
	}
}

func TestRetireWaitsForForwardAndCloseCancels(t *testing.T) {
	entered := make(chan struct{})
	b, _, p, _ := fixture(t, func(ctx context.Context, _ *Peer, _ []byte) error { close(entered); <-ctx.Done(); return ctx.Err() })
	done := make(chan struct{})
	go func() { defer close(done); b.Write([][]byte{udp(downstream, internet, 1000, 53, 1)}, 0) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := b.Retire(ctx, p); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("did not join forwarding", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close returned with forwarding alive")
	}
	if _, err := b.Write([][]byte{udp(downstream, internet, 1000, 53, 1)}, 0); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}

func TestQueueAndPeerLimits(t *testing.T) {
	b, _, p, u := fixture(t, nil)
	ctx := context.Background()
	packet := udp(internet, downstream, 53, 30000, 1)
	for i := 0; i < 128; i++ {
		if err := b.Reply(ctx, p, packet); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := b.Reply(ctx, p, packet); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	if err := b.Reply(ctx, u, udp(internet, own, 53, 30000, 1)); !errors.Is(err, ErrConfig) {
		t.Fatal("NAT reply accepted for upstream", err)
	}
	for i := 3; i <= 8; i++ {
		if _, err := b.Attach(netip.AddrFrom4([4]byte{10, 17, 0, byte(i)}), false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Attach(netip.MustParseAddr("10.17.0.9"), false); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil || len(b.queue) != 0 {
		t.Fatal("queue residue", err)
	}
}
