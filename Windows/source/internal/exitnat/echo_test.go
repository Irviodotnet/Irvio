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
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func echoWire(source, target netip.Addr, id, sequence uint16, data []byte) []byte {
	b := make([]byte, 28+len(data))
	b[0], b[8], b[9], b[20] = 0x45, 64, ICMP, 8
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	binary.BigEndian.PutUint16(b[6:8], 0x4000)
	copy(b[12:16], source.AsSlice())
	copy(b[16:20], target.AsSlice())
	binary.BigEndian.PutUint16(b[24:26], id)
	binary.BigEndian.PutUint16(b[26:28], sequence)
	copy(b[28:], data)
	echoChecksums(b)
	return b
}

func echoReply(wire []byte) []byte {
	b := append([]byte(nil), wire...)
	copy(b[12:16], wire[16:20])
	copy(b[16:20], wire[12:16])
	b[20] = 0
	b[8] = 62
	echoChecksums(b)
	return b
}

type echoFixture struct {
	*fixture
	handles []*reservation
	specs   []EchoCapture
}

func newEchoFixture(t *testing.T, change func(*Config)) *echoFixture {
	t.Helper()
	f := &echoFixture{}
	f.fixture = newFixture(t, func(c *Config) {
		c.ReserveEcho = func(_ context.Context, _ Path, s EchoCapture, key *ReplyToken) (io.Closer, error) {
			r := &reservation{reply: key}
			f.handles = append(f.handles, r)
			f.specs = append(f.specs, s)
			return r, nil
		}
		if change != nil {
			change(c)
		}
	})
	t.Cleanup(func() {
		for _, r := range f.handles {
			r.fail = false
		}
		if err := f.engine.Close(); err != nil {
			t.Error(err)
		}
		for _, r := range f.handles {
			if !r.closed {
				t.Error("echo capture leaked")
			}
		}
	})
	return f
}

func TestEchoRoundTripPayloadSizeAndAtMostOnce(t *testing.T) {
	for _, size := range []int{0, 1, 15, 16, 17, 511, 1472, 65507} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newEchoFixture(t, nil)
			l := f.lease(t, 2)
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i*17 + 9)
			}
			request := echoWire(l.value.IPv4.Addr(), remote, 0, 0, data)
			original := append([]byte(nil), request...)
			var sent []byte
			if err := f.engine.Outbound(context.Background(), l, request, func(_ context.Context, _ Path, b []byte) error { sent = b; return nil }); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, request) || len(sent) != max(44, len(request)) || sent[8] != 63 || checksum(sent[:20]) != 0 || checksum(sent[20:]) != 0 {
				t.Fatal("bad translation or input mutation")
			}
			if f.specs[0].Nonce == ([16]byte{}) || !bytes.Equal(sent[28:44], f.specs[0].Nonce[:]) {
				t.Fatal("missing nonce")
			}
			var delivered []byte
			deliver := func(_ context.Context, _ Path, b []byte) error { delivered = b; return nil }
			if err := f.engine.Inbound(context.Background(), f.path, f.handles[0].reply, echoReply(sent), deliver); err != nil {
				t.Fatal(err)
			}
			parsed, err := parse(delivered)
			if err != nil || parsed.destination != l.value.IPv4.Addr() || parsed.source != remote || parsed.sourcePort != 0 || parsed.destinationPort != 0 || delivered[8] != 61 || !bytes.Equal(delivered[28:], data) {
				t.Fatal("echo restoration", err)
			}
			if f.engine.Stats() != (Stats{Leases: 1}) || !f.handles[0].closed {
				t.Fatal("completed echo retained resources", f.engine.Stats())
			}
			if err := f.engine.Inbound(context.Background(), f.path, f.handles[0].reply, echoReply(sent), deliver); !errors.Is(err, ErrStale) {
				t.Fatal("duplicate delivery", err)
			}
		})
	}
}

