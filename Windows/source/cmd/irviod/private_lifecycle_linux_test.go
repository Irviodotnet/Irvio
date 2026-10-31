// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// This checks an empty runtime directory, not kernel reboot or boot-recovery cleanup.
func TestPrivateDesktopEmptyRuntimeDoesNotRestorePersistentTrust(t *testing.T) {
	for _, mode := range []string{"client", "hosting", "combined"} {
		t.Run(mode, func(t *testing.T) {
			previous := privateLifecycleController(t)
			previous.intent.Path = filepath.Join(t.TempDir(), "session-state")
			if err := os.Chmod(filepath.Dir(previous.intent.Path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := previous.openPrivateStoreLocked(); err != nil {
				t.Fatal(err)
			}
			target := privateLifecyclePeer(t)
			state, err := previous.privateStore.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if err := previous.privateStore.SaveTarget(privateaccess.Target{PeerID: target.String(), Name: "Friend", Locator: state.Locator}); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			invite, err := previous.privateStore.CreateInvitation(now, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if err := previous.privateStore.Reserve(invite.ID, invite.Secret, privateLifecyclePeer(t), now); err != nil {
				t.Fatal(err)
			}
			if err := previous.privateStore.Approve(invite.ID, "Member", now); err != nil {
				t.Fatal(err)
			}
			before, err := previous.privateStore.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			wanted := target
			if mode == "hosting" {
				wanted = ""
			}
			if err := previous.intent.WantPrivate(wanted, mode != "client"); err != nil {
				t.Fatal(err)
			}
			if err := previous.closePrivateStoreLocked(); err != nil {
				t.Fatal(err)
			}
			freshRuntime := t.TempDir()
			if err := os.Chmod(freshRuntime, 0700); err != nil {
				t.Fatal(err)
			}
			intentPath := filepath.Join(freshRuntime, "session-state")
			socket := filepath.Join(freshRuntime, "api", "control.sock")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var protectionCalls atomic.Int32
			protection := func(context.Context) error { protectionCalls.Add(1); return nil }
			done := make(chan error, 1)
			go func() {
				done <- runProductWithContext(ctx, productOptions{role: productruntime.Desktop,
					dataDir: previous.dataDir, apiSocket: socket, intentPath: intentPath,
					logger: previous.logger, inspectProtection: func(context.Context) (bool, error) { return false, nil },
					ensureProtection: protection, cleanupProtection: protection})
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(3 * time.Second):
					t.Error("idle daemon failed to stop")
				}
			})
			var api *localapi.Client
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				api, err = localapi.Dial(ctx, socket)
				if err == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if api == nil {
				t.Fatalf("Local API unavailable: %v", err)
			}
			defer api.Close()
			for sample := 0; sample < 3; sample++ {
				status, err := api.GetStatus(ctx)
				if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED ||
					status.PrivateHostingWanted || status.PrivateTargetPeerId != "" || status.CountryCode != "" || status.ContributionEnabled {
					t.Fatalf("persistent trust enabled network intent: status=%v err=%v", status, err)
				}
				time.Sleep(50 * time.Millisecond)
			}
			if protectionCalls.Load() != 0 {
				t.Fatal("idle startup altered protection")
			}
			if _, present, err := (productintent.Store{Path: intentPath}).Read(); err != nil || present {
				t.Fatalf("idle startup created intent: present=%t err=%v", present, err)
			}
			if _, present, err := previous.intent.Read(); err != nil || !present {
				t.Fatalf("old runtime fixture was modified: present=%t err=%v", present, err)
			}
			hostID, err := peer.Decode(before.HostPeerID)
			if err != nil {
				t.Fatal(err)
			}
			store, err := privateaccess.Open(previous.dataDir, hostID)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			after, err := store.Snapshot()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("idle startup changed persistent private trust")
			}
		})
	}
}

