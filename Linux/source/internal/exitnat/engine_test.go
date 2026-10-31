// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/safetypolicy"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/libp2p/go-libp2p/core/peer"
)

type reservation struct {
	port         uint16
	closed, fail bool
	reply        *ReplyToken
}

func (r *reservation) Port() uint16 { return r.port }
func (r *reservation) Close() error {
	if r.fail {
		return errors.New("native close failed")
	}
	r.closed = true
	return nil
}

type fixture struct {
	engine *Engine
	now    time.Time
	path   Path
	ports  []*reservation
}

func newFixture(t *testing.T, change func(*Config)) *fixture {
	t.Helper()
	f := &fixture{now: time.Unix(1700000000, 0), path: Path{Generation: 1, Interface: 9, Source: netip.MustParseAddr("203.0.113.9")}}
	c := DefaultConfig(f.path, func(_ context.Context, _ Path, _ uint8, token *ReplyToken) (Reservation, error) {
		r := &reservation{port: uint16(40000 + len(f.ports)), reply: token}
		f.ports = append(f.ports, r)
		return r, nil
	})
	c.Now = func() time.Time { return f.now }
	if change != nil {
		change(&c)
	}
	var err error
	f.engine, err = New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, r := range f.ports {
			r.fail = false
		}
		if err := f.engine.Close(); err != nil {
			t.Error(err)
		}
		for _, r := range f.ports {
			if !r.closed {
				t.Error("reservation leak")
			}
		}
	})
	return f
}
func (f *fixture) lease(t *testing.T, id byte) *Lease {
	t.Helper()
	v := tunnellease.Lease{ID: tunnellease.ID{id}, Session: tunnellease.SessionToken{id}, ClientPeer: peer.ID(fmt.Sprintf("client-%d", id)), ExitPeer: peer.ID("exit"), Generation: 1, IPv4: netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 17, 0, id}), 32), ValidUntil: f.now.Add(time.Hour)}
	l, err := f.engine.AddLease(v)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func wire(protocol uint8, src, dst netip.Addr, sport, dport uint16, flags byte, payload []byte) []byte {
	n := 8
	if protocol == TCP {
		n = 20
	}
	b := make([]byte, 20+n+len(payload))
	b[0] = 0x45
	b[8] = 64
	b[9] = protocol
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	b[6] = 0x40
	copy(b[12:16], src.AsSlice())
	copy(b[16:20], dst.AsSlice())
	binary.BigEndian.PutUint16(b[20:22], sport)
	binary.BigEndian.PutUint16(b[22:24], dport)
	if protocol == UDP {
		binary.BigEndian.PutUint16(b[24:26], uint16(n+len(payload)))
	} else {
		binary.BigEndian.PutUint32(b[24:28], 0x11223344)
		binary.BigEndian.PutUint32(b[28:32], 0x55667788)
		b[32] = 0x50
		b[33] = flags
		binary.BigEndian.PutUint16(b[34:36], 32000)
	}
	copy(b[20+n:], payload)
	fixChecksums(b)
	return b
}
func fixChecksums(b []byte) {
	b[10], b[11] = 0, 0
	binary.BigEndian.PutUint16(b[10:12], checksum(b[:20]))
	offset := 26
	if b[9] == TCP {
		offset = 36
	}
	b[offset], b[offset+1] = 0, 0
	c := transportChecksum(b)
	if c == 0 && b[9] == UDP {
		c = 65535
	}
	binary.BigEndian.PutUint16(b[offset:offset+2], c)
}
func capture(target *[]byte) Emit {
	return func(_ context.Context, _ Path, b []byte) error { *target = b; return nil }
}

func tcpSequence(b []byte, sequence, ack uint32) []byte {
	binary.BigEndian.PutUint32(b[24:28], sequence)
	binary.BigEndian.PutUint32(b[28:32], ack)
	fixChecksums(b)
	return b
}
func noEmit(t *testing.T) Emit {
	return func(context.Context, Path, []byte) error { t.Error("denied packet emitted"); return nil }
}

var remote = netip.MustParseAddr("198.51.100.77")