func TestEchoFencesAndLeaseLifecycle(t *testing.T) {
	for _, variant := range []string{"nonce", "peer", "id", "sequence", "length", "source", "token", "path", "checksum", "revoked", "expired", "lease-expired", "generation", "closed"} {
		t.Run(variant, func(t *testing.T) {
			f := newEchoFixture(t, nil)
			l := f.lease(t, 2)
			var sent []byte
			if err := f.engine.Outbound(context.Background(), l, echoWire(l.value.IPv4.Addr(), remote, 7, 9, make([]byte, 20)), func(_ context.Context, _ Path, b []byte) error { sent = b; return nil }); err != nil {
				t.Fatal(err)
			}
			b := echoReply(sent)
			token, path := f.handles[0].reply, f.path
			switch variant {
			case "nonce":
				b[28] ^= 1
			case "peer":
				b[15] ^= 1
			case "id":
				b[25] ^= 1
			case "sequence":
				b[27] ^= 1
			case "length":
				b = append(b, 1)
				binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
			case "source":
				b[19] ^= 1
			case "token":
				token = &ReplyToken{}
			case "path":
				path.Generation++
			case "revoked":
				if err := f.engine.Revoke(l); err != nil {
					t.Fatal(err)
				}
			case "expired":
				f.now = f.now.Add(echoLifetime)
			case "lease-expired":
				f.now = l.value.ValidUntil
			case "generation":
				next := f.path
				next.Generation++
				if err := f.engine.Reconcile(next, nil, nil); err != nil {
					t.Fatal(err)
				}
			case "closed":
				if err := f.engine.Close(); err != nil {
					t.Fatal(err)
				}
			}
			echoChecksums(b)
			if variant == "checksum" {
				b[23] ^= 1
			}
			if err := f.engine.Inbound(context.Background(), path, token, b, func(context.Context, Path, []byte) error { t.Error("invalid echo delivered"); return nil }); err == nil {
				t.Fatal("invalid echo accepted")
			}
		})
	}
}

func TestEchoAdmissionCapacityAndCleanup(t *testing.T) {
	f := newEchoFixture(t, func(c *Config) { c.MaxFlows = 2; c.MaxPerLease = 2 })
	l := f.lease(t, 2)
	send := func(context.Context, Path, []byte) error { return nil }
	for _, packet := range [][]byte{echoWire(l.value.IPv4.Addr(), netip.MustParseAddr("192.168.1.1"), 1, 1, nil), echoWire(netip.MustParseAddr("10.17.0.77"), remote, 1, 1, nil), echoReply(echoWire(remote, l.value.IPv4.Addr(), 1, 1, nil))} {
		if err := f.engine.Outbound(context.Background(), l, packet, send); !errors.Is(err, ErrDenied) {
			t.Fatal("admission", err)
		}
	}
	if len(f.handles) != 0 {
		t.Fatal("unauthorized allocation")
	}
	for i := 0; i < 2; i++ {
		if err := f.engine.Outbound(context.Background(), l, echoWire(l.value.IPv4.Addr(), remote, 1, 1, nil), send); err != nil {
			t.Fatal(err)
		}
	}
	if f.specs[0].Nonce == f.specs[1].Nonce {
		t.Fatal("reused nonce")
	}
	if err := f.engine.Outbound(context.Background(), l, echoWire(l.value.IPv4.Addr(), remote, 1, 1, nil), send); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if err := f.engine.Outbound(context.Background(), l, wire(UDP, l.value.IPv4.Addr(), remote, 12345, 443, 0, nil), send); !errors.Is(err, ErrCapacity) {
		t.Fatal("shared capacity", err)
	}
	f.handles[0].fail = true
	f.now = f.now.Add(echoLifetime)
	if err := f.engine.Sweep(); err == nil {
		t.Fatal("lost close failure")
	}
	if s := f.engine.Stats(); s.Flows != 0 || s.Reserved != 1 {
		t.Fatal("lost failed ownership", s)
	}
	f.handles[0].fail = false
	if err := f.engine.Sweep(); err != nil {
		t.Fatal(err)
	}
	if f.engine.Stats() != (Stats{Leases: 1}) {
		t.Fatal("cleanup residue")
	}
}

