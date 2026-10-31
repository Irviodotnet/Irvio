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
	"net/netip"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/icmpv4"
)

func errorWire(quote []byte, kind, code byte) []byte {
	b := make([]byte, 28+len(quote))
	b[0], b[8], b[9], b[20], b[21] = 0x45, 64, 1, kind, code
	copy(b[12:16], netip.MustParseAddr("192.168.1.1").AsSlice())
	copy(b[16:20], quote[12:16])
	copy(b[28:], quote)
	if kind == 3 && code == 4 {
		binary.BigEndian.PutUint16(b[26:28], 1280)
	}
	fixError(b)
	return b
}

func fixError(b []byte) {
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	b[10], b[11], b[22], b[23] = 0, 0, 0, 0
	binary.BigEndian.PutUint16(b[10:12], checksum(b[:20]))
	binary.BigEndian.PutUint16(b[22:24], checksum(b[20:]))
}

func TestICMPErrorReverseTranslation(t *testing.T) {
	for _, protocol := range []uint8{UDP, TCP} {
		for _, size := range []int{28, 36, 38, 40, 45} {
			for _, kind := range [][2]byte{{3, 3}, {3, 4}, {11, 0}, {12, 0}} {
				t.Run(fmt.Sprint(protocol, "-", size, "-", kind), func(t *testing.T) {
					f := newFixture(t, nil)
					l := f.lease(t, 2)
					request := wire(protocol, l.value.IPv4.Addr(), remote, 31000, 443, 2, bytes.Repeat([]byte{0xab}, 17))
					var out, reply []byte
					if err := f.engine.Outbound(context.Background(), l, request, capture(&out)); err != nil {
						t.Fatal(err)
					}
					input := errorWire(out[:size], kind[0], kind[1])
					original := bytes.Clone(input)
					before := f.engine.Stats()
					f.now = f.now.Add(time.Second)
					if err := f.engine.Inbound(context.Background(), f.path, f.ports[0].reply, input, capture(&reply)); err != nil {
						t.Fatal(err)
					}
					p, err := icmpv4.ParseError(reply)
					if err != nil || p.OriginalSource != l.value.IPv4.Addr() || p.Recipient != l.value.IPv4.Addr() ||
						p.SourcePort != 31000 || p.DestinationPort != 443 || p.OriginalDestination != remote || p.Protocol != protocol {
						t.Fatal("wrong translated ownership", p, err)
					}
					if !bytes.Equal(input, original) || !bytes.Equal(reply[24:28], input[24:28]) ||
						!bytes.Equal(reply[12:16], input[12:16]) || reply[8] != input[8]-1 || before != f.engine.Stats() {
						t.Fatal("error changed input, router, MTU, lifetime state or flow count")
					}
					want := bytes.Clone(request[:size])
					want[8]--
					want[10], want[11] = 0, 0
					binary.BigEndian.PutUint16(want[10:12], checksum(want[:20]))
					if !bytes.Equal(reply[28:], want) {
						t.Fatalf("quoted packet/checksums mismatch\ngot %x\nwant%x", reply[28:], want)
					}
					for _, flow := range f.engine.flows {
						idle := f.engine.c.UDPIdle
						if protocol == TCP {
							idle = f.engine.c.TCPOpeningIdle
						}
						if !flow.expires.Equal(f.now.Add(idle - time.Second)) {
							t.Fatal("ICMP extended flow lifetime")
						}
					}
				})
			}
		}
	}
}

