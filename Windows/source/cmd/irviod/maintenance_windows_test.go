// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/awgidentity"
	"github.com/Irviodotnet/Irvio/internal/identity"
	"github.com/Irviodotnet/Irvio/internal/peerstore"
	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	"github.com/Irviodotnet/Irvio/internal/productprofile"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestMaintenanceCustomProgramDirectory(t *testing.T) {
	dir, err := winfile.OpenDirectory(filepath.Join(t.TempDir(), "state"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	fallback := `C:\Program Files\IRVIO`
	if got, err := maintenanceProgramDirectory(dir, fallback); err != nil || got != fallback {
		t.Fatal(got, err)
	}
	for _, tc := range []struct {
		path  string
		valid bool
	}{
		{`D:\Apps\IRVIO`, true}, {`C:\Program Files\IRVIO Custom`, true},
		{`\\server\share\IRVIO`, false}, {`C:\`, false}, {`relative`, false},
		{`C:\Apps\..\IRVIO`, false}, {`C:\Apps\IRVIO" --extra`, false},
	} {
		raw, _ := json.Marshal(map[string]any{"schema": 1, "role": "desktop", "program_directory": tc.path})
		if err := dir.WriteAtomic("installation.json", append([]byte{0xef, 0xbb, 0xbf}, raw...), true); err != nil {
			t.Fatal(err)
		}
		got, err := maintenanceProgramDirectory(dir, fallback)
		if (err == nil) != tc.valid || (err == nil && got != tc.path) {
			t.Fatalf("path=%q got=%q err=%v", tc.path, got, err)
		}
	}
}

func TestMaintenanceServerCommandOwnership(t *testing.T) {
	dir, err := winfile.OpenDirectory(filepath.Join(t.TempDir(), "state"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	key, _, err := libcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := productbootstrap.FromMultiaddrs([]string{"/ip4/8.8.8.8/tcp/4000/p2p/" + id.String()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	code, err := productbootstrap.Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(code))
	base := `"C:\Program Files\IRVIO\daemon\irviod.exe"`
	newRecord := func() map[string]any {
		return map[string]any{
			"schema": 1, "role": "server", "manifest_sha256": strings.Repeat("a", 64),
			"bootstrap_sha256": hex.EncodeToString(digest[:]), "development_unsigned": true,
		}
	}
	write := func(t *testing.T, name string, data []byte) {
		t.Helper()
		if err := dir.WriteAtomic(name, data, true); err != nil {
			t.Fatal(err)
		}
	}
	set := func(t *testing.T, record map[string]any, bootstrap string) {
		t.Helper()
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		write(t, "installation.json", raw)
		write(t, "server-bootstrap.txt", []byte(bootstrap))
	}
	set(t, newRecord(), code)
	command, err := maintenanceServerCommand(dir, base)
	if err != nil || command != base+" --role server --bootstrap "+code {
		t.Fatal("canonical command changed", err)
	}
	cfg := mgr.Config{BinaryPathName: command, ServiceStartName: "LocalSystem", ServiceType: windows.SERVICE_WIN32_OWN_PROCESS, StartType: mgr.StartDisabled}
	if err := validateMaintenanceService(cfg, svc.Status{State: svc.Stopped}, command); err != nil {
		t.Fatal(err)
	}
	cfg.BinaryPathName += " --country XX"
	if validateMaintenanceService(cfg, svc.Status{State: svc.Stopped}, command) == nil {
		t.Fatal("server command drift accepted")
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"desktop", func(r map[string]any) { r["role"] = "desktop" }},
		{"schema", func(r map[string]any) { r["schema"] = 2 }},
		{"digest", func(r map[string]any) { r["bootstrap_sha256"] = strings.Repeat("b", 64) }},
		{"manifest", func(r map[string]any) { r["manifest_sha256"] = "bad" }},
		{"missing-mode", func(r map[string]any) { delete(r, "development_unsigned") }},
		{"unknown", func(r map[string]any) { r["desktop_sid"] = "S-1-5-18" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRecord()
			tc.mutate(r)
			set(t, r, code)
			if _, err := maintenanceServerCommand(dir, base); err == nil {
				t.Fatal("unsafe record accepted")
			}
		})
	}
	for _, bad := range []string{code + " ", code + " --country XX", "irvio-bootstrap:v1:QUJD", strings.Repeat("x", productbootstrap.MaxBundleCodeBytes+1)} {
		r := newRecord()
		h := sha256.Sum256([]byte(bad))
		r["bootstrap_sha256"] = hex.EncodeToString(h[:])
		set(t, r, bad)
		if _, err := maintenanceServerCommand(dir, base); err == nil {
			t.Fatal("invalid/noncanonical bundle accepted")
		}
	}
	set(t, newRecord(), code)
	write(t, "installation.json", []byte(strings.Repeat(" ", 16*1024+1)))
	if _, err := maintenanceServerCommand(dir, base); err == nil {
		t.Fatal("oversized ownership accepted")
	}
}

func TestWindowsNativeServerMaintenanceInspection(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_SERVER_MAINTENANCE") != "1" {
		t.Skip("explicit installed server maintenance gate required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := inspectProductMaintenance(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsNativeMaintenanceProfileFixture(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_MAINTENANCE_PROFILE_FIXTURE") != "1" {
		t.Skip("explicit disposable installed-profile fixture gate required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := inspectProductMaintenance(ctx, func(paths productruntime.WindowsPaths) error {
		profile, err := productprofile.Open(paths.Profile)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(profile.Path())
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() != productprofile.MarkerFilename {
				t.Fatalf("refuse to populate a nonempty disposable profile: %s", entry.Name())
			}
		}
		if _, err := identity.LoadOrCreate(profile.Path()); err != nil {
			return err
		}
		if _, err := awgidentity.LoadOrCreate(profile.Path()); err != nil {
			return err
		}
		db, err := peerstore.Open(ctx, filepath.Join(profile.Path(), "hydra.db"))
		if err != nil {
			return err
		}
		return db.Close()
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("disposable native identity/AWG/database fixture created; no network participation")
}

func TestMaintenanceRequiresFencedOwnedService(t *testing.T) {
	command := `"C:\Program Files\IRVIO\daemon\irviod.exe"`
	base := mgr.Config{BinaryPathName: command, ServiceStartName: "LocalSystem", ServiceType: windows.SERVICE_WIN32_OWN_PROCESS, StartType: mgr.StartDisabled}
	status := svc.Status{State: svc.Stopped}
	if err := validateMaintenanceService(base, status, command); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*mgr.Config, *svc.Status){
		func(c *mgr.Config, s *svc.Status) { c.BinaryPathName += " --role server" },
		func(c *mgr.Config, s *svc.Status) { c.ServiceStartName = "LocalService" },
		func(c *mgr.Config, s *svc.Status) { c.ServiceType = windows.SERVICE_WIN32_SHARE_PROCESS },
		func(c *mgr.Config, s *svc.Status) { c.StartType = mgr.StartManual },
		func(c *mgr.Config, s *svc.Status) { s.State = svc.Running },
		func(c *mgr.Config, s *svc.Status) { s.ProcessId = 42 },
		func(c *mgr.Config, s *svc.Status) { s.Win32ExitCode = 1 },
	} {
		c, s := base, status
		mutate(&c, &s)
		if err := validateMaintenanceService(c, s, command); err == nil {
			t.Fatal("unsafe service accepted", c, s)
		}
	}
}