func TestEchoRevokeWaitsForAdmittedSend(t *testing.T) {
	f := newEchoFixture(t, nil)
	l := f.lease(t, 2)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	sent := make(chan error, 1)
	go func() {
		sent <- f.engine.Outbound(context.Background(), l, echoWire(l.value.IPv4.Addr(), remote, 1, 1, nil), func(context.Context, Path, []byte) error { close(entered); <-release; return nil })
	}()
	<-entered
	revoked := make(chan error, 1)
	go func() { revoked <- f.engine.Revoke(l) }()
	select {
	case <-revoked:
		t.Fatal("revoke returned during send")
	case <-time.After(20 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	if !f.handles[0].closed || f.engine.Stats() != (Stats{}) {
		t.Fatal("revoke did not fence echo")
	}
}

func TestEchoPartialAllocationAndSendFailure(t *testing.T) {
	for _, mode := range []string{"open", "send"} {
		t.Run(mode, func(t *testing.T) {
			r := &reservation{fail: true}
			f := newEchoFixture(t, func(c *Config) {
				c.ReserveEcho = func(context.Context, Path, EchoCapture, *ReplyToken) (io.Closer, error) {
					if mode == "open" {
						return r, ErrConfig
					}
					return r, nil
				}
			})
			defer func() { r.fail = false }()
			l := f.lease(t, 2)
			if err := f.engine.Outbound(context.Background(), l, echoWire(l.value.IPv4.Addr(), remote, 1, 1, nil), func(context.Context, Path, []byte) error { return io.ErrClosedPipe }); err == nil {
				t.Fatal("failure hidden")
			}
			if s := f.engine.Stats(); s.Flows != 0 || s.Reserved != 1 {
				t.Fatal("partial ownership lost", s)
			}
			r.fail = false
			if err := f.engine.Close(); err != nil || !r.closed {
				t.Fatal("cleanup retry failed", err)
			}
		})
	}
}

func TestEchoAdmissionRecheckedAfterCapture(t *testing.T) {
	for _, mode := range []string{"cancel", "lease-expired", "request-expired"} {
		t.Run(mode, func(t *testing.T) {
			f := newEchoFixture(t, nil)
			l := f.lease(t, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reserve := f.engine.c.ReserveEcho
			f.engine.c.ReserveEcho = func(ctx context.Context, path Path, spec EchoCapture, token *ReplyToken) (io.Closer, error) {
				r, err := reserve(ctx, path, spec, token)
				switch mode {
				case "cancel":
					cancel()
				case "lease-expired":
					f.now = l.value.ValidUntil
				case "request-expired":
					f.now = f.now.Add(echoLifetime)
				}
				return r, err
			}
			want := ErrStale
			if mode == "cancel" {
				want = context.Canceled
			}
			err := f.engine.Outbound(ctx, l, echoWire(l.value.IPv4.Addr(), remote, 1, 1, nil), func(context.Context, Path, []byte) error { t.Error("stale request emitted"); return nil })
			if !errors.Is(err, want) || len(f.handles) != 1 || !f.handles[0].closed || f.engine.Stats() != (Stats{Leases: 1}) {
				t.Fatal("post-allocation fence failed", err, f.engine.Stats())
			}
		})
	}
}

func TestEchoHardLimitsAndLeaseRenewal(t *testing.T) {
	f := newEchoFixture(t, nil)
	send := func(context.Context, Path, []byte) error { return nil }
	for i := byte(2); i < 10; i++ {
		l := f.lease(t, i)
		for n := 0; n < maxEchoPerLease; n++ {
			if err := f.engine.Outbound(context.Background(), l, echoWire(l.value.IPv4.Addr(), remote, 1, 1, nil), send); err != nil {
				t.Fatal(err)
			}
		}
		renewed := l.value
		renewed.Generation++
		if err := f.engine.Renew(l, renewed); err != nil {
			t.Fatal(err)
		}
		if err := f.engine.Outbound(context.Background(), l, echoWire(l.value.IPv4.Addr(), remote, 1, 1, nil), send); !errors.Is(err, ErrCapacity) {
			t.Fatal("per-lease echo bound bypassed after renewal", err)
		}
	}
	l := f.lease(t, 10)
	if err := f.engine.Outbound(context.Background(), l, echoWire(l.value.IPv4.Addr(), remote, 1, 1, nil), send); !errors.Is(err, ErrCapacity) {
		t.Fatal("global echo bound bypassed", err)
	}
	if len(f.handles) != maxEchoRequests {
		t.Fatal("allocated beyond limit", len(f.handles))
	}
	f.now = f.now.Add(echoLifetime)
	if err := f.engine.Sweep(); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Outbound(context.Background(), l, echoWire(l.value.IPv4.Addr(), remote, 1, 1, nil), send); err != nil {
		t.Fatal("expired captures did not release capacity", err)
	}
}
