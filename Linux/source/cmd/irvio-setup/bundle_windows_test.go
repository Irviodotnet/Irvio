// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

type bundleEntry struct {
	name string
	mode fs.FileMode
}

func fixtureBundle(t *testing.T, entries []bundleEntry) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		file, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("fixture")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("fixture"))
	return buffer.Bytes(), hex.EncodeToString(hash[:])
}

func TestBundleValidation(t *testing.T) {
	base := fixtureInstallerEntries()
	data, digest := fixtureBundle(t, base)
	if _, err := readBundle(data, digest); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "not-a-hash", "0000000000000000000000000000000000000000000000000000000000000000"} {
		if _, err := readBundle(data, bad); err == nil {
			t.Fatal("accepted bad manifest digest", bad)
		}
	}
	for _, name := range []string{"../outside", "payload/ui/../../outside", "payload/ui/a:stream", "payload/ui/foo. /a", "payload/ui/NUL.txt", "payload/ui/COM1", "payload/ui/LPT9.dll", "payload/ui//double", "payload/ui/./dot", "payload\\ui\\bad", "scripts/foreign.ps1", "payload/payload-manifest.json"} {
		t.Run(name, func(t *testing.T) {
			entries := append(append([]bundleEntry{}, base...), bundleEntry{name, 0600})
			bad, digest := fixtureBundle(t, entries)
			if _, err := readBundle(bad, digest); err == nil {
				t.Fatal("accepted unsafe/duplicate name", name)
			}
		})
	}
	for _, mode := range []fs.FileMode{fs.ModeSymlink | 0600, fs.ModeDir | 0700} {
		bad, digest := fixtureBundle(t, append(append([]bundleEntry{}, base...), bundleEntry{"payload/ui/redirect", mode}))
		if _, err := readBundle(bad, digest); err == nil {
			t.Fatal("accepted non-regular entry", mode)
		}
	}
	missing, digest := fixtureBundle(t, base[1:])
	if _, err := readBundle(missing, digest); err == nil {
		t.Fatal("accepted missing manifest")
	}
	for i := 1; i < len(base); i++ {
		entries := append(append([]bundleEntry{}, base[:i]...), base[i+1:]...)
		missing, digest := fixtureBundle(t, entries)
		if _, err := readBundle(missing, digest); err == nil {
			t.Fatal("accepted missing backend", base[i].name)
		}
	}
}

func fixtureInstallerEntries() []bundleEntry {
	entries := []bundleEntry{{"payload/payload-manifest.json", 0600}}
	for _, name := range installerScripts {
		entries = append(entries, bundleEntry{"scripts/" + name + ".ps1", 0600})
	}
	return entries
}

func TestProtectedBundleExtraction(t *testing.T) {
	entries := append(fixtureInstallerEntries(), bundleEntry{"payload/ui/data/flutter_assets/asset.txt", 0600})
	data, digest := fixtureBundle(t, entries)
	zr, err := readBundle(data, digest)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "private")
	dir, err := winfile.OpenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := extractBundle(zr, root); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		path := filepath.Join(root, filepath.FromSlash(entry.name))
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "fixture" {
			t.Fatalf("%s: %q %v", entry.name, got, err)
		}
		parent, err := winfile.OpenDirectory(filepath.Dir(path), false)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parent.OpenImmutableFile(filepath.Base(path))
		if err != nil {
			_ = parent.Close()
			t.Fatal(err)
		}
		_ = file.Close()
		_ = parent.Close()
	}
}

