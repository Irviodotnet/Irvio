// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package protocol

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPublisherStateDurablyAdvancesAndLocks(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	first, err := AcquirePublisherState(directory)
	if err != nil {
		t.Fatal(err)
	}
	if first.Incarnation() != 1 {
		t.Fatalf("first incarnation=%d", first.Incarnation())
	}
	if _, err := AcquirePublisherState(directory); err == nil {
		t.Fatal("second concurrent publisher unexpectedly acquired ownership")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquirePublisherState(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.Incarnation() != 2 {
		t.Fatalf("second incarnation=%d", second.Incarnation())
	}
	assertPublisherPermissions(t, directory)
}

func TestPublisherCrashHelper(t *testing.T) {
	directory := os.Getenv("IRVIO_PUBLISHER_CRASH_TEST_DIR")
	if directory == "" {
		return
	}
	state, err := AcquirePublisherState(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	fmt.Println("PUBLISHER_LOCKED")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestPublisherOwnershipRecoversAfterProcessKill(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestPublisherCrashHelper$")
	child.Env = append(os.Environ(), "IRVIO_PUBLISHER_CRASH_TEST_DIR="+directory)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	ready, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || ready != "PUBLISHER_LOCKED\n" {
		t.Fatalf("publisher helper readiness: %q, %v", ready, err)
	}
	if other, err := AcquirePublisherState(directory); err == nil {
		other.Close()
		t.Fatal("second process acquired an active publisher lock")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	waited = true
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, err := AcquirePublisherState(directory)
		if err == nil {
			defer state.Close()
			if state.Incarnation() != 2 {
				t.Fatalf("crash recovery incarnation=%d", state.Incarnation())
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ownership not released after process kill: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPublisherStateRejectsCorruption(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	state, err := AcquirePublisherState(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, PublisherStateFileName)
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = AcquirePublisherState(directory)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt state error=%v", err)
	}
}
