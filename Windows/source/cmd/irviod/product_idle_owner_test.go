// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
)

type idleOwnerProbe struct {
	startupOwnerDaemon
	query   func(context.Context) (rendezvous.AvailableCountries, error)
	queries int
}

func (p *idleOwnerProbe) AvailableCountries(ctx context.Context, refresh bool) (rendezvous.AvailableCountries, error) {
	p.queries++
	return p.query(ctx)
}

func idleOwnerController(t *testing.T) *desktopController {
	t.Helper()
	d := startupOwnerController(t, false)
	d.desiredPrivate = privateProductIntent{}
	if err := d.intent.Clear(); err != nil {
		t.Fatal(err)
	}
	d.ensureProtection = func(context.Context) error { t.Error("idle probe enabled protection"); return nil }
	return d
}

func TestProductIdleProbeRetainsCleanupOwner(t *testing.T) {
	for _, stage := range []string{"start", "query", "success", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			d := idleOwnerController(t)
			failure := errors.New("idle cleanup failure")
			queryFailure := errors.New("idle query/start failure")
			cleanupOK := false
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &idleOwnerProbe{query: func(context.Context) (rendezvous.AvailableCountries, error) {
				if stage == "cancel" {
					cancel()
					return rendezvous.AvailableCountries{}, ctx.Err()
				}
				if stage == "query" {
					return rendezvous.AvailableCountries{}, queryFailure
				}
				return rendezvous.AvailableCountries{GeneratedAt: time.Now()}, nil
			}}
			if stage == "start" {
				probe.start = func(context.Context) error { return queryFailure }
			}
			probe.close = func(ctx context.Context) error {
				if ctx.Err() != nil {
					t.Fatal("initial rollback used canceled request")
				}
				if !cleanupOK {
					return failure
				}
				return nil
			}
			_, err := d.queryIdleCountries(ctx, probe)
			if !errors.Is(err, failure) || (stage == "start" || stage == "query") && !errors.Is(err, queryFailure) {
				t.Fatal("lost query/cleanup cause", err)
			}
			if d.idleProbe != probe || !d.idleCleanupPending || probe.run.Err() == nil || probe.closed != 1 || d.idleWithoutIntent() {
				t.Fatal("lost canceled idle probe ownership")
			}
			if stage == "start" && probe.queries != 0 {
				t.Fatal("queried failed Start")
			}
			if d.Status().State != irvioruntime.StateDisconnecting {
				t.Fatal("cleanup residue advertised final IDLE")
			}
			if _, err := d.AvailableCountries(context.Background(), true); !errors.Is(err, failure) {
				t.Fatal("new query bypassed cleanup", err)
			}
			if err := d.Connect(context.Background(), "NL"); !errors.Is(err, failure) {
				t.Fatal("CONNECT bypassed cleanup", err)
			}
			if err := d.ConnectPrivate(context.Background(), "", true); !errors.Is(err, failure) {
				t.Fatal("private hosting bypassed cleanup", err)
			}
			if d.daemon != nil || d.privateStore != nil || d.traffic.Load() != nil {
				t.Fatal("replacement allocated runtime resources")
			}
			if _, present, err := d.intent.Read(); err != nil || present {
				t.Fatal("idle cleanup introduced user intent", err)
			}
			cleanupOK = true
			if _, err := d.AvailableCountries(context.Background(), true); err != nil {
				t.Fatal("cleanup retry", err)
			}
			if d.idleProbe != nil || d.idleCleanupPending || !d.idleWithoutIntent() {
				t.Fatal("cleanup retry left owner")
			}
		})
	}
}