func TestReplyTokenFencesReusedPort(t *testing.T) {
	for _, protocol := range []uint8{UDP, TCP} {
		t.Run(fmt.Sprint(protocol), func(t *testing.T) {
			var reservations []*reservation
			f := newFixture(t, func(c *Config) {
				c.Reserve = func(_ context.Context, _ Path, _ uint8, token *ReplyToken) (Reservation, error) {
					r := &reservation{port: 41000, reply: token}
					reservations = append(reservations, r)
					return r, nil
				}
			})
			t.Cleanup(func() { f.ports = reservations })
			ctx := context.Background()
			l1, l2 := f.lease(t, 2), f.lease(t, 3)
			var out []byte
			request := func(l *Lease) []byte {
				b := wire(protocol, l.value.IPv4.Addr(), remote, 33000, 443, 2, nil)
				if protocol == TCP {
					b = tcpSequence(b, 100, 0)
				}
				return b
			}
			if err := f.engine.Outbound(ctx, l1, request(l1), capture(&out)); err != nil {
				t.Fatal(err)
			}
			old := reservations[0].reply
			reply := wire(protocol, remote, f.path.Source, 443, 41000, 18, nil)
			if protocol == TCP {
				reply = tcpSequence(reply, 200, 101)
			}
			if err := f.engine.Revoke(l1); err != nil {
				t.Fatal(err)
			}
			if err := f.engine.Inbound(ctx, f.path, old, reply, noEmit(t)); !errors.Is(err, ErrStale) {
				t.Fatal("reply survived revoke", err)
			}
			f.now = f.now.Add(3 * time.Minute)
			if err := f.engine.Sweep(); err != nil || !reservations[0].closed {
				t.Fatal("quarantine did not release old port", err)
			}
			if err := f.engine.Outbound(ctx, l2, request(l2), capture(&out)); err != nil {
				t.Fatal(err)
			}
			current := reservations[1].reply
			if old == nil || current == nil || old == current {
				t.Fatal("reservation identity reused")
			}
			for _, token := range []*ReplyToken{nil, {}, old} {
				if err := f.engine.Inbound(ctx, f.path, token, reply, noEmit(t)); !errors.Is(err, ErrStale) {
					t.Fatal("foreign queued reply reached replacement lease", err)
				}
			}
			if err := f.engine.Inbound(ctx, f.path, current, reply, capture(&out)); err != nil {
				t.Fatal(err)
			}
			p, err := parse(out)
			if err != nil || p.destination != l2.value.IPv4.Addr() {
				t.Fatal("current reply lost", err)
			}
		})
	}
}