func privateLifecyclePeer(t *testing.T) peer.ID {
	t.Helper()
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func privateLifecycleController(t *testing.T) *desktopController {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return &desktopController{parent: context.Background(), dataDir: dir,
		intent:            productintent.Store{Path: filepath.Join(dir, "session-state")},
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		ensureProtection:  func(context.Context) error { return nil },
		cleanupProtection: func(context.Context) error { return nil }}
}

func TestPrivateDesktopRestoreAndFullLeave(t *testing.T) {
	id := privateLifecyclePeer(t)
	for _, intent := range []privateProductIntent{{Target: id}, {Hosting: true}, {Target: id, Hosting: true}} {
		d := privateLifecycleController(t)
		protected, cleaned := 0, 0
		d.ensureProtection = func(context.Context) error { protected++; return nil }
		d.cleanupProtection = func(context.Context) error { cleaned++; return nil }
		if err := d.intent.WantPrivate(intent.Target, intent.Hosting); err != nil {
			t.Fatal(err)
		}
		state, present, err := d.intent.Read()
		if err != nil || !present {
			t.Fatalf("read: %v", err)
		}
		if err := d.restoreIntent(context.Background(), state); err != nil {
			t.Fatal(err)
		}
		if d.desiredPrivate != intent || d.desiredCountry != "" || d.idleWithoutIntent() {
			t.Fatal("private intent lost or treated as idle")
		}
		if d.Status().PrivateHostingWanted != intent.Hosting {
			t.Fatal("status lost private hosting intent")
		}
		if intent.Target != "" {
			status := d.Status()
			if protected != 1 || !d.recoveryPending || status.State != irvioruntime.StateExitReselection ||
				status.Target.Kind != exitselector.TargetPrivatePeer || status.Target.PeerID != id || status.ProtectedMode != killswitch.StateNoExit {
				t.Fatalf("wrong protected recovery: %+v", status)
			}
		} else if protected != 0 || d.recoveryPending {
			t.Fatal("hosting-only blocked ordinary WAN")
		}
		if intent.Target == "" && d.Status().State == irvioruntime.StateIdle {
			t.Fatal("hosting intent exposed as disconnected")
		}
		if err := d.Disconnect(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !d.idleWithoutIntent() || d.desiredPrivate.active() {
			t.Fatal("full leave retained intent")
		}
		if status := d.Status(); status.PrivateHostingWanted || status.Target.PeerID != "" {
			t.Fatal("status retained private intent after full leave")
		}
		if (cleaned == 1) != (intent.Target != "") {
			t.Fatal("incorrect protection cleanup")
		}
		if _, present, err := d.intent.Read(); err != nil || present {
			t.Fatalf("intent survived: %v", err)
		}
	}
}

func TestPrivateHostingProfileDeletionClearsRecoveryIntent(t *testing.T) {
	d := privateLifecycleController(t)
	d.desiredPrivate = privateProductIntent{Hosting: true}
	if err := d.intent.WantPrivate("", true); err != nil {
		t.Fatal(err)
	}
	reset := newProfileResetController(d)
	if err := reset.PrepareDelete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, present, err := d.intent.Read(); err != nil || present || d.desiredPrivate.active() {
		t.Fatalf("profile reset retained hosting: %t %v", present, err)
	}
}

func TestPrivateDesktopCannotSwitchPendingIntent(t *testing.T) {
	d := privateLifecycleController(t)
	id := privateLifecyclePeer(t)
	d.desiredPrivate = privateProductIntent{Target: id, Hosting: true}
	if err := d.Connect(context.Background(), "NL"); !errors.Is(err, irvioruntime.ErrInvalidState) {
		t.Fatal(err)
	}
	if err := d.ConnectPrivate(context.Background(), privateLifecyclePeer(t), true); !errors.Is(err, irvioruntime.ErrInvalidState) {
		t.Fatal(err)
	}
	d.desiredPrivate = privateProductIntent{}
	d.desiredCountry = "NL"
	if err := d.ConnectPrivate(context.Background(), id, false); !errors.Is(err, irvioruntime.ErrInvalidState) {
		t.Fatal(err)
	}
	d.desiredCountry = ""
	d.recoveryPending = true
	if err := d.ConnectPrivate(context.Background(), "", true); !errors.Is(err, irvioruntime.ErrInvalidState) {
		t.Fatal(err)
	}
}

func TestPrivateDesktopStoreOwnershipAndUnsavedTarget(t *testing.T) {
	d := privateLifecycleController(t)
	if err := d.openPrivateStoreLocked(); err != nil {
		t.Fatal(err)
	}
	store := d.privateStore
	if err := d.openPrivateStoreLocked(); err != nil || d.privateStore != store {
		t.Fatal("store not reused")
	}
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	host, err := peer.Decode(state.HostPeerID)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := privateaccess.Open(d.dataDir, host); err == nil {
		other.Close()
		t.Fatal("second writer admitted")
	}
	if err := d.ConnectPrivate(context.Background(), privateLifecyclePeer(t), false); err == nil {
		t.Fatal("unsaved target admitted")
	}
	if d.desiredPrivate.active() || d.recoveryPending {
		t.Fatal("invalid target changed intent")
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Snapshot(); err == nil {
		t.Fatal("store survived controller close")
	}
	reopened, err := privateaccess.Open(d.dataDir, host)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
}

func TestPrivateDisconnectFailureDoesNotRestoreIntent(t *testing.T) {
	d := privateLifecycleController(t)
	d.desiredPrivate = privateProductIntent{Target: privateLifecyclePeer(t), Hosting: true}
	d.recoveryPending = true
	d.cleanupProtection = func(context.Context) error { return errors.New("injected cleanup failure") }
	if err := d.Disconnect(context.Background()); err == nil {
		t.Fatal("lost cleanup error")
	}
	state, present, err := d.intent.Read()
	if err != nil || !present || !state.TeardownPending || d.desiredPrivate.active() || !d.recoveryPending || d.idleWithoutIntent() {
		t.Fatalf("unsafe failed teardown: %+v %v", state, err)
	}
	if d.Status().State != irvioruntime.StateDisconnecting {
		t.Fatal("unfinished teardown exposed as idle")
	}
	if err := d.ConnectPrivate(context.Background(), privateLifecyclePeer(t), false); !errors.Is(err, irvioruntime.ErrInvalidState) {
		t.Fatal("new connection admitted during unfinished teardown")
	}
	d.cleanupProtection = func(context.Context) error { return nil }
	if err := d.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !d.idleWithoutIntent() {
		t.Fatal("cleanup retry failed to leave")
	}
}
