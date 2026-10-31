// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/socketpolicy"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"golang.org/x/sys/windows"
)

func daemonNativeOwner() wfp.Config {
	return wfp.Config{
		Provider: windows.GUID{Data1: 0xd6e22314, Data2: 0xe698, Data3: 0x4ed9, Data4: [8]byte{0x90, 0x35, 0x83, 0x15, 0x3a, 0x1a, 0x66, 0x17}},
		Sublayer: windows.GUID{Data1: 0xab6e4d92, Data2: 0x1176, Data3: 0x4edf, Data4: [8]byte{0x9a, 0x5c, 0x76, 0xde, 0x38, 0x2e, 0x1c, 0x53}},
		Name:     "IRVIO W05 full protection lab", Marker: "irvio-w05-protection-v1", ServiceName: "IRVIO-W05-Owner", Weight: 49200,
	}
}

func TestWindowsNativeDaemonRetainsProductProtection(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_DAEMON_TEST") != "1" {
		t.Skip("requires isolated Windows VM with WFP recovery fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	probe := os.Getenv("IRVIO_WINDOWS_DAEMON_WAN_PROBE")
	endpoint, err := netip.ParseAddrPort(probe)
	if err != nil || endpoint.Addr().IsLoopback() || endpoint.Port() == 0 {
		t.Fatal("a reachable literal non-loopback lab TCP probe is required")
	}
	probeWAN := func(want bool) {
		t.Helper()
		conn, err := net.DialTimeout("tcp", probe, time.Second)
		if conn != nil {
			conn.Close()
		}
		if (err == nil) != want {
			t.Fatalf("WAN reachable=%v want=%v error=%v", err == nil, want, err)
		}
	}
	probeWAN(true)
	store, err := wfp.NewStore(daemonNativeOwner())
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Inspect(ctx)
	if err != nil || before.Installed {
		t.Fatal("preexisting owned protection", err)
	}
	t.Cleanup(func() {
		if err := store.Remove(context.Background()); err != nil {
			t.Error(err)
		}
	})
	root := t.TempDir()
	config := DaemonConfig{DataDir: filepath.Join(root, "profile"), ListenPort: 0, DHTMode: "invalid", ProtectedMode: true,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Platform: DaemonPlatformConfig{JournalDirectory: filepath.Join(root, "journals"), ProtectionOwner: daemonNativeOwner(), RetainProtection: true},
		M12:      M12Config{Enabled: true}}
	for iteration := 0; iteration < 3; iteration++ {
		if iteration > 0 {
			config.DHTMode = "server"
		}
		d, err := NewDaemon(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		err = d.Start(ctx)
		if (err != nil) != (iteration == 0) {
			t.Fatalf("start iteration %d: %v", iteration, err)
		}
		if iteration > 0 {
			if !d.protection.Snapshot().Adopted {
				t.Fatal("same product intent not adopted")
			}
			if err := RecoverWindowsDaemonResources(ctx, config.Platform.JournalDirectory); err == nil {
				t.Fatal("live network recovered")
			}
		}
		if err := d.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if err := RecoverWindowsDaemonResources(ctx, config.Platform.JournalDirectory); err != nil {
			t.Fatal(err)
		}
		policy := killswitch.Policy{TUNName: "irvio0", BypassMark: socketpolicy.Underlay}
		backend, err := killswitch.NewWindowsBackend(killswitch.WindowsBackendConfig{Owner: daemonNativeOwner(), Policy: policy})
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := backend.Inspect(ctx, policy)
		if err != nil || !snapshot.Installed || !snapshot.Complete {
			t.Fatalf("deny-only product protection not retained: %+v %v", snapshot, err)
		}
		probeWAN(false)
	}
	if err := store.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	probeWAN(true)
}

func TestWindowsNativeDaemonLifecycle(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_DAEMON_TEST") != "1" {
		t.Skip("requires isolated Windows VM with WFP recovery fixture")
	}
	for _, protected := range []bool{false, true} {
		t.Run(fmt.Sprint(protected), func(t *testing.T) {
			root := t.TempDir()
			config := DaemonConfig{DataDir: filepath.Join(root, "profile"), ListenPort: 0, DHTMode: "server", PublicSTUN: true, ProtectedMode: protected,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OperationTimeout: 10 * time.Second,
				Platform: DaemonPlatformConfig{JournalDirectory: filepath.Join(root, "journals"), ProtectionOwner: daemonNativeOwner()}, M12: M12Config{Enabled: true}}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			var identity string
			for iteration := 0; iteration < 2; iteration++ {
				d, err := NewDaemon(config)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := d.Close(context.Background()); err != nil {
						t.Error(err)
					}
				})
				if err := d.Start(ctx); err != nil {
					t.Fatal("start", err)
				}
				if d.Node() == nil || len(d.Node().ListenAddresses()) == 0 || d.AWGKeyEpoch() != 1 || d.Status().NetworkGeneration == 0 {
					t.Fatal("incomplete native daemon startup")
				}
				if identity == "" {
					identity = d.Node().PeerID().String()
				} else if identity != d.Node().PeerID().String() {
					t.Fatal("identity changed across restart")
				}
				if got := d.Status().ProtectedMode.Protected(); got != protected {
					t.Fatal("wrong protection mode", got)
				}
				if d.m12 == nil || d.platform.dialer == nil || d.stunServer == nil {
					t.Fatal("missing discovery/STUN composition")
				}
				competitor, err := NewDaemon(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := competitor.Start(ctx); err == nil {
					competitor.Close(ctx)
					t.Fatal("second daemon stole runtime ownership")
				}
				if err := competitor.Close(ctx); err != nil {
					t.Fatal(err)
				}
				if protected {
					if err := d.protection.Reconcile(ctx, d.protection.Snapshot().Generation); err != nil {
						t.Fatal("competing startup changed protection", err)
					}
				}
				state := d.network.Current()
				state.Generation++
				if err := d.reconcileWindowsControl(ctx, state); err != nil {
					t.Fatal("migrate listeners", err)
				}
				if len(d.Node().ListenAddresses()) == 0 || len(d.platform.permissions.entries) == 0 || d.platform.state.Generation != state.Generation {
					t.Fatal("migration lost listeners")
				}
				// Leave an observation window for the independent mini-PC STUN sender.
				select {
				case <-time.After(time.Second):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if protected {
					if err := d.Disconnect(ctx); err != nil {
						t.Fatal("disconnect", err)
					}
					if d.Status().ProtectedMode != killswitch.StateVPNOff || d.stunServer != nil || len(d.Node().ListenAddresses()) != 0 {
						t.Fatal("disconnect left network participation")
					}
				}
				if err := d.Close(ctx); err != nil {
					t.Fatal("close", err)
				}
				if len(d.platform.permissions.entries) != 0 || len(d.platform.sockets.entries) != 0 || d.platform.stunConn != nil {
					t.Fatal("native owners retained after close")
				}
				if err := d.Close(ctx); err != nil {
					t.Fatal("repeated close", err)
				}
			}
		})
	}
}
