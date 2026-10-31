// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
)

func TestConfigStrictAndRolePrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	missing, err := LoadConfig(path)
	if err != nil || missing.Version != 1 {
		t.Fatalf("missing config = %+v, %v", missing, err)
	}
	role, err := ResolveRole(missing, "")
	if err != nil || role != Desktop {
		t.Fatalf("default role = %q, %v", role, err)
	}
	for _, test := range []struct {
		name, json string
		valid      bool
	}{
		{"server", `{"version":1,"role":"server"}`, true},
		{"empty", `{"version":1}`, true},
		{"typo", `{"version":1,"rendevous":true}`, false},
		{"version", `{"version":2}`, false},
		{"missing version", `{"role":"server"}`, false},
		{"invalid role", `{"version":1,"role":"genesis"}`, false},
		{"trailing object", `{"version":1}{"version":1}`, false},
		{"duplicate role", `{"version":1,"role":"server","role":"desktop"}`, false},
		{"null role", `{"version":1,"role":null}`, false},
		{"oversized", `{"version":1,"role":"` + strings.Repeat("x", 4096) + `"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.json), 0o600); err != nil {
				t.Fatal(err)
			}
			config, err := LoadConfig(path)
			if (err == nil) != test.valid {
				t.Fatalf("config = %+v, error = %v", config, err)
			}
			if test.name == "server" {
				role, err := ResolveRole(config, "desktop")
				if err != nil || role != Desktop {
					t.Fatalf("CLI override role = %q, %v", role, err)
				}
			}
		})
	}
	if _, err := ParseRole("genesis"); err == nil {
		t.Fatal("genesis must not be a public product role")
	}
}

func TestExpandBootstrapBundleAndLegacyAddress(t *testing.T) {
	_, public, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	legacy := "/ip4/203.0.113.9/tcp/4001/p2p/" + id.String()
	bundle, err := productbootstrap.FromMultiaddrs([]string{legacy}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.FormatVersion != productbootstrap.BundleVersion {
		t.Fatal("unexpected bundle version")
	}
	code, err := productbootstrap.Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExpandBootstrap([]string{code, legacy})
	if err != nil || len(got) != 2 || got[0] != legacy || got[1] != legacy {
		t.Fatalf("expanded = %v, %v", got, err)
	}
	if _, err := ExpandBootstrap([]string{"irvio-bootstrap:v2:AAAA"}); err == nil {
		t.Fatal("unsupported bundle must fail")
	}
}
