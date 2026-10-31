// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func TestIRVIOProductPaths(t *testing.T) {
	for got, want := range map[string]string{
		localapi.DefaultSocketPath:       "/run/irvio/control.sock",
		productintent.DefaultPath:        "/run/irvio/session-state",
		productruntime.DefaultConfigPath: "/etc/irvio/config.json",
		productruntime.DefaultDataDir:    "/var/lib/irvio/profile",
		productruntime.DefaultGeoIPPath:  "/var/lib/irvio/geo/country.mmdb",
		productBootMarker:                "/var/lib/irvio/boot-id",
	} {
		if got != want {
			t.Errorf("product path = %q, want %q", got, want)
		}
	}
}

func TestDesktopProfileDeleteReactivatesSystemdSocket(t *testing.T) {
	if base := os.Getenv("IRVIO_TEST_PROFILE_RESET_ACTIVATED"); base != "" {
		t.Setenv("LISTEN_PID", fmt.Sprint(os.Getpid()))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		logs, err := localapi.NewLogStore(localapi.DefaultLogCapacity)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		socket := filepath.Join(base, "api", "control.sock")
		go func() {
			done <- runProductWithContext(ctx, productOptions{
				role: productruntime.Desktop, dataDir: filepath.Join(base, "profile"),
				apiSocket: socket, apiGroup: "irvio", intentPath: filepath.Join(base, "intent", "session-state"),
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)), logStore: logs,
				inspectProtection: func(context.Context) (bool, error) { return false, nil },
			})
		}()
		var client *localapi.Client
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			client, err = localapi.Dial(context.Background(), socket)
			if err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if client == nil {
			t.Fatalf("activated API: %v", err)
		}
		defer client.Close()
		backup := filepath.Join(base, "profile", "bootstrap-v1.before-FI-20261013.pb")
		if err := os.WriteFile(backup, []byte("backup"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := client.DeleteProfile(context.Background()); err == nil {
			t.Fatal("profile with unexpected backup was accepted for deletion")
		}
		if _, err := client.GetStatus(context.Background()); err != nil {
			t.Fatalf("daemon unavailable after rejected deletion: %v", err)
		}
		if err := os.Remove(backup); err != nil {
			t.Fatal(err)
		}
		if err := client.DeleteProfile(context.Background()); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, errProfileResetComplete) {
				t.Fatalf("profile reset result: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("activated profile reset did not complete")
		}
		if err := completeProfileReset(slog.New(slog.NewTextHandler(io.Discard, nil)), true, func() error {
			t.Fatal("activated reset attempted exec without systemd fd")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	base := t.TempDir()
	apiDir := filepath.Join(base, "api")
	if err := os.Mkdir(apiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(apiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "intent"), 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(apiDir, "control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	file, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for cycle := 0; cycle < 3; cycle++ {
		command := exec.Command(os.Args[0], "-test.run=^TestDesktopProfileDeleteReactivatesSystemdSocket$")
		command.ExtraFiles = []*os.File{file}
		command.Env = append(os.Environ(), "IRVIO_TEST_PROFILE_RESET_ACTIVATED="+base, "LISTEN_FDS=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("reset/reactivation %d: %v\n%s", cycle, err, output)
		}
		if _, err := os.Lstat(socket); err != nil {
			t.Fatalf("systemd socket lost after reset: %v", err)
		}
		info, err := os.Stat(apiDir)
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("socket parent permissions were changed: %v %v", info, err)
		}
	}
}

func TestDesktopProductIdleAPIAndBootstrapImport(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "profile")
	socket := filepath.Join(base, "api", "control.sock")
	intentDir := filepath.Join(base, "intent")
	if err := os.Mkdir(intentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	intentPath := filepath.Join(intentDir, "session-state")
	logs, err := localapi.NewLogStore(localapi.DefaultLogCapacity)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runProductWithContext(ctx, productOptions{role: productruntime.Desktop,
			dataDir: dataDir, apiSocket: socket, apiGroup: "irvio", intentPath: intentPath,
			logger: slog.New(slog.NewTextHandler(io.Discard, nil)), logStore: logs,
			inspectProtection: func(context.Context) (bool, error) { return false, nil }})
	}()
	var client *localapi.Client
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		client, err = localapi.Dial(context.Background(), socket)
		if err == nil {
			break
		}
		select {
		case runErr := <-done:
			t.Fatalf("desktop startup failed: %v", runErr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if client == nil {
		t.Fatalf("desktop Local API did not start: %v", err)
	}
	defer client.Close()
	status, err := client.GetStatus(context.Background())
	if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
		t.Fatalf("idle status=%+v err=%v", status, err)
	}
	_, public, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	address := fmt.Sprintf("/ip4/89.124.71.34/tcp/4000/p2p/%s", id)
	bundle, err := productbootstrap.FromMultiaddrs([]string{address}, 0)
	if err != nil {
		t.Fatal(err)
	}
	code, err := productbootstrap.Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.ImportBootstrapBundle(context.Background(), code)
	if err != nil || state.ConfiguredCount != 1 || state.CurrentlyReachableCount != 0 {
		t.Fatalf("idle bootstrap import=%+v err=%v", state, err)
	}
	status, err = client.GetStatus(context.Background())
	if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
		t.Fatalf("import unexpectedly joined network: %+v err=%v", status, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("desktop idle shutdown did not finish")
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("Local API socket after shutdown: %v", err)
	}
}

func TestDesktopSameBootIntentAdoptsProtectionAndDisconnects(t *testing.T) {
	base := t.TempDir()
	intentDir := filepath.Join(base, "run")
	if err := os.Mkdir(intentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := productintent.Store{Path: filepath.Join(intentDir, "session-state")}
	if err := store.Want("NL"); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(base, "api", "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ensured, cleaned int
	done := make(chan error, 1)
	go func() {
		done <- runProductWithContext(ctx, productOptions{role: productruntime.Desktop,
			dataDir: filepath.Join(base, "profile"), apiSocket: socket, intentPath: store.Path,
			logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
			inspectProtection: func(context.Context) (bool, error) { return true, nil },
			ensureProtection:  func(context.Context) error { ensured++; return nil },
			cleanupProtection: func(context.Context) error { cleaned++; return nil },
		})
	}()
	var client *localapi.Client
	var err error
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		client, err = localapi.Dial(context.Background(), socket)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if client == nil {
		t.Fatalf("Local API did not start: %v", err)
	}
	defer client.Close()
	status, err := client.GetStatus(context.Background())
	if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_RECOVERING || status.CountryCode != "NL" {
		t.Fatalf("adopted recovery status=%+v err=%v", status, err)
	}
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, present, err := store.Read(); err != nil || present {
		t.Fatalf("intent after Disconnect: present=%t err=%v", present, err)
	}
	if ensured == 0 || cleaned != 1 {
		t.Fatalf("protection ensure=%d cleanup=%d", ensured, cleaned)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDesktopFinishesInterruptedTeardownBeforeAPI(t *testing.T) {
	base := t.TempDir()
	intentDir := filepath.Join(base, "run")
	if err := os.Mkdir(intentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := productintent.Store{Path: filepath.Join(intentDir, "session-state")}
	if err := store.BeginTeardown(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cleaned int
	done := make(chan error, 1)
	go func() {
		done <- runProductWithContext(ctx, productOptions{role: productruntime.Desktop,
			dataDir: filepath.Join(base, "profile"), apiSocket: filepath.Join(base, "api", "control.sock"),
			intentPath: store.Path, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			inspectProtection: func(context.Context) (bool, error) { return true, nil },
			cleanupProtection: func(context.Context) error { cleaned++; return nil },
		})
	}()
	socket := filepath.Join(base, "api", "control.sock")
	var client *localapi.Client
	var err error
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		client, err = localapi.Dial(context.Background(), socket)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if client == nil {
		t.Fatalf("Local API did not start: %v", err)
	}
	defer client.Close()
	status, err := client.GetStatus(context.Background())
	if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED || cleaned != 1 {
		t.Fatalf("interrupted teardown status=%+v cleaned=%d err=%v", status, cleaned, err)
	}
	if _, present, err := store.Read(); err != nil || present {
		t.Fatalf("intent after cleanup: present=%t err=%v", present, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDesktopIdleStopsAfterLastLocalAPIClientLeaves(t *testing.T) {
	base := t.TempDir()
	intentDir := filepath.Join(base, "run")
	if err := os.Mkdir(intentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(base, "api", "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runProductWithContext(ctx, productOptions{role: productruntime.Desktop,
			dataDir: filepath.Join(base, "profile"), apiSocket: socket,
			intentPath: filepath.Join(intentDir, "session-state"), idleStopAfter: 100 * time.Millisecond,
			logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
			inspectProtection: func(context.Context) (bool, error) { return false, nil },
		})
	}()
	var client *localapi.Client
	var err error
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		client, err = localapi.Dial(context.Background(), socket)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if client == nil {
		t.Fatalf("Local API did not start: %v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("idle daemon exited with UI client attached: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle daemon remained after last client left")
	}
}
