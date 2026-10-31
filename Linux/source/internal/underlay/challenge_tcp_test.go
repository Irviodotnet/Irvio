// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package underlay

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
)

type challengeDialFunc func(context.Context, string, string) (net.Conn, error)

func (f challengeDialFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

type challengeCloseConn struct {
	net.Conn
	closed *atomic.Int32
	err    error
}

func (c challengeCloseConn) Close() error { c.closed.Add(1); return errors.Join(c.Conn.Close(), c.err) }

func TestTCPChallengeProbeExchangeAndOwnedCleanup(t *testing.T) {
	for _, mode := range []string{"echo", "mismatch", "cancel", "close-failure"} {
		t.Run(mode, func(t *testing.T) {
			client, server := net.Pipe()
			defer server.Close()
			defer client.Close()
			var closed atomic.Int32
			cleanupErr := errors.New("grant revocation failed")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()
				request := make([]byte, challengeSize)
				if _, err := io.ReadFull(server, request); err != nil {
					return
				}
				if string(request[:4]) != string(challengeMagic[:]) {
					t.Error("wire magic changed")
				}
				if mode == "cancel" {
					cancel()
					io.Copy(io.Discard, server)
					return
				}
				if mode == "mismatch" {
					request[len(request)-1] ^= 1
				}
				_, _ = server.Write(request)
			}()
			factory := func(address multiaddr.Multiaddr) (tcptransport.ContextDialer, error) {
				if address.String() != "/ip4/192.0.2.1/tcp/4000" {
					t.Fatal("endpoint changed", address)
				}
				return challengeDialFunc(func(_ context.Context, network, address string) (net.Conn, error) {
					if network != "tcp4" || address != "192.0.2.1:4000" {
						t.Fatal("dial policy changed", network, address)
					}
					var err error
					if mode == "close-failure" {
						err = cleanupErr
					}
					return challengeCloseConn{Conn: client, closed: &closed, err: err}, nil
				}), nil
			}
			probe, err := NewTCPChallengeProbe(factory)
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { result <- probe.Probe(ctx, Target{Endpoint: netip.MustParseAddrPort("192.0.2.1:4000")}) }()
			select {
			case err := <-result:
				want := map[string]error{"mismatch": ErrChallengeMismatch, "cancel": context.Canceled, "close-failure": cleanupErr}[mode]
				if !errors.Is(err, want) {
					t.Fatalf("probe returned %v want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("probe did not terminate")
			}
			<-done
			if closed.Load() != 1 {
				t.Fatal("probe lost connection ownership")
			}
		})
	}
}

func TestTCPChallengeRejectsInvalidTargetAndPolicyFailure(t *testing.T) {
	if _, err := NewTCPChallengeProbe(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	denied := errors.New("stale network generation")
	var calls int
	probe, _ := NewTCPChallengeProbe(func(multiaddr.Multiaddr) (tcptransport.ContextDialer, error) {
		calls++
		return nil, denied
	})
	for _, text := range []string{"0.0.0.0:4000", "224.0.0.1:4000", "[::ffff:192.0.2.1]:4000", "[fe80::1%2]:4000", "192.0.2.1:0"} {
		if err := probe.Probe(context.Background(), Target{Endpoint: netip.MustParseAddrPort(text)}); !errors.Is(err, ErrInvalidTarget) {
			t.Fatal(text, err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid endpoint reached socket policy")
	}
	if err := probe.Probe(context.Background(), Target{Endpoint: netip.MustParseAddrPort("192.0.2.1:4000")}); !errors.Is(err, denied) || calls != 1 {
		t.Fatal("policy failure bypassed", err, calls)
	}
}
