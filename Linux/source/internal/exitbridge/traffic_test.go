// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitbridge

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

func TestTrafficSeparatesOwnSharedControlAndRetiredPackets(t *testing.T) {
	var totals Traffic
	upstreamControl := netip.MustParseAddrPort("10.23.0.1:7890")
	for generation := uint64(1); generation <= 2; generation++ {
		host := newHostTUN()
		b, err := New(Config{TUN: host, Control: control, Limit: 8, Traffic: &totals,
			Forward: func(_ context.Context, _ *Peer, packet []byte) error {
				if packet[len(packet)-1] == 99 {
					return ErrStale
				}
				return nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.Close() })
		p, err := b.Attach(downstream, false)
		if err != nil {
			t.Fatal(err)
		}
		u, err := b.Attach(own, true, upstreamControl)
		if err != nil {
			t.Fatal(err)
		}
		write := func(packet []byte) {
			t.Helper()
			if n, err := b.Write([][]byte{packet}, 0); n != 1 || err != nil {
				t.Fatalf("write=%d err=%v", n, err)
			}
		}
		outgoing := func(packet []byte) {
			t.Helper()
			host.read <- packet
			if got := receive(t, readAsync(b)); len(got) != len(packet) {
				t.Fatal("wrong outgoing packet")
			}
		}
		outgoing(udp(own, internet, 5000, 443, 1))
		outgoing(tcp(own, upstreamControl.Addr(), 5001, upstreamControl.Port(), 2))
		write(udp(internet, own, 443, 5000, 3))
		write(tcp(upstreamControl.Addr(), own, upstreamControl.Port(), 5001, 4))
		write(udp(downstream, internet, 6000, 443, 5))
		write(tcp(downstream, control.Addr(), 6001, control.Port(), 6))
		write(udp(downstream, internet, 6000, 443, 99))
		write([]byte{0})
		write(udp(internet, netip.MustParseAddr("9.9.9.9"), 443, 5000, 7))
		if err := b.Reply(context.Background(), p, udp(internet, downstream, 443, 6000, 8)); err != nil {
			t.Fatal(err)
		}
		receive(t, readAsync(b))
		if err := b.enqueue(context.Background(), u, udp(own, internet, 5000, 443, 9)); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = b.Retire(ctx, u)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Reply(context.Background(), p, udp(internet, downstream, 443, 6000, 10)); err != nil {
			t.Fatal(err)
		}
		receive(t, readAsync(b))
		if got := totals.Snapshot(); got != (TrafficSnapshot{29 * generation, 29 * generation, 29 * generation}) {
			t.Fatalf("generation %d totals=%+v", generation, got)
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		if got := totals.Snapshot(); got != (TrafficSnapshot{29 * generation, 29 * generation, 29 * generation}) {
			t.Fatalf("cleanup lost totals: %+v", got)
		}
	}
}

func TestTrafficControlMetadataValidation(t *testing.T) {
	b, err := New(Config{TUN: newHostTUN(), Control: control, Limit: 8, Forward: func(context.Context, *Peer, []byte) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	address := netip.MustParseAddr("10.23.0.3")
	for _, controls := range [][]netip.AddrPort{
		{{}}, {netip.MustParseAddrPort("0.0.0.0:7890")},
		{control, control}, {netip.MustParseAddrPort("10.23.0.1:0")},
	} {
		if _, err := b.Attach(address, true, controls...); err == nil {
			t.Fatal("invalid upstream control metadata accepted")
		}
	}
	if _, err := b.Attach(address, false, control); err == nil {
		t.Fatal("downstream control metadata accepted")
	}
	if _, err := b.Attach(address, true, control); err != nil {
		t.Fatal("valid metadata rejected or failed call retained attachment", err)
	}
}
