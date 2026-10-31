// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"bytes"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/productprofile"
	"github.com/Irviodotnet/Irvio/internal/winfile"
)

func testWindowsProfile(t *testing.T) *productprofile.Profile {
	t.Helper()
	profile, err := productprofile.Open(filepath.Join(t.TempDir(), "profile"))
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func testWindowsRecord(t *testing.T, path, name, value string) {
	t.Helper()
	d, err := winfile.OpenDirectory(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.WriteAtomic(name, []byte(value), false); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsProductValuesPersistAndProfileDeletion(t *testing.T) {
	p := testWindowsProfile(t)
	pool, tun, err := ResolveExitPool(p.Path())
	if err != nil {
		t.Fatal(err)
	}
	port, err := ResolveListenPort(p.Path())
	if err != nil || port < 40000 || port > 59999 || !allowedExitPool(pool) || tun != exitTUNAddress(pool) {
		t.Fatalf("pool=%s tun=%s port=%d error=%v", pool, tun, port, err)
	}
	for i := 0; i < 30; i++ {
		next, nextTUN, err := ResolveExitPool(p.Path())
		if err != nil || next != pool || nextTUN != tun {
			t.Fatalf("pool changed on reopen: %s %s %v", next, nextTUN, err)
		}
		nextPort, err := ResolveListenPort(p.Path())
		if err != nil || nextPort != port {
			t.Fatalf("port changed on reopen: %d %v", nextPort, err)
		}
	}
	if err := p.DeleteContents(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(p.Path())
	if err != nil || len(entries) != 0 {
		t.Fatalf("profile deletion residue: %v %v", entries, err)
	}
}

func TestWindowsProductPoolCollisionDoesNotRenumber(t *testing.T) {
	p := testWindowsProfile(t)
	testWindowsRecord(t, p.Path(), ExitPoolFilename, `{"version":1,"ipv4":"10.42.7.0/24"}`)
	before, _ := os.ReadFile(filepath.Join(p.Path(), ExitPoolFilename))
	if _, _, err := resolveWindowsExitPool(p.Path(), []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}); err == nil {
		t.Fatal("persisted pool collision ignored")
	}
	after, _ := os.ReadFile(filepath.Join(p.Path(), ExitPoolFilename))
	if !bytes.Equal(before, after) {
		t.Fatal("colliding pool silently renumbered")
	}
	occupied := []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16"), netip.MustParsePrefix("10.2.0.0/24")}
	pool, err := selectExitPoolFrom(occupied, 0)
	if err != nil || pool != netip.MustParsePrefix("10.2.1.0/24") {
		t.Fatalf("collision selection=%s %v", pool, err)
	}
	if _, err := selectExitPoolFrom([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, 0); err == nil {
		t.Fatal("exhausted pool accepted")
	}
}

func TestWindowsProductRecordRefusesCorruption(t *testing.T) {
	for _, value := range []string{`null`, `{}`, `{"version":2,"tcp_port":40000}`, `{"version":1,"tcp_port":22}`,
		`{"version":1,"tcp_port":40000,"tcp_port":40001}`, strings.Repeat("x", 1025)} {
		t.Run(fmt.Sprintf("port-%d", len(value)), func(t *testing.T) {
			p := testWindowsProfile(t)
			testWindowsRecord(t, p.Path(), ListenPortFilename, value)
			if _, err := ResolveListenPort(p.Path()); err == nil {
				t.Fatal("invalid port accepted")
			}
		})
	}
	for _, value := range []string{`{"version":1,"ipv4":"8.8.8.0/24"}`, `{"version":1,"ipv4":"10.42.0.1/24"}`, `{"version":1,"ipv4":"10.42.0.0/16"}`} {
		p := testWindowsProfile(t)
		testWindowsRecord(t, p.Path(), ExitPoolFilename, value)
		if _, _, err := resolveWindowsExitPool(p.Path(), nil); err == nil {
			t.Fatal("invalid pool accepted")
		}
	}
}

func TestWindowsProductRecordKeepsConcurrentWinner(t *testing.T) {
	p := testWindowsProfile(t)
	want := `{"version":1,"tcp_port":44000}`
	value, err := resolveWindowsRecord(p.Path(), ListenPortFilename, func() ([]byte, error) {
		testWindowsRecord(t, p.Path(), ListenPortFilename, want)
		return []byte(`{"version":1,"tcp_port":45000}`), nil
	})
	if err != nil || string(value) != want {
		t.Fatalf("concurrent winner replaced: %s %v", value, err)
	}
}

func TestWindowsProductRecordRefusesHardlink(t *testing.T) {
	p := testWindowsProfile(t)
	testWindowsRecord(t, p.Path(), ListenPortFilename, `{"version":1,"tcp_port":44000}`)
	path := filepath.Join(p.Path(), ListenPortFilename)
	if err := os.Link(path, filepath.Join(filepath.Dir(p.Path()), "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveListenPort(p.Path()); err == nil {
		t.Fatal("multiply linked record accepted")
	}
}
