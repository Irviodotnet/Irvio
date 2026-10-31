// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/underlay"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

func TestWindowsNativeTCPProbes(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_PROBE_TEST") != "1" {
		t.Skip("SYSTEM VM and mini-PC nonce oracle required")
	}
	ctx := context.Background()
	c := nativeProtectionConfig(t)
	store, err := wfp.NewStore(c.Owner)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Inspect(ctx)
	if err != nil || prior.Installed {
		t.Fatal("preexisting WFP fixture", err)
	}
	t.Cleanup(func() {
		if err := store.Remove(ctx); err != nil {
			t.Error(err)
		}
	})
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c.Network.Application, err = wfp.ApplicationIDForPath(self)
	if err != nil {
		t.Fatal(err)
	}
	path, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewWindowsBackend(c)
	if err != nil {
		t.Fatal(err)
	}
	i, _, err := b.Ensure(ctx, c.Policy)
	if err != nil {
		t.Fatal(err)
	}
	u, err := b.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	o, err := u.OwnUnderlay(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Error(err)
		}
	})
	generation := uint64(1)
	factory, err := controlsocket.DialerForOwnedBindings(func(ctx context.Context, target netip.AddrPort) (winroute.Binding, io.Closer, error) {
		grant, err := o.Acquire(ctx, generation, WindowsBypass{Interface: path.Binding.LUID, Source: path.Binding.Source, Remote: target, Protocol: 6})
		if grant == nil {
			return path.Binding, nil, err
		}
		return path.Binding, grant, err
	})
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := underlay.NewTCPChallengeProbe(factory)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := exitselector.NewTCPProbe(factory)
	if err != nil {
		t.Fatal(err)
	}
	target := netip.MustParseAddrPort("192.168.23.2:49496")
	assertDrained := func() {
		t.Helper()
		if len(o.leases) != 0 || len(b.network.Underlay) != 0 {
			t.Fatal("probe retained authorization")
		}
		if err := b.verify(ctx); err != nil {
			t.Fatal("native state differs from ownership", err)
		}
	}
	for cycle := range 20 {
		started := time.Now()
		bounded, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		err := challenge.Probe(bounded, underlay.Target{Endpoint: target})
		cancel()
		if err != nil {
			t.Fatal("nonce exchange", cycle, err)
		}
		t.Logf("nonce cycle=%d total=%s", cycle, time.Since(started))
		assertDrained()
		started = time.Now()
		bounded, cancel = context.WithTimeout(ctx, 750*time.Millisecond)
		result, err := candidate.Probe(bounded, exitselector.Candidate{ControlEndpoint: target, NetworkEpoch: 7})
		cancel()
		if err != nil || !result.Reachable || result.NetworkEpoch != 7 || result.ObservedAt.IsZero() {
			t.Fatal("candidate observation", cycle, result, err)
		}
		t.Logf("candidate cycle=%d dial=%s total=%s", cycle, result.Latency, time.Since(started))
		assertDrained()
	}
	for cycle := range 5 {
		type observation struct {
			index          int
			latency, total time.Duration
			reachable      bool
			err            error
		}
		results := make(chan observation, 5)
		start := make(chan struct{})
		for index := range 5 {
			go func() {
				<-start
				started := time.Now()
				bounded, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
				defer cancel()
				endpoint := netip.AddrPortFrom(target.Addr(), uint16(49500+index))
				r, err := candidate.Probe(bounded, exitselector.Candidate{ControlEndpoint: endpoint, NetworkEpoch: 7})
				results <- observation{index: index, latency: r.Latency, total: time.Since(started), reachable: r.Reachable, err: err}
			}()
		}
		close(start)
		failed := false
		for range 5 {
			r := <-results
			t.Logf("parallel cycle=%d candidate=%d dial=%s total=%s reachable=%t err=%v", cycle, r.index, r.latency, r.total, r.reachable, r.err)
			failed = failed || r.err != nil || !r.reachable
		}
		assertDrained()
		if failed {
			t.Fatal("production five-candidate/750ms concurrency budget failed")
		}
	}
	for _, failure := range []struct {
		target string
		want   error
	}{{"192.168.23.2:49497", underlay.ErrChallengeMismatch}, {"192.168.23.2:49498", context.DeadlineExceeded}} {
		bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := challenge.Probe(bounded, underlay.Target{Endpoint: netip.MustParseAddrPort(failure.target)})
		cancel()
		var timeout net.Error
		matched := errors.Is(err, failure.want) || failure.want == context.DeadlineExceeded && errors.As(err, &timeout) && timeout.Timeout()
		if !matched {
			t.Fatal("wrong failure classification", failure.target, err)
		}
		assertDrained()
	}
	if err := o.Advance(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := challenge.Probe(ctx, underlay.Target{Endpoint: target}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("stale underlay probe escaped generation", err)
	}
	if result, err := candidate.Probe(ctx, exitselector.Candidate{ControlEndpoint: target}); !errors.Is(err, ErrStaleGeneration) || result.Reachable {
		t.Fatal("stale candidate probe escaped generation", result, err)
	}
	assertDrained()
	generation = 2
	bounded, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	err = challenge.Probe(bounded, underlay.Target{Endpoint: target})
	cancel()
	if err != nil {
		t.Fatal("fresh generation failed", err)
	}
	assertDrained()
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := i.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	t.Log("20 nonce/candidate cycles, mismatch, timeout, stale-generation refusal, fresh-generation recovery and grant drain PASS")
}
