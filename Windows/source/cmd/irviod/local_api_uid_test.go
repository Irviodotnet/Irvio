// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDesktopLocalAPIPolicy(t *testing.T) {
	p, err := localAPICredentialPolicy("1000", 972)
	if err != nil || len(p.AllowedUIDs) != 2 || len(p.AllowedGIDs) != 0 {
		t.Fatalf("unexpected UID policy: %+v, %v", p, err)
	}
	for _, uid := range []uint32{0, 1000} {
		if _, ok := p.AllowedUIDs[uid]; !ok {
			t.Fatalf("missing UID %d", uid)
		}
	}
	legacy, err := localAPICredentialPolicy("", 972)
	if _, ok := legacy.AllowedGIDs[972]; err != nil || !ok {
		t.Fatalf("legacy group policy lost: %+v, %v", legacy, err)
	}
	for _, uid := range []string{"0", "999", "-1", "+1000", "01000", "1000\n", "user", "4294967295", "4294967296"} {
		if _, err := localAPICredentialPolicy(uid, 972); err == nil {
			t.Fatalf("accepted invalid UID %q", uid)
		}
	}
}

func TestDesktopLocalAPIUIDWithoutGroup(t *testing.T) {
	uid := os.Geteuid()
	if uid < 1000 {
		t.Skip("requires an ordinary desktop test user")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "control.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	p, err := localAPICredentialPolicy(strconv.Itoa(uid), 0)
	if err != nil {
		t.Fatal(err)
	}
	if credentials, err := p.Authorize(conn); err != nil || credentials.UID != uint32(uid) {
		t.Fatalf("selected UID without allowed groups denied: %+v, %v", credentials, err)
	}
	p, err = localAPICredentialPolicy(strconv.Itoa(uid+1), uint32(os.Getegid()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Authorize(conn); err == nil {
		t.Fatal("wrong UID accepted through matching GID")
	}
}
