// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnellease

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type manualRenewalTimer struct {
	channel chan time.Time
	once    sync.Once
}

func (t *manualRenewalTimer) C() <-chan time.Time { return t.channel }
func (t *manualRenewalTimer) Stop() bool {
	t.once.Do(func() {})
	return true
}

type manualRenewalClock struct {
	mu     sync.Mutex
	now    time.Time
	timers chan *manualRenewalTimer
}

func (c *manualRenewalClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *manualRenewalClock) NewTimer(time.Duration) RenewalTimer {
	timer := &manualRenewalTimer{channel: make(chan time.Time, 1)}
	c.timers <- timer
	return timer
}
func (c *manualRenewalClock) advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	now := c.now
	c.mu.Unlock()
	timer := <-c.timers
	timer.channel <- now
}

type renewalClient struct {
	mu       sync.Mutex
	now      func() time.Time
	ttl      time.Duration
	renewals int
	releases int
	err      error
}

func (c *renewalClient) Renew(_ context.Context, lease Lease) (Lease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.renewals++
	if c.err != nil {
		return Lease{}, c.err
	}
	lease.Generation++
	lease.ValidUntil = c.now().Add(c.ttl)
	return lease, nil
}
func (c *renewalClient) Release(context.Context, Lease) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releases++
	return nil
}

func renewalFixture(now time.Time) Lease {
	var id ID
	id[0] = 1
	return Lease{
		ID: id, ExitPeer: peer.ID("exit"), ClientPeer: peer.ID("client"), Generation: 1,
		IPv4: netip.MustParsePrefix("10.90.0.2/32"), ValidUntil: now.Add(time.Minute),
	}
}

func TestRenewalJitterIsBoundedAndCannotDelayPastLead(t *testing.T) {
	now := time.Unix(1000, 0)
	validUntil := now.Add(time.Minute)
	if delay := nextRenewalDelay(validUntil, now, 20*time.Second, func(maximum time.Duration) time.Duration {
		return maximum
	}); delay != 35*time.Second {
		t.Fatalf("maximum early jitter delay = %s", delay)
	}
	if delay := nextRenewalDelay(validUntil, now, 20*time.Second, func(time.Duration) time.Duration { return -1 }); delay != 40*time.Second {
		t.Fatalf("invalid jitter was not fenced: %s", delay)
	}
}

func TestRenewerKeepsLeaseIdentityAcrossMultipleTTLs(t *testing.T) {
	clock := &manualRenewalClock{now: time.Unix(1000, 0), timers: make(chan *manualRenewalTimer, 4)}
	client := &renewalClient{now: clock.Now, ttl: time.Minute}
	config := DefaultRenewerConfig(client, renewalFixture(clock.Now()))
	config.Clock = clock
	config.LeadTime = 20 * time.Second
	config.RequestTimeout = time.Second
	config.ReleaseOnClose = true
	renewer, err := NewRenewer(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := renewer.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial, _ := renewer.Current()
	for wanted := uint64(2); wanted <= 4; wanted++ {
		clock.advance(40 * time.Second)
		event := waitRenewalEvent(t, renewer.Events(), RenewalSucceeded)
		if event.Generation != wanted {
			t.Fatalf("renewal generation=%d want=%d", event.Generation, wanted)
		}
	}
	current, err := renewer.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != initial.ID || current.IPv4 != initial.IPv4 || current.Generation != 4 {
		t.Fatalf("lease identity changed: initial=%+v current=%+v", initial, current)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := renewer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.renewals != 3 || client.releases != 1 {
		t.Fatalf("renewals/releases=%d/%d", client.renewals, client.releases)
	}
}

func TestRenewerFailureCannotInstallMalformedOrStaleLease(t *testing.T) {
	clock := &manualRenewalClock{now: time.Unix(1000, 0), timers: make(chan *manualRenewalTimer, 4)}
	client := &renewalClient{now: clock.Now, ttl: time.Minute, err: errors.New("controlled renewal failure")}
	config := DefaultRenewerConfig(client, renewalFixture(clock.Now()))
	config.Clock = clock
	config.LeadTime = 20 * time.Second
	config.RetryDelay = 10 * time.Second
	renewer, err := NewRenewer(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := renewer.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.advance(40 * time.Second)
	waitRenewalEvent(t, renewer.Events(), RenewalFailed)
	current, currentErr := renewer.Current()
	if current.Generation != 1 || currentErr == nil {
		t.Fatalf("failed renewal mutated current lease: %+v err=%v", current, currentErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := renewer.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func waitRenewalEvent(t *testing.T, events <-chan RenewalEvent, wanted RenewalEventType) RenewalEvent {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("renewal events closed")
			}
			if event.Type == wanted {
				return event
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for renewal event %d", wanted)
		}
	}
}
