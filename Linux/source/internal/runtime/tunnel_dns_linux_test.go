// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

type tunnelDNSTestStage struct {
	events     *[]string
	promoteErr error
	closeOnce  sync.Once
}

func (s *tunnelDNSTestStage) Promote(context.Context) error {
	*s.events = append(*s.events, "base promote")
	return s.promoteErr
}

func (s *tunnelDNSTestStage) Close() error {
	s.closeOnce.Do(func() { *s.events = append(*s.events, "base close") })
	return nil
}

func tunnelDNSTestRequest() InstallRequest {
	return InstallRequest{Lease: tunnellease.Lease{IPv4: netip.MustParsePrefix("10.77.0.2/32")}}
}

func TestTunnelDNSManagerAppliesAndReverts(t *testing.T) {
	var calls []string
	manager, err := newTunnelDNSManager("hydra0", []netip.Addr{
		netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("9.9.9.9"),
		netip.MustParseAddr("2606:4700:4700::1111"),
	}, func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}, func(name string) (int, error) {
		if name != "hydra0" {
			t.Fatalf("unexpected interface %q", name)
		}
		return 41, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.activate(context.Background(), 1, tunnelDNSTestRequest()); err != nil {
		t.Fatal(err)
	}
	if err := manager.release(1); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"dns hydra0 1.1.1.1 9.9.9.9",
		"domain hydra0 ~.",
		"default-route hydra0 yes",
		"revert hydra0",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands = %#v, want %#v", calls, want)
	}
}

func TestTunnelDNSManagerFencesSupersededRelease(t *testing.T) {
	var calls []string
	manager, err := newTunnelDNSManager("hydra0", []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		func(_ context.Context, args ...string) error {
			calls = append(calls, strings.Join(args, " "))
			return nil
		}, func(string) (int, error) { return 41, nil })
	if err != nil {
		t.Fatal(err)
	}
	request := tunnelDNSTestRequest()
	if err := manager.activate(context.Background(), 1, request); err != nil {
		t.Fatal(err)
	}
	if err := manager.activate(context.Background(), 2, request); err != nil {
		t.Fatal(err)
	}
	if err := manager.release(1); err != nil {
		t.Fatal(err)
	}
	if got := len(calls); got != 3 {
		t.Fatalf("stale release changed DNS: command count = %d, want 3", got)
	}
	if err := manager.release(2); err != nil {
		t.Fatal(err)
	}
	if got, want := calls[len(calls)-1], "revert hydra0"; got != want {
		t.Fatalf("last command = %q, want %q", got, want)
	}
}

