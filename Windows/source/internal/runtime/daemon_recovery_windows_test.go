// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/winfile"
)

type retainedInstallationFixture struct {
	removed  bool
	snapshot killswitch.KernelSnapshot
}

func (i *retainedInstallationFixture) Snapshot(context.Context) (killswitch.KernelSnapshot, error) {
	return i.snapshot, nil
}
func (i *retainedInstallationFixture) Reconcile(context.Context) error { return nil }
func (i *retainedInstallationFixture) Remove(context.Context) error    { i.removed = true; return nil }

func TestWindowsRetainedAttemptNeverRemovesProtection(t *testing.T) {
	for _, failure := range []bool{false, true} {
		base := &retainedInstallationFixture{snapshot: killswitch.KernelSnapshot{Installed: true, Complete: true}}
		revoked := false
		i := windowsRetainedInstallation{Installation: base, revoke: func(context.Context) error {
			revoked = true
			if failure {
				return errors.New("revoke failed")
			}
			return nil
		}}
		err := i.Remove(context.Background())
		if (err != nil) != failure || !revoked || base.removed {
			t.Fatalf("failure=%v revoked=%v removed=%v err=%v", failure, revoked, base.removed, err)
		}
		base.snapshot.Complete = false
		if err := i.Remove(context.Background()); err == nil {
			t.Fatal("incomplete persistent protection accepted")
		}
	}
}

func TestWindowsDaemonRecoveryRefusesLiveOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime")
	owner, err := openWindowsDaemonJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := RecoverWindowsDaemonResources(context.Background(), path); err == nil {
		t.Fatal("recovery accepted a live daemon lock")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RecoverWindowsDaemonResources(context.Background(), path); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsDaemonRecoveryMissingDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	if err := RecoverWindowsDaemonResources(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created absent tree", err)
	}
	if err := RecoverWindowsDaemonResources(nil, path); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	if err := RecoverWindowsDaemonResources(context.Background(), "relative"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
}

func TestWindowsDaemonRecoveryCorruptJournalIsPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime")
	for _, child := range []string{"", "dataplane", filepath.Join("dataplane", "upstream"), filepath.Join("dataplane", "upstream", "dns")} {
		d, err := winfile.OpenDirectory(filepath.Join(path, child), true)
		if err != nil {
			t.Fatal(err)
		}
		if child == filepath.Join("dataplane", "upstream", "dns") {
			if err := d.WriteAtomic("dns-state.json", []byte("not a journal"), false); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := RecoverWindowsDaemonResources(context.Background(), path); err == nil {
		t.Fatal("corrupt DNS journal ignored")
	}
	value, err := os.ReadFile(filepath.Join(path, "dataplane", "upstream", "dns", "dns-state.json"))
	if err != nil || string(value) != "not a journal" {
		t.Fatal("evidence changed", err)
	}
}