func TestUDPTranslationDeduplicationAndOwnership(t *testing.T) {
	f := newFixture(t, nil)
	l1, l2 := f.lease(t, 2), f.lease(t, 3)
	ctx := context.Background()
	var a, b, out []byte
	request := wire(UDP, l1.value.IPv4.Addr(), remote, 33333, 443, 0, []byte("payload"))
	original := bytes.Clone(request)
	if err := f.engine.Outbound(ctx, l1, request, capture(&a)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(request, original) {
		t.Fatal("caller packet mutated")
	}
	if err := f.engine.Outbound(ctx, l2, wire(UDP, l2.value.IPv4.Addr(), remote, 33333, 443, 0, []byte("other")), capture(&b)); err != nil {
		t.Fatal(err)
	}
	pa, _ := parse(a)
	pb, _ := parse(b)
	if pa.source != f.path.Source || pa.sourcePort == pb.sourcePort || a[8] != 63 {
		t.Fatal("incorrect translation")
	}
	if err := f.engine.Outbound(ctx, l1, wire(UDP, l1.value.IPv4.Addr(), remote, 33333, 444, 0, nil), capture(&out)); err != nil {
		t.Fatal(err)
	}
	po, _ := parse(out)
	if po.sourcePort != pa.sourcePort || f.engine.Stats().Mappings != 2 {
		t.Fatal("mapping must be endpoint-independent")
	}
	reply := wire(UDP, remote, f.path.Source, 443, pa.sourcePort, 0, []byte("reply"))
	if err := f.engine.Inbound(ctx, f.path, f.ports[0].reply, reply, capture(&out)); err != nil {
		t.Fatal(err)
	}
	got, err := parse(out)
	if err != nil || got.destination != l1.value.IPv4.Addr() || got.destinationPort != 33333 || !bytes.Equal(out[28:], []byte("reply")) {
		t.Fatal("bad reverse translation", err)
	}
	for _, source := range []netip.Addr{netip.MustParseAddr("198.51.100.78"), netip.MustParseAddr("127.0.0.1")} {
		if err := f.engine.Inbound(ctx, f.path, f.ports[0].reply, wire(UDP, source, f.path.Source, 443, pa.sourcePort, 0, nil), noEmit(t)); err == nil {
			t.Fatal("foreign source accepted")
		}
	}
	if err := f.engine.Inbound(ctx, f.path, f.ports[0].reply, wire(UDP, remote, f.path.Source, 445, pa.sourcePort, 0, nil), noEmit(t)); err == nil {
		t.Fatal("uncontacted port accepted")
	}
	if err := f.engine.Outbound(ctx, l1, wire(UDP, l2.value.IPv4.Addr(), remote, 33333, 443, 0, nil), noEmit(t)); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if err := f.engine.Revoke(l1); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Inbound(ctx, f.path, f.ports[0].reply, reply, noEmit(t)); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if err := f.engine.Outbound(ctx, l1, request, noEmit(t)); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if f.ports[0].closed {
		t.Fatal("port not quarantined")
	}
	f.now = f.now.Add(3 * time.Minute)
	if err := f.engine.Sweep(); err != nil {
		t.Fatal(err)
	}
	if !f.ports[0].closed {
		t.Fatal("expired quarantine not released")
	}
}

func TestSafetyAndManagementBeforeAllocation(t *testing.T) {
	f := newFixture(t, func(c *Config) {
		c.Blocked = []netip.Prefix{netip.MustParsePrefix("198.18.0.0/15")}
		c.LocalAddresses = []netip.Addr{netip.MustParseAddr("203.0.113.8")}
	})
	l := f.lease(t, 2)
	for _, s := range []string{"0.1.2.3", "10.2.3.4", "100.64.3.2", "127.0.0.1", "169.254.1.2", "172.16.1.2", "192.168.1.2", "198.18.1.1", "203.0.113.8", "203.0.113.9", "224.0.0.1", "255.255.255.255"} {
		if err := f.engine.Outbound(context.Background(), l, wire(UDP, l.value.IPv4.Addr(), netip.MustParseAddr(s), 20000, 443, 0, nil), noEmit(t)); !errors.Is(err, ErrDenied) {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, rule := range safetypolicy.Rules(safetypolicy.V0) {
		proto := TCP
		if rule.Transport == safetypolicy.TransportUDP {
			proto = UDP
		}
		for p := uint32(rule.From); p <= uint32(rule.To); p++ {
			if err := f.engine.Outbound(context.Background(), l, wire(proto, l.value.IPv4.Addr(), remote, 20000, uint16(p), 2, nil), noEmit(t)); !errors.Is(err, ErrDenied) {
				t.Errorf("blocked %d/%d: %v", proto, p, err)
			}
		}
	}
	if len(f.ports) != 0 {
		t.Fatal("denied traffic allocated resources")
	}
}

func TestTCPTranslationPreservesSequenceAndClosing(t *testing.T) {
	f := newFixture(t, nil)
	l := f.lease(t, 2)
	ctx := context.Background()
	var out []byte
	if err := f.engine.Outbound(ctx, l, wire(TCP, l.value.IPv4.Addr(), remote, 33000, 443, 16, nil), noEmit(t)); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	syn := tcpSequence(wire(TCP, l.value.IPv4.Addr(), remote, 33000, 443, 2, nil), 100, 0)
	if err := f.engine.Outbound(ctx, l, syn, capture(&out)); err != nil {
		t.Fatal(err)
	}
	p, err := parse(out)
	if err != nil || !bytes.Equal(syn[24:36], out[24:36]) {
		t.Fatal("TCP sequence/header changed", err)
	}
	if err := f.engine.Inbound(ctx, f.path, f.ports[0].reply, wire(TCP, remote, f.path.Source, 443, p.sourcePort, 16, nil), noEmit(t)); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if err := f.engine.Inbound(ctx, f.path, f.ports[0].reply, tcpSequence(wire(TCP, remote, f.path.Source, 443, p.sourcePort, 18, nil), 200, 101), capture(&out)); err != nil {
		t.Fatal(err)
	}
	for _, flow := range f.engine.flows {
		if flow.established {
			t.Fatal("SYN/ACK alone marked established")
		}
	}
	data := tcpSequence(wire(TCP, l.value.IPv4.Addr(), remote, 33000, 443, 24, []byte("opaque TCP stream")), 101, 201)
	if err := f.engine.Outbound(ctx, l, data, capture(&out)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data[40:], out[40:]) {
		t.Fatal("payload changed")
	}
	for _, flow := range f.engine.flows {
		if !flow.expires.Equal(f.now.Add(2*time.Hour + 4*time.Minute)) {
			t.Fatal("established lifetime")
		}
	}
	if err := f.engine.Outbound(ctx, l, tcpSequence(wire(TCP, l.value.IPv4.Addr(), remote, 33000, 443, 17, nil), 118, 201), capture(&out)); err != nil {
		t.Fatal(err)
	}
	for _, flow := range f.engine.flows {
		if !flow.expires.Equal(f.now.Add(2*time.Hour + 4*time.Minute)) {
			t.Fatal("one-sided FIN shortened lifetime")
		}
	}
	if err := f.engine.Inbound(ctx, f.path, f.ports[0].reply, tcpSequence(wire(TCP, remote, f.path.Source, 443, p.sourcePort, 4, nil), 999, 119), capture(&out)); err != nil {
		t.Fatal(err)
	}
	if f.engine.Stats().Flows != 1 {
		t.Fatal("out-of-window RST retired mapping")
	}
	if err := f.engine.Inbound(ctx, f.path, f.ports[0].reply, tcpSequence(wire(TCP, remote, f.path.Source, 443, p.sourcePort, 17, nil), 201, 119), capture(&out)); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Outbound(ctx, l, tcpSequence(wire(TCP, l.value.IPv4.Addr(), remote, 33000, 443, 16, nil), 119, 202), capture(&out)); err != nil {
		t.Fatal(err)
	}
	for _, flow := range f.engine.flows {
		if !flow.expires.Equal(f.now.Add(4 * time.Minute)) {
			t.Fatal("closing lifetime")
		}
	}
	if err := f.engine.Inbound(ctx, f.path, f.ports[0].reply, tcpSequence(wire(TCP, remote, f.path.Source, 443, p.sourcePort, 4, nil), 202, 119), capture(&out)); err != nil {
		t.Fatal(err)
	}
	if f.engine.Stats().Flows != 0 || f.engine.Stats().Reserved != 1 {
		t.Fatal("RST ownership cleanup")
	}
}

func TestReservationPartialFailureAndRetry(t *testing.T) {
	bad := &reservation{port: 40000, fail: true}
	f := newFixture(t, func(c *Config) {
		c.MaxFlows = 1
		c.MaxPerLease = 1
		c.Reserve = func(context.Context, Path, uint8, *ReplyToken) (Reservation, error) {
			return bad, errors.New("partial native allocation")
		}
	})
	f.ports = append(f.ports, bad)
	l := f.lease(t, 2)
	request := wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, nil)
	if err := f.engine.Outbound(context.Background(), l, request, noEmit(t)); err == nil {
		t.Fatal("failed reserve accepted")
	}
	if f.engine.Stats().Reserved != 1 {
		t.Fatal("partial resource lost")
	}
	if err := f.engine.Outbound(context.Background(), l, request, noEmit(t)); !errors.Is(err, ErrCapacity) {
		t.Fatal("failed resources escaped capacity limit", err)
	}
	bad.fail = false
	if err := f.engine.Sweep(); err != nil {
		t.Fatal(err)
	}
	if !bad.closed || f.engine.Stats().Reserved != 0 {
		t.Fatal("cleanup retry failed")
	}
}

func TestLeaseRenewalPathGenerationAndCapacity(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.MaxFlows = 2; c.MaxPerLease = 2 })
	l := f.lease(t, 2)
	ctx := context.Background()
	var out []byte
	request := wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, nil)
	if err := f.engine.Outbound(ctx, l, request, capture(&out)); err != nil {
		t.Fatal(err)
	}
	p, _ := parse(out)
	reply := wire(UDP, remote, f.path.Source, 443, p.sourcePort, 0, nil)
	path := f.path
	path.Interface++
	if err := f.engine.Inbound(ctx, path, f.ports[0].reply, reply, noEmit(t)); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	path.Generation++
	if err := f.engine.Reconcile(path, []netip.Addr{remote}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Inbound(ctx, f.path, f.ports[0].reply, reply, noEmit(t)); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if err := f.engine.Outbound(ctx, l, request, noEmit(t)); !errors.Is(err, ErrDenied) {
		t.Fatal("new host address not denied", err)
	}
	v := l.value
	v.Generation++
	v.ValidUntil = v.ValidUntil.Add(time.Hour)
	if err := f.engine.Renew(l, v); err != nil {
		t.Fatal(err)
	}
	v.Session[0]++
	if err := f.engine.Renew(l, v); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	f.now = f.now.Add(3 * time.Hour)
	if err := f.engine.Outbound(ctx, l, request, noEmit(t)); !errors.Is(err, ErrStale) {
		t.Fatal("expired lease accepted")
	}
	if err := f.engine.Sweep(); err != nil {
		t.Fatal(err)
	}
	if f.engine.Stats() != (Stats{}) {
		t.Fatal(f.engine.Stats())
	}
}

