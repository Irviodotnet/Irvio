// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"golang.org/x/sys/windows"
)

// The harness installs the real zero-argument daemon as SYSTEM before running this ordinary-user client.
func TestWindowsNativeProductIdle(t *testing.T) {
	mode := os.Getenv("IRVIO_WINDOWS_PRODUCT_IDLE")
	if mode != "status" && mode != "shutdown" && mode != "reset" {
		t.Skip("disposable product SCM installation required")
	}
	if os.Getenv("IRVIO_WINDOWS_PRODUCT_UNPRIVILEGED") == "1" {
		token := windows.GetCurrentProcessToken()
		user, err := token.GetTokenUser()
		if err != nil {
			t.Fatal(err)
		}
		if token.IsElevated() || user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
			t.Fatal("ordinary non-elevated account required")
		}
		t.Log("non-elevated local user verified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := localapi.Dial(ctx, localapi.DefaultSocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	status, err := client.GetStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED || status.ContributionEnabled || status.PrivateHostingWanted || status.CountryCode != "" {
		t.Fatalf("not idle: %v", status)
	}
	bootstrap, err := client.GetBootstrapState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.Phase != localv1.BootstrapPhase_BOOTSTRAP_PHASE_EMPTY || bootstrap.ConfiguredCount != 0 {
		t.Fatalf("unexpected bootstrap: %v", bootstrap)
	}
	if err := client.Disconnect(ctx); err != nil {
		t.Fatal("idle disconnect:", err)
	}
	if mode == "shutdown" {
		if err := client.Shutdown(ctx); err != nil {
			t.Fatal("intentional shutdown:", err)
		}
	}
	if mode == "reset" {
		if err := client.DeleteProfile(ctx); err != nil {
			t.Fatal("profile deletion:", err)
		}
		for {
			if ctx.Err() != nil {
				t.Fatal("old API did not close after reset")
			}
			if _, err := client.GetStatus(ctx); err != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = client.Close()
		for {
			if ctx.Err() != nil {
				t.Fatal("product did not reopen after reset:", ctx.Err())
			}
			next, err := localapi.Dial(ctx, localapi.DefaultSocketPath)
			if err == nil {
				status, err := next.GetStatus(ctx)
				if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
					t.Fatalf("post-reset status=%v err=%v", status, err)
				}
				bootstrap, err := next.GetBootstrapState(ctx)
				if err != nil || bootstrap.Phase != localv1.BootstrapPhase_BOOTSTRAP_PHASE_EMPTY {
					t.Fatalf("post-reset bootstrap=%v err=%v", bootstrap, err)
				}
				_ = next.Close()
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	t.Logf("real product pipe: IDLE, no contribution, bootstrap EMPTY, disconnect accepted; mode=%s", mode)
}

func TestWindowsNativeProductIdleCleanup(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_PRODUCT_IDLE_CLEANUP") != "1" {
		t.Skip("stopped disposable product installation required")
	}
	options, err := resolveProductOptions(productOptions{role: productruntime.Desktop})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := acquireProductPlatform(options)
	if err != nil {
		t.Fatal("product still owns runtime:", err)
	}
	defer owner.Close()
	_, present, err := (productintent.Store{Path: options.intentPath}).Read()
	if err != nil || present {
		t.Fatalf("intent residue: present=%v err=%v", present, err)
	}
	backend, err := windowsProductBackend(options.platform)
	if err != nil {
		t.Fatal(err)
	}
	state, err := backend.Inspect(context.Background(), productProtectionPolicy())
	if err != nil || state.Installed {
		t.Fatalf("unexpected protection residue: %+v err=%v", state, err)
	}
	if err := irvioruntime.CheckWindowsDaemonJournalsInactive(context.Background(), productDaemonPlatform(options.platform).JournalDirectory); err != nil {
		t.Fatal("unresolved network journal:", err)
	}
}
