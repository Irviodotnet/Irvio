// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package reachability

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	ma "github.com/multiformats/go-multiaddr"
)

func windowsBoundFactory(t *testing.T, calls *atomic.Int32, deny *atomic.Bool) tcptransport.DialerForAddr {
	t.Helper()
	factory, err := controlsocket.DialerForBindings(func(_ context.Context, target netip.Addr) (winroute.Binding, error) {
		calls.Add(1)
		if deny.Load() {
			return winroute.Binding{}, errors.New("underlay generation unavailable")
		}
		if !target.IsLoopback() {
			return winroute.Binding{}, errors.New("unexpected non-lab target")
		}
		return winroute.ForSource(target)
	})
	if err != nil {
		t.Fatal(err)
	}
	return factory
}

func TestWindowsBoundM11FreshFamilyQuorumAndNoFallback(t *testing.T) {
	for _, family := range []irviov1.ReachabilityFamily{irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4, irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6} {
		t.Run(family.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			key, _, err := crypto.GenerateEd25519Key(nil)
			if err != nil {
				t.Fatal(err)
			}
			subject, err := newTestHostAddresses(key, "/ip4/127.0.0.1/tcp/0", "/ip6/::1/tcp/0")
			if err != nil {
				t.Fatal(err)
			}
			defer subject.Close()
			var targets []peer.AddrInfo
			var challengeCalls [3]atomic.Int32
			var challengeDenied [3]atomic.Bool
			for i := range 3 {
				verifierKey, _, err := crypto.GenerateEd25519Key(nil)
				if err != nil {
					t.Fatal(err)
				}
				verifier, err := newTestHostAddresses(verifierKey, "/ip4/127.0.0.1/tcp/0", "/ip6/::1/tcp/0")
				if err != nil {
					t.Fatal(err)
				}
				defer verifier.Close()
				fresh, err := NewFreshDialerWithTCP(verifierKey, windowsBoundFactory(t, &challengeCalls[i], &challengeDenied[i]))
				if err != nil {
					t.Fatal(err)
				}
				service, err := NewService(ServiceConfig{Host: verifier, PrivateKey: verifierKey, Challenger: fresh, ObservedIP: ObservedIPPolicy{AllowLoopback: true}})
				if err != nil {
					t.Fatal(err)
				}
				defer service.Close()
				targets = append(targets, peer.AddrInfo{ID: verifier.ID(), Addrs: verifier.Addrs()})
				var ipv4 []ma.Multiaddr
				for _, address := range verifier.Addrs() {
					if _, err := address.ValueForProtocol(ma.P_IP4); err == nil {
						ipv4 = append(ipv4, address)
					}
				}
				if err := subject.Connect(ctx, peer.AddrInfo{ID: verifier.ID(), Addrs: ipv4}); err != nil {
					t.Fatal(err)
				}
			}
			var requestCalls atomic.Int32
			var requestDenied atomic.Bool
			requester, err := NewFamilyRequesterWithTCP(key, windowsBoundFactory(t, &requestCalls, &requestDenied), targets)
			if err != nil {
				t.Fatal(err)
			}
			client, err := NewClient(ClientConfig{Host: subject, Requester: requester})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			protocol, expected := ma.P_IP4, netip.MustParseAddr("127.0.0.1")
			if family == irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6 {
				protocol, expected = ma.P_IP6, netip.IPv6Loopback()
			}
			port := tcpPortFamily(t, subject, protocol)
			for range 2 {
				var attestations []*irviov1.SignedReachabilityAttestation
				for _, target := range targets {
					signed, err := client.Request(ctx, target.ID, family, port, 7, 9)
					if err != nil {
						t.Fatal(err)
					}
					value, err := VerifyAttestation(signed, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					observed, ok := netip.AddrFromSlice(value.ObservedIp)
					if !ok || observed != expected {
						t.Fatal("cross-family proof reuse")
					}
					attestations = append(attestations, signed)
				}
				if _, err := BuildProof(subject.ID(), attestations, DefaultProofPolicy()); err != nil {
					t.Fatal(err)
				}
				if _, err := BuildProof(subject.ID(), attestations[:1], DefaultProofPolicy()); !errors.Is(err, ErrInsufficient) {
					t.Fatalf("one verifier met quorum: %v", err)
				}
				if _, err := BuildProof(subject.ID(), []*irviov1.SignedReachabilityAttestation{attestations[0], attestations[0]}, DefaultProofPolicy()); !errors.Is(err, ErrInsufficient) {
					t.Fatalf("duplicate verifier met quorum: %v", err)
				}
			}
			if requestCalls.Load() < 6 {
				t.Fatal("requester reused an unbound existing connection")
			}
			for i := range 3 {
				if challengeCalls[i].Load() < 2 {
					t.Fatal("challenge was not freshly dialed")
				}
			}
			challengeDenied[0].Store(true)
			if signed, err := client.Request(ctx, targets[0].ID, family, port, 7, 9); err == nil || signed != nil {
				t.Fatal("failed fresh binding fell back to existing connection")
			}
			requestDenied.Store(true)
			if signed, err := client.Request(ctx, targets[1].ID, family, port, 7, 9); err == nil || signed != nil {
				t.Fatal("failed request binding fell back to existing connection")
			}
		})
	}
}

func TestExplicitReachabilityTCPPolicyIsRequired(t *testing.T) {
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewFreshDialerWithTCP(key, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("missing policy accepted")
	}
	if _, err := NewFamilyRequesterWithTCP(key, nil, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("missing policy accepted")
	}
}