func TestCapacityAndFailedCloseRemainBounded(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.MaxFlows = 2; c.MaxPerLease = 2 })
	l := f.lease(t, 2)
	ctx := context.Background()
	var out []byte
	for i := 0; i < 2; i++ {
		if err := f.engine.Outbound(ctx, l, wire(UDP, l.value.IPv4.Addr(), remote, uint16(30000+i), 443, 0, nil), capture(&out)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.engine.Outbound(ctx, l, wire(UDP, l.value.IPv4.Addr(), remote, 30002, 443, 0, nil), noEmit(t)); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	f.ports[0].fail = true
	if err := f.engine.Close(); err == nil {
		t.Fatal("lost failed Close")
	}
	if f.engine.Stats().Reserved != 1 {
		t.Fatal("failed reservation lost")
	}
	f.ports[0].fail = false
	if err := f.engine.Close(); err != nil {
		t.Fatal(err)
	}
	if f.engine.Stats() != (Stats{}) {
		t.Fatal(f.engine.Stats())
	}
}

func TestRevokeWaitsForAdmittedSend(t *testing.T) {
	f := newFixture(t, nil)
	l := f.lease(t, 2)
	started, finish := make(chan struct{}), make(chan struct{})
	sent, revoked := make(chan error, 1), make(chan error, 1)
	request := wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, nil)
	go func() {
		sent <- f.engine.Outbound(context.Background(), l, request, func(context.Context, Path, []byte) error { close(started); <-finish; return nil })
	}()
	<-started
	go func() { revoked <- f.engine.Revoke(l) }()
	select {
	case err := <-revoked:
		t.Fatal("revoke returned during send", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(finish)
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Outbound(context.Background(), l, request, noEmit(t)); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
}

func TestConcurrentLifecycleAndPacketCalls(t *testing.T) {
	f := newFixture(t, nil)
	l := f.lease(t, 2)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = f.engine.Outbound(context.Background(), l, wire(UDP, l.value.IPv4.Addr(), remote, 30000, 443, 0, nil), func(context.Context, Path, []byte) error { return nil })
			}
		}()
	}
	if err := f.engine.Revoke(l); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if f.engine.Stats().Flows != 0 {
		t.Fatal("flow survived revoke")
	}
}