func TestICMPErrorOwnershipFences(t *testing.T) {
	for _, change := range []string{"token", "generation", "interface", "recipient", "quoted-source", "port", "remote", "remote-port", "expired-flow", "expired-lease", "revoked", "closed", "canceled"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t, nil)
			l := f.lease(t, 2)
			var out []byte
			ctx := context.Background()
			if err := f.engine.Outbound(ctx, l, wire(UDP, l.value.IPv4.Addr(), remote, 31000, 443, 0, nil), capture(&out)); err != nil {
				t.Fatal(err)
			}
			path, token := f.path, f.ports[0].reply
			reply := errorWire(out, 3, 4)
			switch change {
			case "token":
				token = &ReplyToken{}
			case "generation":
				path.Generation++
			case "interface":
				path.Interface++
			case "recipient":
				reply[19]++
			case "quoted-source":
				reply[43]++
			case "port":
				reply[49]++
			case "remote":
				reply[47]++
			case "remote-port":
				reply[51]++
			case "expired-flow":
				f.now = f.now.Add(f.engine.c.UDPIdle)
			case "expired-lease":
				f.now = f.now.Add(time.Hour)
			case "revoked":
				if err := f.engine.Revoke(l); err != nil {
					t.Fatal(err)
				}
			case "closed":
				if err := f.engine.Close(); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			reply[38], reply[39] = 0, 0
			binary.BigEndian.PutUint16(reply[38:40], checksum(reply[28:48]))
			fixError(reply)
			if err := f.engine.Inbound(ctx, path, token, reply, noEmit(t)); err == nil {
				t.Fatal("unowned ICMP error accepted")
			}
		})
	}
}

func TestICMPErrorInvalidPackets(t *testing.T) {
	q := wire(UDP, netip.MustParseAddr("203.0.113.9"), remote, 40000, 443, 0, nil)
	valid := errorWire(q, 3, 4)
	for n := 0; n < len(valid); n++ {
		if _, err := icmpv4.ParseError(valid[:n]); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	for _, change := range []string{"outer-checksum", "icmp-checksum", "quote-checksum", "fragment", "quote-fragment", "options", "quote-options", "ttl", "redirect", "echo", "code", "protocol", "udp-length", "quote-length", "extension-length", "zero-port"} {
		t.Run(change, func(t *testing.T) {
			b := bytes.Clone(valid)
			switch change {
			case "outer-checksum":
				b[10]++
			case "icmp-checksum":
				b[22]++
			case "quote-checksum":
				b[38]++
			case "fragment":
				b[6] = 0x20
			case "quote-fragment":
				b[34] = 0x20
			case "options":
				b[0] = 0x46
			case "quote-options":
				b[28] = 0x46
			case "ttl":
				b[8] = 1
			case "redirect":
				b[20] = 5
			case "echo":
				b[20] = 0
			case "code":
				b[21] = 16
			case "protocol":
				b[37] = 1
			case "udp-length":
				b[53]++
			case "quote-length":
				b[31] = 27
			case "extension-length":
				b[25] = 32
			case "zero-port":
				b[48], b[49] = 0, 0
			}
			if change != "quote-checksum" {
				b[38], b[39] = 0, 0
				binary.BigEndian.PutUint16(b[38:40], checksum(b[28:48]))
			}
			if change != "outer-checksum" && change != "icmp-checksum" {
				fixError(b)
			}
			if _, err := icmpv4.ParseError(b); !errors.Is(err, icmpv4.ErrPacket) {
				t.Fatal("malformed error accepted", err)
			}
		})
	}
}

func TestICMPErrorExtensionAndDisabledUDPChecksum(t *testing.T) {
	q := wire(UDP, netip.MustParseAddr("203.0.113.9"), remote, 40000, 443, 0, nil)
	q[26], q[27] = 0, 0
	quote := append(bytes.Clone(q), make([]byte, 128-len(q))...)
	quote = append(quote, []byte{0x20, 0, 0, 0, 0, 4, 1, 1}...)
	b := errorWire(quote, 3, 4)
	b[25] = 32
	fixError(b)
	p, err := icmpv4.ParseError(b)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Translate(netip.MustParseAddr("10.17.0.2"), 31000)
	if err != nil || !bytes.Equal(out[56:], b[56:]) || out[54] != 0 || out[55] != 0 {
		t.Fatal("extension, padding or disabled UDP checksum changed", err)
	}
	if _, err := icmpv4.ParseError(out); err != nil {
		t.Fatal(err)
	}
}

func FuzzICMPErrorParser(f *testing.F) {
	q := wire(UDP, netip.MustParseAddr("203.0.113.9"), remote, 40000, 443, 0, nil)
	f.Add(errorWire(q, 3, 4))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := icmpv4.ParseError(b)
		if err != nil {
			return
		}
		out, err := p.Translate(netip.MustParseAddr("10.17.0.2"), 31000)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := icmpv4.ParseError(out); err != nil && b[8] != 2 {
			t.Fatal("translation invalidated error", err)
		}
	})
}
