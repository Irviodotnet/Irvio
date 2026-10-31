// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/libp2p/go-libp2p/core/peer"
)

type fakePacketPort struct {
	port     uint16
	packets  chan []byte
	fault    chan error
	closed   chan struct{}
	once     sync.Once
	mu       sync.Mutex
	closeErr error
}

func (p *fakePacketPort) Port() uint16 { return p.port }
func (p *fakePacketPort) ReceiveIPv4(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.closed:
		return nil, ErrStale
	case err := <-p.fault:
		return nil, err
	case packet := <-p.packets:
		return packet, nil
	}
}
func (p *fakePacketPort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closeErr != nil {
		return p.closeErr
	}
	p.once.Do(func() { close(p.closed) })
	return nil
}

type fakePacketSender struct {
	mu       sync.Mutex
	packets  [][]byte
	closed   bool
	closeErr error
}

func (s *fakePacketSender) SendIPv4(ctx context.Context, p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStale
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.packets = append(s.packets, p)
	return nil
}
func (s *fakePacketSender) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closeErr != nil {
		return s.closeErr
	}
	s.closed = true
	return nil
}

type backendFixture struct {
	b      *Backend
	path   Path
	sender *fakePacketSender
	mu     sync.Mutex
	ports  []*fakePacketPort
}

func newBackendFixture(t *testing.T, deliver Emit) *backendFixture {
	t.Helper()
	f := &backendFixture{path: Path{1, 9, netip.MustParseAddr("203.0.113.9")}, sender: &fakePacketSender{}}
	var err error
	f.b, err = NewBackend(DefaultConfig(f.path, nil), f.sender, func(context.Context, Path, uint8) (PacketPort, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p := &fakePacketPort{port: uint16(41000 + len(f.ports)), packets: make(chan []byte, 512), fault: make(chan error, 1), closed: make(chan struct{})}
		f.ports = append(f.ports, p)
		return p, nil
	}, deliver)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.b.Close(); err != nil {
			t.Error(err)
		}
		if f.b.Stats() != (Stats{}) {
			t.Error("resources retained", f.b.Stats())
		}
		for _, p := range f.ports {
			select {
			case <-p.closed:
			default:
				t.Error("port leaked")
			}
		}
	})
	return f
}

func (f *backendFixture) lease(t *testing.T, id byte) *Lease {
	t.Helper()
	l, err := f.b.AddLease(tunnellease.Lease{ID: tunnellease.ID{id}, Session: tunnellease.SessionToken{id}, ClientPeer: peer.ID(fmt.Sprint("client-", id)), ExitPeer: peer.ID("exit"), Generation: 1, IPv4: netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 17, 0, id}), 32), ValidUntil: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-deadline.C:
			t.Fatal("condition not reached")
		case <-tick.C:
		}
	}
}

func TestBackendFiveLeasesRoundTripAndPolicyBeforeDriver(t *testing.T) {
	delivered := make(chan []byte, 10)
	f := newBackendFixture(t, func(_ context.Context, _ Path, p []byte) error { delivered <- p; return nil })
	for id := byte(2); id < 7; id++ {
		l := f.lease(t, id)
		for _, packet := range [][]byte{
			wire(UDP, netip.MustParseAddr("10.17.0.99"), remote, 30000, 443, 0, nil),
			wire(UDP, l.value.IPv4.Addr(), netip.MustParseAddr("192.168.1.1"), 30000, 443, 0, nil),
			wire(TCP, l.value.IPv4.Addr(), remote, 30000, 25, 2, nil),
		} {
			if err := f.b.Outbound(context.Background(), l, packet); !errors.Is(err, ErrDenied) {
				t.Fatal(err)
			}
		}
		if len(f.ports) != int(id-2) {
			t.Fatal("denied traffic allocated native port")
		}
		if err := f.b.Outbound(context.Background(), l, wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, []byte{id})); err != nil {
			t.Fatal(err)
		}
		p := f.ports[len(f.ports)-1]
		p.packets <- wire(UDP, remote, f.path.Source, 443, p.port, 0, []byte{id})
		select {
		case got := <-delivered:
			parsed, err := parse(got)
			if err != nil || parsed.destination != l.value.IPv4.Addr() || parsed.destinationPort != 30000 || got[28] != id {
				t.Fatal("wrong reverse translation", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("reply lost")
		}
		if err := f.b.Revoke(l); err != nil {
			t.Fatal(err)
		}
		before := f.b.DroppedReplies()
		p.packets <- wire(UDP, remote, f.path.Source, 443, p.port, 0, nil)
		eventually(t, func() bool { return f.b.DroppedReplies() > before })
	}
	if len(f.sender.packets) != 5 || f.b.Stats().Reserved != 5 || f.b.Stats().Flows != 0 {
		t.Fatal("incorrect ownership", f.b.Stats())
	}
	select {
	case <-delivered:
		t.Fatal("revoked reply delivered")
	default:
	}
}

func TestBackendSaturationAndCloseDoNotDeadlock(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	f := newBackendFixture(t, func(ctx context.Context, _ Path, _ []byte) error {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	})
	l := f.lease(t, 2)
	if err := f.b.Outbound(context.Background(), l, wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, nil)); err != nil {
		t.Fatal(err)
	}
	p := f.ports[0]
	p.packets <- wire(UDP, remote, f.path.Source, 443, p.port, 0, nil)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("no delivery")
	}
	for i := 0; i < 300; i++ {
		p.packets <- wire(UDP, remote, f.path.Source, 443, p.port, 0, nil)
	}
	eventually(t, func() bool { return f.b.DroppedReplies() > 0 })
	done := make(chan error, 1)
	go func() { done <- f.b.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close blocked on reader/engine lock")
	}
	if len(f.b.queue) != 0 {
		t.Fatal("queue retained after successful close")
	}
	if err := f.b.Outbound(context.Background(), l, wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, nil)); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
}

