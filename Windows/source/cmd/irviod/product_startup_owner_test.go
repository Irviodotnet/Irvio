// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/trafficstats"
)

type startupOwnerDaemon struct {
	desktopDaemon
	start           func(context.Context) error
	close           func(context.Context) error
	node            func() *discovery.Node
	started, closed int
	run             context.Context
}

func (d *startupOwnerDaemon) Start(ctx context.Context) error {
	d.started++
	d.run = ctx
	if d.start != nil {
		return d.start(ctx)
	}
	return nil
}

func (d *startupOwnerDaemon) Close(ctx context.Context) error {
	d.closed++
	if d.close != nil {
		return d.close(ctx)
	}
	return nil
}

func (d *startupOwnerDaemon) Node() *discovery.Node {
	if d.node != nil {
		return d.node()
	}
	return &discovery.Node{}
}

func (d *startupOwnerDaemon) Status() irvioruntime.Status {
	return irvioruntime.Status{State: irvioruntime.StateConnected}
}

func (d *startupOwnerDaemon) TrafficSource() trafficstats.Source {
	return func() trafficstats.Snapshot { return trafficstats.Snapshot{} }
}

func startupOwnerController(t *testing.T, protected bool) *desktopController {
	t.Helper()
	dir := t.TempDir()
	parent := filepath.Join(dir, "runtime")
	if runtime.GOOS == "linux" {
		if err := os.Mkdir(parent, 0700); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bootstrap, err := productbootstrap.New(productbootstrap.Config{DataDir: dir, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bootstrap.Close)
	d := &desktopController{parent: context.Background(), dataDir: dir, bootstrap: bootstrap,
		logger: logger, intent: productintent.Store{Path: filepath.Join(parent, "session-state")},
		ensureProtection: func(context.Context) error { return nil }, cleanupProtection: func(context.Context) error { return nil }}
	if protected {
		d.desiredCountry, d.recoveryPending = "NL", true
		err = d.intent.Want("NL")
	} else {
		d.desiredPrivate.Hosting = true
		err = d.intent.WantPrivate("", true)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestProductStartupRetainsFailedCleanupOwner(t *testing.T) {
	for _, protected := range []bool{false, true} {
		for _, stage := range []string{"start", "request-cancel", "attach", "after-attach-cancel"} {
			t.Run(stage+map[bool]string{false: "-hosting", true: "-protected"}[protected], func(t *testing.T) {
				d := startupOwnerController(t, protected)
				var diagnostics bytes.Buffer
				d.logger = slog.New(slog.NewJSONHandler(&diagnostics, nil))
				protectCalls := 0
				d.ensureProtection = func(ctx context.Context) error { protectCalls++; return ctx.Err() }
				startErr, cleanupErr := errors.New("startup failure"), errors.New("native cleanup failed")
				cleanupOK := false
				fake := &startupOwnerDaemon{close: func(ctx context.Context) error {
					if ctx.Err() != nil {
						t.Fatal("rollback inherited canceled request")
					}
					if !cleanupOK {
						return cleanupErr
					}
					return nil
				}}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				switch stage {
				case "start":
					fake.start = func(context.Context) error { return startErr }
				case "request-cancel":
					fake.start = func(context.Context) error { cancel(); return nil }
				case "attach":
					fake.node = func() *discovery.Node { return nil }
				case "after-attach-cancel":
					fake.node = func() *discovery.Node { cancel(); return &discovery.Node{} }
				}
				err := d.startDaemonLocked(ctx, fake, true, protected)
				if !errors.Is(err, cleanupErr) || stage == "start" && !errors.Is(err, startErr) {
					t.Fatalf("lost startup/cleanup cause: %v", err)
				}
				if !strings.Contains(diagnostics.String(), `"msg":"PRODUCT_DESKTOP_START_FAILED"`) || stage == "start" && !strings.Contains(diagnostics.String(), `"reason":"startup failure"`) {
					t.Fatalf("startup cause absent from diagnostics: %s", diagnostics.String())
				}
				if d.daemon != fake || !d.startupRollback || fake.run.Err() == nil || fake.closed != 1 {
					t.Fatal("failed startup lost its canceled, retryable owner")
				}
				status := d.Status()
				if status.State == irvioruntime.StateIdle || status.State == irvioruntime.StateConnected || d.idleWithoutIntent() || d.TrafficStats().Active {
					t.Fatal("uncommitted runtime advertised idle/connected/traffic readiness")
				}
				if protected && status.ProtectedMode != killswitch.StateNoExit {
					t.Fatal("lost fail-closed status")
				}
				countries, err := d.AvailableCountries(context.Background(), true)
				if err != nil || countries.Status != rendezvous.AvailableCountriesUnavailable {
					t.Fatal("query reached partial runtime", err)
				}
				replacement := &startupOwnerDaemon{}
				if err := d.startDaemonLocked(context.Background(), replacement, true, protected); !errors.Is(err, irvioruntime.ErrInvalidState) || replacement.started != 0 {
					t.Fatal("replacement overlapped failed owner", err)
				}
				if err := d.rollbackStartupLocked(context.Background()); !errors.Is(err, cleanupErr) || d.daemon != fake {
					t.Fatal("retry discarded owner", err)
				}
				cleanupOK = true
				if err := d.rollbackStartupLocked(context.Background()); err != nil || d.daemon != nil || d.cancel != nil || d.startupRollback {
					t.Fatal("successful cleanup did not release owner", err)
				}
				if protected && protectCalls != 3 || !protected && protectCalls != 0 {
					t.Fatal("wrong protection reinstallation", protectCalls)
				}
				intent, present, err := d.intent.Read()
				if err != nil || !present || intent.TeardownPending || intent.ConnectIntent != protected {
					t.Fatal("rollback changed user intent", err)
				}
				if err := d.startDaemonLocked(context.Background(), replacement, true, protected); err != nil {
					t.Fatal("replacement after cleanup", err)
				}
				if d.daemon != replacement || d.startupRollback || d.recoveryPending {
					t.Fatal("replacement not committed")
				}
				if err := d.Disconnect(context.Background()); err != nil {
					t.Fatal(err)
				}
				if d.daemon != nil || d.Status().State != irvioruntime.StateIdle || !d.idleWithoutIntent() {
					t.Fatal("not idle after full leave")
				}
				if _, present, err := d.intent.Read(); err != nil || present {
					t.Fatal("Disconnect left intent", err)
				}
			})
		}
	}
}

func TestProductStartupDisconnectKeepsPendingOwner(t *testing.T) {
	d := startupOwnerController(t, true)
	var diagnostics bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&diagnostics, nil))
	cleanupOK := false
	failure := errors.New("retry cleanup")
	fake := &startupOwnerDaemon{start: func(context.Context) error { return errors.New("start") }, close: func(context.Context) error {
		if !cleanupOK {
			return failure
		}
		return nil
	}}
	if err := d.startDaemonLocked(context.Background(), fake, true, true); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := d.Disconnect(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics.String(), "PRODUCT_DESKTOP_DISCONNECT_FAILED") || !strings.Contains(diagnostics.String(), `reason="retry cleanup"`) {
		t.Fatal("Disconnect diagnostic lost cleanup cause", diagnostics.String())
	}
	state, present, err := d.intent.Read()
	if err != nil || !present || !state.TeardownPending || state.ConnectIntent || d.daemon != fake || !d.teardownPending || d.desiredCountry != "" {
		t.Fatal("failed Disconnect lost cleanup ownership or resurrected intent", err)
	}
	if d.Status().State != irvioruntime.StateDisconnecting || !d.DisconnectRetryNeeded() {
		t.Fatal("missing Disconnect retry state")
	}
	cleanupOK = true
	if err := d.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.daemon != nil || d.startupRollback || d.teardownPending || !d.idleWithoutIntent() {
		t.Fatal("retry left residue")
	}
}

func TestProductStartupRecoveryRetriesOwnedRollback(t *testing.T) {
	d := startupOwnerController(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("retry cleanup")
	fake := &startupOwnerDaemon{start: func(context.Context) error { return errors.New("start") }}
	fake.close = func(context.Context) error {
		if fake.closed == 2 {
			cancel()
		}
		return failure
	}
	if err := d.startDaemonLocked(context.Background(), fake, true, true); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	d.recoveryLoop(ctx)
	if fake.closed != 2 || d.daemon != fake || !d.startupRollback {
		t.Fatal("recovery did not retry retained owner")
	}
	fake.close = nil
	if err := d.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProductStartupCommittedLifetimeAndProtectionFailure(t *testing.T) {
	d := startupOwnerController(t, true)
	fake := &startupOwnerDaemon{}
	ctx, cancel := context.WithCancel(context.Background())
	if err := d.startDaemonLocked(ctx, fake, true, true); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-fake.run.Done():
		t.Fatal("committed runtime inherited API cancellation")
	case <-time.After(10 * time.Millisecond):
	}
	if err := d.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.run.Err() == nil {
		t.Fatal("Disconnect did not cancel runtime")
	}
	d = startupOwnerController(t, true)
	protectErr := errors.New("protection install failed")
	d.ensureProtection = func(context.Context) error { return protectErr }
	fake = &startupOwnerDaemon{start: func(context.Context) error { return errors.New("start") }}
	if err := d.startDaemonLocked(context.Background(), fake, true, true); !errors.Is(err, protectErr) {
		t.Fatal("lost protection error", err)
	}
	if d.daemon != nil || !d.recoveryPending || d.desiredCountry != "NL" || d.idleWithoutIntent() {
		t.Fatal("lost recovery intent after cleaned startup")
	}
	if err := d.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
}
