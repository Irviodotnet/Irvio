// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
)

type probeDialFunc func(context.Context, string, string) (net.Conn, error)

func (f probeDialFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

type probeCloseConn struct {
	net.Conn
	err error
}

func (c probeCloseConn) Close() error { return errors.Join(c.Conn.Close(), c.err) }

func TestExplicitTCPProbePreservesObservationAndCleanup(t *testing.T) {
	for _, failClose := range []bool{false, true} {
		client, server := net.Pipe()
		cleanupErr := errors.New("revocation failed")
		factory := func(address multiaddr.Multiaddr) (tcptransport.ContextDialer, error) {
			if address.String() != "/ip6/2001:db8::1/tcp/4000" {
				t.Fatal(address)
			}
			return probeDialFunc(func(_ context.Context, network, address string) (net.Conn, error) {
				if network != "tcp6" || address != "[2001:db8::1]:4000" {
					t.Fatal(network, address)
				}
				var err error
				if failClose {
					err = cleanupErr
				}
				return probeCloseConn{Conn: client, err: err}, nil
			}), nil
		}
		probe, err := NewTCPProbe(factory)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
		probe.now = func() time.Time { now = now.Add(time.Millisecond); return now }
		result, err := probe.Probe(context.Background(), Candidate{ControlEndpoint: netip.MustParseAddrPort("[2001:db8::1]:4000"), NetworkEpoch: 42})
		client.Close()
		server.Close()
		if !result.Reachable || result.NetworkEpoch != 42 || result.Latency != time.Millisecond || !result.ObservedAt.Equal(now) {
			t.Fatal("observation changed", result)
		}
		if failClose && !errors.Is(err, cleanupErr) || !failClose && err != nil {
			t.Fatal("cleanup error hidden", err)
		}
	}
}

func TestExplicitTCPProbeNoPolicyFallback(t *testing.T) {
	if _, err := NewTCPProbe(nil); !errors.Is(err, ErrProbeUnavailable) {
		t.Fatal(err)
	}
	denied := errors.New("generation unavailable")
	calls := 0
	probe, _ := NewTCPProbe(func(multiaddr.Multiaddr) (tcptransport.ContextDialer, error) { calls++; return nil, denied })
	for _, text := range []string{"127.0.0.1:4000", "0.0.0.0:4000", "224.0.0.1:4000", "[::ffff:192.0.2.1]:4000", "[fe80::1%2]:4000", "192.0.2.1:0"} {
		if _, err := probe.Probe(context.Background(), Candidate{ControlEndpoint: netip.MustParseAddrPort(text)}); !errors.Is(err, ErrInvalidCandidate) {
			t.Fatal(text, err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid target reached socket policy")
	}
	result, err := probe.Probe(context.Background(), Candidate{ControlEndpoint: netip.MustParseAddrPort("192.0.2.1:4000"), NetworkEpoch: 9})
	if !errors.Is(err, denied) || calls != 1 || result.Reachable || result.NetworkEpoch != 9 {
		t.Fatal("failed policy reported reachable", result, err)
	}
}