func TestSetupIdentityBeforeElevation(t *testing.T) {
	const sid = "S-1-5-21-101-202-303-1001"
	actual, child, err := setupSID(nil, false, sid)
	if err != nil || child || actual != sid {
		t.Fatalf("original desktop identity: %q %v %v", actual, child, err)
	}
	if _, _, err := setupSID([]string{"--internal-install-for", sid}, false, sid); err == nil {
		t.Fatal("non-elevated caller chose arbitrary target")
	}
	actual, child, err = setupSID([]string{"--internal-install-for", sid}, true, "S-1-5-18")
	if err != nil || !child || actual != sid {
		t.Fatalf("elevated target changed: %q %v %v", actual, child, err)
	}
	for _, bad := range []string{"S-1-5-18", "S-1-5-32-544", sid + " --overwrite", ""} {
		if _, _, err := setupSID([]string{"--internal-install-for", bad}, true, sid); err == nil {
			t.Fatal("unsafe target", bad)
		}
	}
	old := development
	defer func() { development = old }()
	development = "false"
	if _, _, err := setupSID([]string{"--internal-install-for", sid, "--development-unattended"}, true, sid); err == nil {
		t.Fatal("release accepted lab-only unattended flag")
	}
	development = "true"
	if _, _, err := setupSID([]string{"--internal-install-for", sid, "--development-unattended"}, true, sid); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallIntentBeforeElevation(t *testing.T) {
	const sid = "S-1-5-21-101-202-303-1001"
	old := development
	defer func() { development = old }()
	development = "false"
	intent, err := parseSetupIntent([]string{"--uninstall"}, false, sid)
	if err != nil || intent.SID != sid || !intent.Uninstall || intent.Child || intent.RemoveProfile {
		t.Fatalf("unsafe default uninstall intent: %+v %v", intent, err)
	}
	for _, choice := range []string{"keep-profile", "remove-profile"} {
		args := []string{"--internal-uninstall-for", sid, choice}
		if _, err := parseSetupIntent(args, false, sid); err == nil {
			t.Fatal("non-elevated caller supplied an internal uninstall intent")
		}
		intent, err := parseSetupIntent(args, true, "S-1-5-18")
		if err != nil || intent.SID != sid || !intent.Child || !intent.Uninstall || intent.RemoveProfile != (choice == "remove-profile") {
			t.Fatalf("UAC changed intent: %+v %v", intent, err)
		}
		if got := intent.elevationArguments(); got != "--internal-uninstall-for "+sid+" "+choice {
			t.Fatal("UAC arguments changed", got)
		}
		if _, err := parseSetupIntent(append(args, "--development-unattended"), true, sid); err == nil {
			t.Fatal("release accepted unattended uninstall")
		}
		development = "true"
		if _, err := parseSetupIntent(append(args, "--development-unattended"), true, sid); err != nil {
			t.Fatal(err)
		}
		development = "false"
	}
	for _, args := range [][]string{
		{"--uninstall", "remove-profile"},
		{"--internal-uninstall-for", sid},
		{"--internal-uninstall-for", "S-1-5-18", "keep-profile"},
		{"--internal-uninstall-for", sid, "remove-profile --overwrite"},
		{"--internal-uninstall-for", sid, ""},
		{"--internal-uninstall-for", sid, "keep-profile", "extra"},
	} {
		if _, err := parseSetupIntent(args, true, sid); err == nil {
			t.Fatal("accepted unsupported arguments", args)
		}
	}
	if _, err := parseSetupIntent([]string{"--uninstall"}, true, "S-1-5-18"); err == nil {
		t.Fatal("public uninstall did not require desktop identity")
	}
}

func TestServerUninstallIntent(t *testing.T) {
	old := development
	defer func() { development = old }()
	development = "false"
	intent, err := parseSetupIntent([]string{"--role", "server", "--uninstall"}, false, "")
	if err != nil || !intent.Server || !intent.Uninstall || intent.Child || intent.RemoveProfile || intent.SID != "" || intent.Bootstrap != "" {
		t.Fatalf("unsafe server uninstall default: %+v %v", intent, err)
	}
	for _, choice := range []string{"keep-profile", "remove-profile"} {
		args := []string{"--internal-uninstall-server", choice}
		if _, err := parseSetupIntent(args, false, ""); err == nil {
			t.Fatal("non-elevated internal server uninstall accepted")
		}
		intent, err := parseSetupIntent(args, true, "S-1-5-18")
		if err != nil || !intent.Child || !intent.Server || !intent.Uninstall || intent.RemoveProfile != (choice == "remove-profile") || intent.SID != "" || intent.Bootstrap != "" {
			t.Fatalf("UAC changed server uninstall intent: %+v %v", intent, err)
		}
		if intent.elevationArguments() != "--internal-uninstall-server "+choice {
			t.Fatal("server uninstall was redirected to installation or lost profile choice")
		}
		unattended := append(args, "--development-unattended")
		if _, err := parseSetupIntent(unattended, true, ""); err == nil {
			t.Fatal("release accepted unattended server uninstall")
		}
		development = "true"
		if _, err := parseSetupIntent(unattended, true, ""); err != nil {
			t.Fatal(err)
		}
		development = "false"
	}
	for _, args := range [][]string{
		{"--role", "server", "--uninstall", "remove-profile"},
		{"--role", "server", "--uninstall", "--bootstrap", "code"},
		{"--internal-uninstall-server"},
		{"--internal-uninstall-server", ""},
		{"--internal-uninstall-server", "remove-profile --overwrite"},
		{"--internal-uninstall-server", "keep-profile", "extra"},
	} {
		if _, err := parseSetupIntent(args, true, ""); err == nil {
			t.Fatal("accepted unsupported server uninstall arguments", args)
		}
	}
}

func TestServerSetupIntent(t *testing.T) {
	old := development
	defer func() { development = old }()
	development = "false"
	var addresses []string
	for _, ip := range []string{"8.8.8.8", "1.1.1.1"} {
		key, _, err := libcrypto.GenerateEd25519Key(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		id, err := peer.IDFromPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		addresses = append(addresses, "/ip4/"+ip+"/tcp/4000/p2p/"+id.String())
	}
	bundle, err := productbootstrap.FromMultiaddrs(addresses, 1)
	if err != nil {
		t.Fatal(err)
	}
	code, err := productbootstrap.Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := parseSetupIntent([]string{"--role", "server", "--bootstrap", code}, false, "S-1-5-18")
	if err != nil || !intent.Server || intent.Child || intent.Uninstall || intent.SID != "" || intent.Bootstrap != code {
		t.Fatal("server intent changed", err)
	}
	if intent.elevationArguments() != "--internal-install-server "+code {
		t.Fatal("server elevation lost bootstrap bundle")
	}
	args := []string{"--internal-install-server", code}
	if _, err := parseSetupIntent(args, false, ""); err == nil {
		t.Fatal("non-elevated internal server intent accepted")
	}
	child, err := parseSetupIntent(args, true, "S-1-5-18")
	if err != nil || !child.Child || !child.Server || child.Bootstrap != code {
		t.Fatal("server elevation changed intent", err)
	}
	args = append(args, "--development-unattended")
	if _, err := parseSetupIntent(args, true, ""); err == nil {
		t.Fatal("release accepted unattended server install")
	}
	development = "true"
	if _, err := parseSetupIntent(args, true, ""); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "irvio-bootstrap:v1:AAAA", "hydra-bootstrap:v1:AAAA", code + " --role desktop", strings.Repeat("a", productbootstrap.MaxBundleCodeBytes+1)} {
		if _, err := serverSetupIntent(invalid, false); err == nil {
			t.Fatal("invalid bootstrap accepted")
		}
	}
	private, err := productbootstrap.FromMultiaddrs([]string{strings.Replace(addresses[0], "8.8.8.8", "192.168.1.1", 1)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	code, err = productbootstrap.Encode(private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverSetupIntent(code, false); err == nil {
		t.Fatal("server accepted bootstrap with no production STUN source")
	}
}