func TestProductIdleProbeDisconnectAndCloseRetry(t *testing.T) {
	for _, command := range []string{"disconnect", "close"} {
		t.Run(command, func(t *testing.T) {
			d := idleOwnerController(t)
			failure := errors.New("idle cleanup failure")
			probe := &idleOwnerProbe{query: func(context.Context) (rendezvous.AvailableCountries, error) {
				return rendezvous.AvailableCountries{}, nil
			}}
			probe.close = func(context.Context) error { return failure }
			if _, err := d.queryIdleCountries(context.Background(), probe); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			operation := d.Disconnect
			if command == "close" {
				operation = d.Close
			}
			if err := operation(context.Background()); !errors.Is(err, failure) || d.idleProbe != probe {
				t.Fatal("lost owner", err)
			}
			if command == "disconnect" && !d.DisconnectRetryNeeded() {
				t.Fatal("missing teardown retry")
			}
			probe.close = nil
			if err := operation(context.Background()); err != nil {
				t.Fatal(err)
			}
			if d.idleProbe != nil || d.idleCleanupPending {
				t.Fatal("cleanup residue")
			}
			if command == "disconnect" && !d.idleWithoutIntent() {
				t.Fatal("not idle after Disconnect")
			}
		})
	}
}

func TestProductIdleProbeRecoveryDoesNotCreateConnectIntent(t *testing.T) {
	d := idleOwnerController(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	probe := &idleOwnerProbe{query: func(context.Context) (rendezvous.AvailableCountries, error) {
		return rendezvous.AvailableCountries{}, nil
	}}
	probe.close = func(context.Context) error {
		if probe.closed == 1 {
			return errors.New("retry cleanup")
		}
		cancel()
		return nil
	}
	if _, err := d.queryIdleCountries(context.Background(), probe); err == nil {
		t.Fatal("missing cleanup failure")
	}
	d.recoveryLoop(ctx)
	if probe.closed != 2 || d.idleProbe != nil || d.idleCleanupPending || !d.idleWithoutIntent() {
		t.Fatal("background cleanup failed")
	}
	if d.daemon != nil || d.traffic.Load() != nil {
		t.Fatal("idle cleanup started network participation")
	}
	if _, present, err := d.intent.Read(); err != nil || present {
		t.Fatal("idle cleanup created ConnectIntent", err)
	}
}

func TestProductIdleProbeSuccessReleasesOwner(t *testing.T) {
	d := idleOwnerController(t)
	stamp := time.Now().UTC()
	probe := &idleOwnerProbe{query: func(context.Context) (rendezvous.AvailableCountries, error) {
		return rendezvous.AvailableCountries{GeneratedAt: stamp}, nil
	}}
	got, err := d.queryIdleCountries(context.Background(), probe)
	if err != nil || !got.GeneratedAt.Equal(stamp) || !d.countries.GeneratedAt.Equal(stamp) || probe.closed != 1 || probe.run.Err() == nil || !d.idleWithoutIntent() {
		t.Fatal("successful query lifecycle", got, err)
	}
}

func TestProductIdleProbeCommandWaitsForFailedCleanup(t *testing.T) {
	d := idleOwnerController(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.discoveryCancel = cancel
	d.discoveryDone = make(chan struct{})
	queryStarted, cleanupStarted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	failure := errors.New("retry cleanup")
	probe := &idleOwnerProbe{query: func(ctx context.Context) (rendezvous.AvailableCountries, error) {
		close(queryStarted)
		<-ctx.Done()
		return rendezvous.AvailableCountries{}, ctx.Err()
	}}
	probe.close = func(context.Context) error {
		if probe.closed == 1 {
			close(cleanupStarted)
			<-release
		}
		return failure
	}
	queryDone := make(chan error, 1)
	go func() {
		_, err := d.queryIdleCountries(ctx, probe)
		queryDone <- err
		close(d.discoveryDone)
	}()
	select {
	case <-queryStarted:
	case <-ctx.Done():
		t.Fatal("query never started")
	}
	commandDone := make(chan error, 1)
	go func() { commandDone <- d.Connect(context.Background(), "NL") }()
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("command did not cancel discovery")
	}
	select {
	case err := <-commandDone:
		t.Fatal("command overlapped query cleanup", err)
	default:
	}
	unblock()
	select {
	case err := <-queryDone:
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("query did not finish")
	}
	select {
	case err := <-commandDone:
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("command did not retry cleanup")
	}
	if d.idleProbe != probe || probe.closed != 2 || d.daemon != nil || d.desiredCountry != "" {
		t.Fatal("lost probe or started overlapping CONNECT")
	}
	probe.close = nil
	if err := d.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
}