func TestTunnelDNSManagerRollsBackPartialConfiguration(t *testing.T) {
	var calls []string
	failure := errors.New("injected domain failure")
	manager, err := newTunnelDNSManager("hydra0", []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		func(_ context.Context, args ...string) error {
			call := strings.Join(args, " ")
			calls = append(calls, call)
			if strings.HasPrefix(call, "domain ") {
				return failure
			}
			return nil
		}, func(string) (int, error) { return 41, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.activate(context.Background(), 1, tunnelDNSTestRequest()); !errors.Is(err, failure) {
		t.Fatalf("activate error = %v, want injected failure", err)
	}
	want := []string{"dns hydra0 1.1.1.1", "domain hydra0 ~.", "revert hydra0"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands = %#v, want %#v", calls, want)
	}
	if manager.current.token != 0 {
		t.Fatalf("partial configuration retained token %d", manager.current.token)
	}
}

func TestTunnelDNSInstallerOrdersPromotionAndCleanup(t *testing.T) {
	var events []string
	base := SessionInstallerFunc(func(context.Context, InstallRequest) (io.Closer, error) {
		return &tunnelDNSTestStage{events: &events}, nil
	})
	manager, err := newTunnelDNSManager("hydra0", []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		func(_ context.Context, args ...string) error {
			events = append(events, strings.Join(args, " "))
			return nil
		}, func(string) (int, error) { return 41, nil })
	if err != nil {
		t.Fatal(err)
	}
	installer, err := newTunnelDNSInstaller(base, nil, manager)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := installer.Install(context.Background(), tunnelDNSTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.(SessionPromoter).Promote(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := resource.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"base promote",
		"dns hydra0 1.1.1.1",
		"domain hydra0 ~.",
		"default-route hydra0 yes",
		"revert hydra0",
		"base close",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestTunnelDNSInstallerRejectsConnectedPromotionWhenDNSFails(t *testing.T) {
	var events []string
	base := SessionInstallerFunc(func(context.Context, InstallRequest) (io.Closer, error) {
		return &tunnelDNSTestStage{events: &events}, nil
	})
	failure := errors.New("injected resolver failure")
	manager, err := newTunnelDNSManager("hydra0", []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		func(_ context.Context, args ...string) error {
			call := strings.Join(args, " ")
			events = append(events, call)
			if strings.HasPrefix(call, "dns ") {
				return failure
			}
			return nil
		}, func(string) (int, error) { return 41, nil })
	if err != nil {
		t.Fatal(err)
	}
	installer, err := newTunnelDNSInstaller(base, nil, manager)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := installer.Install(context.Background(), tunnelDNSTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.(SessionPromoter).Promote(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("promote error = %v, want injected failure", err)
	} else {
		var outcome *exitselector.OutcomeError
		if !errors.As(err, &outcome) || outcome.Outcome != exitselector.OutcomeLocalPromotionFailed {
			t.Fatalf("DNS promotion error classification = %#v", err)
		}
	}
	want := []string{"base promote", "dns hydra0 1.1.1.1", "revert hydra0", "base close"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestTunnelDNSCommandPreservesBoundedSanitizedStderr(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "resolvectl-fixture")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'stdout-secret'\nprintf 'org.freedesktop.resolve1.NoSuchLink\\nsecond line\\001' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := runTunnelDNSExecutable(context.Background(), executable, "dns", "hydra0", "198.18.250.20")
	var commandError *tunnelDNSCommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("command error type = %T, want *tunnelDNSCommandError", err)
	}
	message := err.Error()
	for _, want := range []string{"operation=dns", `interface="hydra0"`, "org.freedesktop.resolve1.NoSuchLink", "second line?"} {
		if !strings.Contains(message, want) {
			t.Fatalf("command error %q missing %q", message, want)
		}
	}
	if strings.Contains(message, "stdout-secret") || strings.ContainsAny(message, "\n\r\t") {
		t.Fatalf("command diagnostics were not isolated and sanitized: %q", message)
	}
}

func TestBoundedCommandOutputTruncates(t *testing.T) {
	output := &boundedCommandOutput{limit: maxTunnelDNSStderr}
	_, _ = output.Write([]byte(strings.Repeat("x", maxTunnelDNSStderr+100)))
	if got := output.String(); len(got) > maxTunnelDNSStderr+len(" [truncated]") || !strings.HasSuffix(got, " [truncated]") {
		t.Fatalf("bounded output length=%d suffix=%q", len(got), got[max(0, len(got)-20):])
	}
}

func TestTunnelDNSPromotionInHostNamespace(t *testing.T) {
	if os.Getenv("HYDRA_TUNNEL_DNS_PRIVILEGED") != "1" || os.Geteuid() != 0 {
		t.Skip("set HYDRA_TUNNEL_DNS_PRIVILEGED=1 and execute the prebuilt test binary as root")
	}
	selfNS, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	hostNS, err := os.Readlink("/proc/1/ns/net")
	if err != nil || selfNS != hostNS {
		t.Fatalf("DNS host gate must run in PID 1 network namespace: self=%s host=%s err=%v", selfNS, hostNS, err)
	}
	baseline := hostDNSConfiguration(t)
	name := fmt.Sprintf("hydns%x", os.Getpid()&0xffff)
	owner, err := tunnel.NewLinux(tunnel.Config{
		Name: name, MTU: 1280,
		Addresses: []netip.Prefix{netip.MustParsePrefix("198.18.250.2/32")},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	manager, err := newTunnelDNSManager(name, []netip.Addr{netip.MustParseAddr("198.18.250.20")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	events := make([]string, 0, 2)
	installer, err := newTunnelDNSInstaller(SessionInstallerFunc(func(context.Context, InstallRequest) (io.Closer, error) {
		return &tunnelDNSTestStage{events: &events}, nil
	}), nil, manager)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := installer.Install(context.Background(), tunnelDNSTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resource.Close() })
	if err := resource.(SessionPromoter).Promote(context.Background()); err != nil {
		t.Fatalf("host-namespace DNS promotion failed: %v", err)
	}
	checkLink := func(want map[string]string) {
		t.Helper()
		for operation, suffix := range want {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			command := exec.CommandContext(ctx, resolvectlPath, "--no-pager", operation, name)
			command.Env = append(os.Environ(), "LC_ALL=C", "SYSTEMD_COLORS=0")
			status, err := command.CombinedOutput()
			cancel()
			if err != nil || !strings.HasSuffix(strings.TrimSpace(string(status)), suffix) {
				t.Fatalf("actual link DNS %s mismatch: want suffix=%q error=%v status=%q", operation, suffix, err, status)
			}
		}
	}
	checkLink(map[string]string{"dns": ": 198.18.250.20", "domain": ": ~.", "default-route": ": yes"})
	if err := resource.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	// Check revert before deleting TUN, so kernel link removal cannot hide missed DNS cleanup.
	checkLink(map[string]string{"dns": ":", "domain": ":", "default-route": ": no"})
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	var actual map[string]string
	for attempt := 0; attempt < 20; attempt++ {
		actual = hostDNSConfiguration(t)
		if reflect.DeepEqual(baseline, actual) {
			t.Log("real host DNS configuration and resolv.conf restored to baseline")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("DNS teardown did not restore host baseline: before=%v after=%v", baseline, actual)
}

func hostDNSConfiguration(t *testing.T) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, operation := range []string{"dns", "domain", "default-route"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		command := exec.CommandContext(ctx, resolvectlPath, "--no-pager", operation)
		command.Env = append(os.Environ(), "LC_ALL=C", "SYSTEMD_COLORS=0")
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("snapshot actual host DNS %s: %v %s", operation, err, output)
		}
		result[operation] = string(output)
	}
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	result["resolv.conf"] = string(data)
	info, err := os.Lstat("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	result["resolv.conf-mode"] = info.Mode().String()
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink("/etc/resolv.conf")
		if err != nil {
			t.Fatal(err)
		}
		result["resolv.conf-target"] = link
	}
	return result
}