func TestBackendCaptureFailureStopsForwarding(t *testing.T) {
	f := newBackendFixture(t, noEmit(t))
	l := f.lease(t, 2)
	packet := wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, nil)
	if err := f.b.Outbound(context.Background(), l, packet); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("capture driver failed")
	f.ports[0].fault <- failure
	eventually(t, func() bool { return f.b.Err() != nil })
	if err := f.b.Outbound(context.Background(), l, packet); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(f.sender.packets) != 1 {
		t.Fatal("forwarded without reply capture")
	}
}

func TestBackendCloseFailureRemainsOwned(t *testing.T) {
	f := newBackendFixture(t, noEmit(t))
	l := f.lease(t, 2)
	if err := f.b.Outbound(context.Background(), l, wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, nil)); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("close failed")
	p := f.ports[0]
	p.mu.Lock()
	p.closeErr = failure
	p.mu.Unlock()
	f.sender.closeErr = failure
	if err := f.b.Close(); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if f.b.Stats().Reserved != 1 || f.sender.closed {
		t.Fatal("failed cleanup lost ownership")
	}
	p.mu.Lock()
	p.closeErr = nil
	p.mu.Unlock()
	f.sender.closeErr = nil
	if err := f.b.Close(); err != nil {
		t.Fatal(err)
	}
	if f.b.Stats() != (Stats{}) || !f.sender.closed {
		t.Fatal("retry failed")
	}
}

func TestBackendPartialOpenFailureRetainsReservation(t *testing.T) {
	failure := errors.New("capture allocation failed")
	p := &fakePacketPort{port: 41000, closed: make(chan struct{}), closeErr: failure}
	sender := &fakePacketSender{}
	path := Path{1, 9, netip.MustParseAddr("203.0.113.9")}
	c := DefaultConfig(path, nil)
	c.MaxFlows, c.MaxPerLease = 1, 1
	b, err := NewBackend(c, sender, func(context.Context, Path, uint8) (PacketPort, error) { return p, failure }, noEmit(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.mu.Lock()
		p.closeErr = nil
		p.mu.Unlock()
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	f := &backendFixture{b: b}
	l := f.lease(t, 2)
	packet := wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, nil)
	if err := b.Outbound(context.Background(), l, packet); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if b.Stats().Reserved != 1 || len(sender.packets) != 0 {
		t.Fatal("partial capture resource escaped accounting")
	}
	if err := b.Outbound(context.Background(), l, packet); !errors.Is(err, ErrCapacity) {
		t.Fatal("partial resource did not consume capacity", err)
	}
	if err := b.Close(); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.closeErr = nil
	p.mu.Unlock()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if b.Stats() != (Stats{}) {
		t.Fatal(b.Stats())
	}
}

func TestBackendInvalidConfigLeavesSenderWithCaller(t *testing.T) {
	sender := &fakePacketSender{}
	if b, err := NewBackend(DefaultConfig(Path{}, nil), sender, func(context.Context, Path, uint8) (PacketPort, error) {
		t.Fatal("invalid configuration opened a port")
		return nil, nil
	}, noEmit(t)); b != nil || !errors.Is(err, ErrConfig) {
		t.Fatal(b, err)
	}
	if sender.closed {
		t.Fatal("failed constructor took caller ownership")
	}
}

func TestBackendReplyDeliveryCarriesBoundedContext(t *testing.T) {
	deadline := make(chan time.Time, 1)
	f := newBackendFixture(t, func(ctx context.Context, _ Path, _ []byte) error {
		value, ok := ctx.Deadline()
		if !ok {
			return errors.New("unbounded reply delivery")
		}
		deadline <- value
		return nil
	})
	l := f.lease(t, 2)
	if err := f.b.Outbound(context.Background(), l, wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, nil)); err != nil {
		t.Fatal(err)
	}
	p := f.ports[0]
	p.packets <- wire(UDP, remote, f.path.Source, 443, p.port, 0, nil)
	select {
	case d := <-deadline:
		if remaining := time.Until(d); remaining <= 0 || remaining > replyDeliveryBudget {
			t.Fatal("invalid reply delivery budget", remaining)
		}
	case <-time.After(time.Second):
		t.Fatal("reply emitter had no bounded context")
	}
}
