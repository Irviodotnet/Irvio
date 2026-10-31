// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestLauncherDNSIsolation(t *testing.T) {
	launcher := os.Getenv("IRVIO_LAB_DNS_LAUNCHER_TEST")
	if launcher == "" {
		t.Skip("requires explicit privileged mini-PC launcher path")
	}
	if os.Geteuid() != 0 {
		t.Fatal("requires root for isolated mount namespaces")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for i := 0; i < 20; i++ {
		cmd := exec.CommandContext(ctx, launcher, self, "-test.run=^TestLauncherDNSChild$")
		cmd.Env = append(os.Environ(), "IRVIO_LAB_DNS_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("launch %d: %v: %s", i, err, output)
		}
	}
}

func TestLauncherDNSChild(t *testing.T) {
	if os.Getenv("IRVIO_LAB_DNS_CHILD") != "1" {
		t.Skip("isolated subprocess only")
	}
	want, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 25; j++ {
				runtime.Gosched()
				got, err := os.ReadFile("/usr/bin/resolvectl")
				if err != nil || !bytes.Equal(got, want) {
					t.Error("DNS executable escaped isolated mount namespace")
					return
				}
			}
		}()
	}
	workers.Wait()
}