func TestChecksumGoldenAndMalformedPackets(t *testing.T) {
	b, _ := hex.DecodeString("0001f203f4f5f6f7")
	if checksum(b) != 0x220d {
		t.Fatal("RFC1071 checksum arithmetic")
	}
	source := netip.MustParseAddr("10.17.0.2")
	valid := wire(UDP, source, remote, 12345, 443, 0, []byte("odd"))
	if _, err := parse(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func([]byte){func(b []byte) { b[0] = 0x65 }, func(b []byte) { b[0] = 0x46 }, func(b []byte) { b[2] = 0xff }, func(b []byte) { b[6] = 0x20; fixChecksums(b) }, func(b []byte) { b[8] = 1; fixChecksums(b) }, func(b []byte) { b[25] = 2; fixChecksums(b) }, func(b []byte) { b[len(b)-1] ^= 1 }} {
		copy := bytes.Clone(valid)
		mutate(copy)
		if _, err := parse(copy); err == nil {
			t.Fatal("malformed/unsupported accepted")
		}
	}
	zero := bytes.Clone(valid)
	zero[26], zero[27] = 0, 0
	if _, err := parse(zero); err != nil {
		t.Fatal("valid IPv4 UDP zero checksum rejected", err)
	}
}

func FuzzPacketParser(f *testing.F) {
	f.Add(wire(UDP, netip.MustParseAddr("10.17.0.2"), remote, 12345, 443, 0, []byte("seed")))
	f.Add(wire(TCP, netip.MustParseAddr("10.17.0.2"), remote, 12345, 443, 2, nil))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := parse(b)
		if err != nil {
			return
		}
		out := rewrite(p, netip.MustParseAddr("203.0.113.9"), 40000, true)
		if len(out) != len(b) || checksum(out[:20]) != 0 || transportChecksum(out) != 0 {
			t.Fatal("translation broke checksums")
		}
		if b[8] > 2 {
			if _, err := parse(out); err != nil {
				t.Fatal(err)
			}
		}
	})
}
