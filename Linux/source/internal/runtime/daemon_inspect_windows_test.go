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

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

func TestWindowsDaemonInactiveInspection(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "network")
	if err := CheckWindowsDaemonJournalsInactive(ctx, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created absent directory", err)
	}
	if err := CheckWindowsDaemonJournalsInactive(nil, path); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	if err := CheckWindowsDaemonJournalsInactive(ctx, "relative"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := CheckWindowsDaemonJournalsInactive(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	owner, err := openWindowsDaemonJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckWindowsDaemonJournalsInactive(ctx, path); err == nil {
		t.Fatal("accepted live daemon")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CheckWindowsDaemonJournalsInactive(ctx, path); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsDaemonInactiveInspectionRejectsNestedState(t *testing.T) {
	for _, branch := range []string{"dataplane/dns", "dataplane/routes", "dataplane/upstream/dns", "dataplane/upstream/routes", "dataplane/control", "sockets/socket-00", "sockets/socket-11"} {
		t.Run(branch, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "network")
			owner, err := openWindowsDaemonJournal(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			leaf := filepath.Join(path, filepath.FromSlash(branch))
			var makePrivate func(string)
			makePrivate = func(p string) {
				if p == path {
					return
				}
				makePrivate(filepath.Dir(p))
				d, err := winfile.OpenDirectory(p, true)
				if err != nil {
					t.Fatal(err)
				}
				defer d.Close()
				for _, name := range []string{"socket-permissions.lock", "dns.lock", "routes.lock", "control-firewall.lock"} {
					f, err := d.OpenFile(name, true, true)
					if err != nil {
						t.Fatal(err)
					}
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			makePrivate(leaf)
			if err := CheckWindowsDaemonJournalsInactive(context.Background(), path); err != nil {
				t.Fatal("empty fixture:", err)
			}
			d, err := winfile.OpenDirectory(leaf, false)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			for _, name := range []string{"dns-state.json", "routes-state.json", "control-firewall.json"} {
				if err := d.WriteAtomic(name, []byte("corrupt"), false); err != nil {
					t.Fatal(err)
				}
			}
			if err := CheckWindowsDaemonJournalsInactive(context.Background(), path); err == nil {
				t.Fatal("nested corrupt state ignored")
			}
		})
	}
}
