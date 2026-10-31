// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/runtime"
)

func TestInheritedSystemdSocketSurvivesDaemonExit(t *testing.T) {
	if path := os.Getenv("IRVIO_TEST_ACTIVATED_SOCKET"); path != "" {
		if err := os.Setenv("LISTEN_PID", fmt.Sprint(os.Getpid())); err != nil {
			t.Fatal(err)
		}
		config := DefaultServerConfig(path)
		server, err := NewServer(config, &fakeController{status: runtime.Status{State: runtime.StateIdle}})
		if err != nil {
			t.Fatal(err)
		}
		if err := server.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		client, err := Dial(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("systemd-owned socket was unlinked: %v", err)
		}
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	defer func() { _ = listener.Close(); _ = os.Remove(path) }()
	file, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestInheritedSystemdSocketSurvivesDaemonExit$")
	command.ExtraFiles = []*os.File{file}
	command.Env = append(os.Environ(), "IRVIO_TEST_ACTIVATED_SOCKET="+path, "LISTEN_FDS=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("activated daemon child: %v\n%s", err, output)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("systemd-owned socket after daemon exit: %v", err)
	}
}

func testServerConfig(t *testing.T) ServerConfig {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return DefaultServerConfig(filepath.Join(directory, "control.sock"))
}

func startTestServer(t *testing.T, controller *fakeController, configure func(*ServerConfig)) (*Server, string) {
	t.Helper()
	config := testServerConfig(t)
	path := config.SocketPath
	config.PollInterval = time.Millisecond
	config.IdleTimeout = time.Second
	config.FrameTimeout = 100 * time.Millisecond
	config.WriteTimeout = 100 * time.Millisecond
	config.RequestTimeout = 100 * time.Millisecond
	if configure != nil {
		configure(&config)
	}
	server, err := NewServer(config, controller)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close server: %v", err)
		}
		if err := server.Close(); err != nil {
			t.Errorf("second close: %v", err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("socket remains after close: %v", err)
		}
	})
	return server, path
}

func TestCredentialPolicyUsesKernelPeerCredentials(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	_, authorizedPath := startTestServer(t, controller, nil)
	client := dialTestClient(t, authorizedPath)
	if _, err := client.GetStatus(context.Background()); err != nil {
		t.Fatalf("authorized client: %v", err)
	}

	_, unauthorizedPath := startTestServer(t, controller, func(config *ServerConfig) {
		config.Authorizer = CredentialPolicy{
			AllowedUIDs: map[uint32]struct{}{uint32(os.Geteuid() + 1): {}},
			AllowedGIDs: map[uint32]struct{}{uint32(os.Getegid() + 1): {}},
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Dial(ctx, unauthorizedPath); err == nil {
		t.Fatal("unauthorized kernel credentials were accepted")
	}
}

func TestCredentialPolicyAcceptsSupplementaryGroup(t *testing.T) {
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	var supplementary int
	for _, group := range groups {
		if group != os.Getegid() {
			supplementary = group
			break
		}
	}
	if supplementary == 0 {
		t.Skip("process has no nonzero supplementary group")
	}
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	_, path := startTestServer(t, controller, func(config *ServerConfig) {
		config.Authorizer = CredentialPolicy{AllowedGIDs: map[uint32]struct{}{uint32(supplementary): {}}}
	})
	client := dialTestClient(t, path)
	if _, err := client.GetStatus(context.Background()); err != nil {
		t.Fatalf("supplementary group was not accepted: %v", err)
	}
}

func TestSocketPermissionsAndSafeStaleHandling(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	server, path := startTestServer(t, controller, nil)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode %o", info.Mode().Perm())
	}
	secondConfig := DefaultServerConfig(path)
	second, err := NewServer(secondConfig, controller)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Start(context.Background()); !errors.Is(err, ErrSocketInUse) {
		t.Fatalf("live socket replacement: %v", err)
	}
	_ = server.Close()

	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(directory, "control.sock")
	if err := os.WriteFile(foreign, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := DefaultServerConfig(foreign)
	third, err := NewServer(config, controller)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.Start(context.Background()); err == nil {
		t.Fatal("foreign regular file was replaced")
	}
	contents, err := os.ReadFile(foreign)
	if err != nil || string(contents) != "foreign" {
		t.Fatalf("foreign file changed: contents=%q err=%v", contents, err)
	}
}

func TestStaleOwnedSocketIsAdopted(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	config := DefaultServerConfig(path)
	server, err := NewServer(config, controller)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("adopt stale socket: %v", err)
	}
	if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}

func TestParentDirectoryMustNotBeWorldAccessible(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	config := DefaultServerConfig(filepath.Join(directory, "control.sock"))
	server, err := NewServer(config, controller)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err == nil {
		t.Fatal("world-accessible socket directory was accepted")
	}
}

func TestKernelCredentialShape(t *testing.T) {
	if os.Geteuid() < 0 || os.Getegid() < 0 || syscall.Getpid() <= 0 {
		t.Fatal("invalid process credentials")
	}
}
