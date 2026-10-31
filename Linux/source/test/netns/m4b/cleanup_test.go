// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestM4B_PartialSetupCleanupIsIdempotent(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "partial-cleanup")
	namespace := lab.namespace("probe")
	left := lab.interfaceName('x')
	right := lab.interfaceName('y')
	lab.run("ip", "link", "add", left, "type", "veth", "peer", "name", right)
	lab.hostLinks = append(lab.hostLinks, left, right)
	lab.run("ip", "link", "set", left, "netns", namespace)
	runtimeDir := lab.tempDir("partial")
	if err := os.WriteFile(filepath.Join(runtimeDir, "identity.key"), []byte("test-only"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := lab.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lab.Close(); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("runtime directory remains after cleanup: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := suite.command(ctx, "ip", "netns", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, namespace) {
		t.Fatalf("namespace remains after cleanup: %s", namespace)
	}
	if _, err := suite.command(ctx, "ip", "link", "show", right); err == nil {
		t.Fatalf("host-side partial veth remains after cleanup: %s", right)
	}
}
