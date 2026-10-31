// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/reachability"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type renewalDelayedChallenge struct {
	base  testInboundChallenge
	delay *atomic.Bool
}

func TestPublicRoleAutomaticallyReplacesLostVerifier(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	start := func() *discovery.Node {
		n, err := discovery.Start(context.Background(), discovery.Config{DataDir: t.TempDir(), DHTMode: "server", Logger: logger})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { n.Close() })
		return n
	}
	subject, first, second, replacement := start(), start(), start(), start()
	base := PublicRoleConfig{OperationTimeout: 4 * time.Second, ProofTTL: 8 * time.Second, ReverifyLead: 4 * time.Second, RetryDelay: 50 * time.Millisecond, AllowTestAddresses: true, Logger: logger}
	var firstRole *publicRole
	for _, n := range []*discovery.Node{first, second, replacement} {
		c := base
		c.Challenger = testInboundChallenge{key: n.IdentityPrivateKey()}
		r, err := newPublicRole(context.Background(), n, c)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if n == first {
			firstRole = r
		}
	}
	address := func(n *discovery.Node, ip string) string {
		value, err := n.IPv4LoopbackAddress()
		if err != nil {
			t.Fatal(err)
		}
		return strings.Replace(value, "/ip4/127.0.0.1/", "/ip4/"+ip+"/", 1)
	}
	base.Requested = true
	base.DiscoverVerifiers = true
	base.VerifierAddresses = []string{address(first, "127.0.0.2"), address(second, "127.0.0.3")}
	base.VerifierBootstrap = []string{address(replacement, "127.0.0.4"), address(second, "127.0.0.3")}
	base.Challenger = testInboundChallenge{key: subject.IdentityPrivateKey()}
	role, err := newPublicRole(context.Background(), subject, base)
	if err != nil {
		t.Fatal(err)
	}
	defer role.Close()
	if err = role.Enable(context.Background(), networkstate.State{Generation: 1, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = firstRole.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(20 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("replacement never became verified: %+v", role.Status())
		case <-ticker.C:
			snapshot := role.Status().IPv4
			if !snapshot.Ready(time.Now()) {
				continue
			}
			for _, signed := range snapshot.Proof.Attestations {
				value, err := reachability.VerifyAttestation(signed, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if peer.ID(value.VerifierPeerId) == replacement.PeerID() {
					return
				}
			}
		}
	}
}

func (a renewalDelayedChallenge) Challenge(ctx context.Context, target peer.AddrInfo, c *irviov1.ReachabilityChallenge) error {
	if a.delay.Load() {
		select {
		case <-time.After(1500 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return a.base.Challenge(ctx, target, c)
}

func TestDistributorRecoveryM11RenewalPreservesRendezvous(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	start := func() *discovery.Node {
		n, e := discovery.Start(context.Background(), discovery.Config{DataDir: t.TempDir(), DHTMode: "server", Logger: logger})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { n.Close() })
		return n
	}
	subject, first, second := start(), start(), start()
	delay := &atomic.Bool{}
	base := PublicRoleConfig{OperationTimeout: 4 * time.Second, ProofTTL: 8 * time.Second, ReverifyLead: 4 * time.Second, RetryDelay: time.Second, AllowTestAddresses: true, Logger: logger}
	for _, n := range []*discovery.Node{first, second} {
		c := base
		c.Challenger = renewalDelayedChallenge{base: testInboundChallenge{key: n.IdentityPrivateKey()}, delay: delay}
		role, e := newPublicRole(context.Background(), n, c)
		if e != nil {
			t.Fatal(e)
		}
		defer role.Close()
	}
	a, e := first.IPv4LoopbackAddress()
	if e != nil {
		t.Fatal(e)
	}
	b, e := second.IPv4LoopbackAddress()
	if e != nil {
		t.Fatal(e)
	}
	base.Requested = true
	base.VerifierAddresses = []string{a, b}
	base.Challenger = testInboundChallenge{key: subject.IdentityPrivateKey()}
	role, e := newPublicRole(context.Background(), subject, base)
	if e != nil {
		t.Fatal(e)
	}
	defer role.Close()
	if e = role.Enable(context.Background(), networkstate.State{Generation: 1, ObservedAt: time.Now()}); e != nil {
		t.Fatal(e)
	}
	config := rendezvous.DefaultConfig()
	registry, e := rendezvous.NewRegistry(rendezvous.RegistryOptions{Config: config, ServerPeer: subject.PeerID(), ServerIncarnation: 1, Signer: subject.IdentityPrivateKey(), Resolver: rendezvous.UnknownCountryResolver{}, ControlAddresses: subject.ListenAddresses()})
	if e != nil {
		t.Fatal(e)
	}
	service, e := rendezvous.NewService(rendezvous.ServiceOptions{Host: subject.Host(), Registry: registry})
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	runtime := &m12Runtime{node: subject, public: role, config: M12Config{Policy: config}, logger: logger, ctx: context.Background(), service: service, registry: registry}
	delay.Store(true)
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("no automatic renewal observed")
		case <-ticker.C:
			snapshot := role.Status().IPv4
			if snapshot.State != reachability.StateVerifying {
				continue
			}
			if !snapshot.Proof.ExpiresAt.After(time.Now()) {
				t.Fatal("fixture proof already expired")
			}
			runtime.reconcileService()
			if runtime.service == nil {
				t.Fatalf("automatic VERIFYING closed live Rendezvous registry while previous quorum proof remains valid for %s", time.Until(snapshot.Proof.ExpiresAt))
			}
			return
		}
	}
}
