// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

type retryTestCloser struct{ calls, failures int }

func (c *retryTestCloser) Close() error {
	c.calls++
	if c.calls <= c.failures {
		return errors.New("transient cleanup failure")
	}
	return nil
}

func TestDaemonCloseRetriesWithoutLosingOwner(t *testing.T) {
	c := &retryTestCloser{failures: 1}
	d := &Daemon{clientCore: c}
	if err := d.Close(context.Background()); err == nil || d.clientCore != c {
		t.Fatalf("failed cleanup lost ownership: %v", err)
	}
	for range 2 {
		if err := d.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if c.calls != 2 || d.clientCore != nil {
		t.Fatalf("calls=%d owner=%v", c.calls, d.clientCore)
	}
}

func TestRuntimeCoreRetriesOnlyFailedClosers(t *testing.T) {
	a, b := &retryTestCloser{}, &retryTestCloser{failures: 1}
	c := &runtimeCore{values: []io.Closer{a, b}}
	if c.Close() == nil {
		t.Fatal("missing first cleanup failure")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if a.calls != 1 || b.calls != 2 {
		t.Fatalf("successful resource closed again: a=%d b=%d", a.calls, b.calls)
	}
}

func TestRuntimeCloseRetriesCoreBeforeRemovingProtection(t *testing.T) {
	c := &retryTestCloser{failures: 1}
	protection := &fakeProtection{state: killswitch.StateNoExit}
	r, err := New(Config{
		Network:  &fakeNetwork{current: networkstate.State{Generation: 1}, changes: make(chan networkstate.State), errors: make(chan error)},
		Underlay: &fakeOracle{}, Protection: protection, Core: &runtimeCore{values: []io.Closer{c}},
		NewSelector: func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error) {
			return nil, errors.New("unexpected selection")
		},
		Now: time.Now, OperationTimeout: time.Second, UnderlayInterval: time.Minute, EventCapacity: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(context.Background()); err == nil {
		t.Fatal("lost cleanup error")
	}
	if !protection.Snapshot().State.Protected() {
		t.Fatal("cleanup failure exposed WAN")
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if protection.Snapshot().State.Protected() || c.calls != 2 {
		t.Fatal("cleanup was not retried")
	}
	if err := r.Close(context.Background()); err != nil || c.calls != 2 {
		t.Fatal("cleanup was repeated after success")
	}
}

func TestDNSCloseRetriesTransientRevert(t *testing.T) {
	attempts := 0
	m, err := newTunnelDNSManager("hydra0", []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		func(_ context.Context, args ...string) error {
			if args[0] == "revert" {
				attempts++
				if attempts == 1 {
					return errors.New("resolvectl temporarily unavailable")
				}
			}
			return nil
		}, func(string) (int, error) { return 42, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := m.activate(context.Background(), 1, tunnelDNSTestRequest()); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err == nil || m.current.token == 0 {
		t.Fatal("lost failed DNS cleanup")
	}
	if err := m.Close(); err != nil || m.current.token != 0 || attempts != 2 {
		t.Fatalf("retry: %v", err)
	}
}
