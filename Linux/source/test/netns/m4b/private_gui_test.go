// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

// Opt-in human interaction uses real UI commands; the harness only waits for CONNECTED.
func runPrivateManualGUIOnboarding(t *testing.T, ctx context.Context, lab *lab, client *localapi.Client, namespaces []string, base string) {
	t.Helper()
	ui := assertNormalUserExecutable(t, "IRVIO_PRIVATE_UI")
	var consoles []map[string]string
	for _, item := range []struct {
		label string
		index int
	}{{"host", 5}, {"client", 6}} {
		label := "onboarding-" + item.label
		graphics := filepath.Join(base, label)
		if err := os.Mkdir(graphics, 0700); err != nil {
			t.Fatal(err)
		}
		lab.startChild(label+"-xvfb", namespaces[item.index], "Xvfb", "-displayfd", "1", "-screen", "0", "1280x1000x24", "-nolisten", "tcp", "-ac")
		displayFile := filepath.Join(lab.artifactDir, label+"-xvfb.stdout.log")
		ready, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := waitForFile(ready, displayFile)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		value, err := os.ReadFile(displayFile)
		if err != nil {
			t.Fatal(err)
		}
		display, err := strconv.Atoi(strings.TrimSpace(string(value)))
		if err != nil || display < 0 {
			t.Fatal("invalid Xvfb display")
		}
		socket := filepath.Join(base, "private-"+item.label, "control.sock")
		lab.startChild(label, namespaces[item.index], "env", "DISPLAY=:"+strconv.Itoa(display),
			"GDK_BACKEND=x11", "LIBGL_ALWAYS_SOFTWARE=1", "DBUS_SESSION_BUS_ADDRESS=",
			"HOME="+graphics, "XDG_RUNTIME_DIR="+graphics, "XDG_CONFIG_HOME="+filepath.Join(graphics, "config"),
			"XDG_DATA_HOME="+filepath.Join(graphics, "data"), "HYDRA_CONTROL_SOCKET="+socket, ui)
		consoles = append(consoles, map[string]string{"role": item.label, "namespace": namespaces[item.index], "display": ":" + strconv.Itoa(display), "socket": socket})
	}
	data, err := json.MarshalIndent(consoles, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lab.artifactDir, "onboarding-consoles.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("native onboarding ready; console metadata: %s", path)
	for {
		status, err := client.GetStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == localv1.ConnectionState_CONNECTION_STATE_CONNECTED {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("native onboarding deadline:", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func runPrivateGUICrash(t *testing.T, ctx context.Context, lab *lab, api *localapi.Client,
	daemon *childProcess, namespace, base, hostID string, hostWAN netip.Addr, target netip.AddrPort) {
	t.Helper()
	checkTraffic := func() {
		t.Helper()
		select {
		case <-daemon.done:
			t.Fatal("GUI death terminated daemon")
		default:
		}
		status, err := api.GetStatus(ctx)
		if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_CONNECTED || status.PrivateTargetPeerId != hostID || status.CountryCode != "" {
			t.Fatalf("GUI death changed private session: status=%v err=%v", status, err)
		}
		probe := runM7Probe(t, lab, namespace, "http", target)
		source, err := netip.ParseAddrPort(probe.ObservedSource)
		if err != nil || !probe.Success || source.Addr() != hostWAN {
			t.Fatalf("GUI death interrupted private egress: %+v err=%v", probe, err)
		}
	}
	runPrivateGUIRestarts(t, ctx, lab, namespace, base, "private-client", checkTraffic, nil)
}

func runPrivateHostingGUICrash(t *testing.T, ctx context.Context, lab *lab, host, client *localapi.Client,
	daemon *childProcess, namespaces []string, base, hostID string, hostWAN netip.Addr, target netip.AddrPort) {
	t.Helper()
	checkTraffic := func() {
		t.Helper()
		select {
		case <-daemon.done:
			t.Fatal("hosting GUI death terminated daemon")
		default:
		}
		status, err := host.GetStatus(ctx)
		if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_HOSTING || !status.PrivateHostingWanted || status.PrivateTargetPeerId != "" || status.CountryCode != "" || status.ContributionEnabled {
			t.Fatalf("GUI death changed private hosting intent: status=%v err=%v", status, err)
		}
		stats, err := host.GetTrafficStats(ctx)
		if err != nil || stats.GetClients() != 1 {
			t.Fatalf("GUI death lost downstream lease: stats=%v err=%v", stats, err)
		}
		status, err = client.GetStatus(ctx)
		if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_CONNECTED || status.PrivateTargetPeerId != hostID || status.CountryCode != "" {
			t.Fatalf("hosting GUI death changed downstream session: status=%v err=%v", status, err)
		}
		for _, index := range []int{5, 6} {
			probe := runM7Probe(t, lab, namespaces[index], "http", target)
			source, err := netip.ParseAddrPort(probe.ObservedSource)
			if err != nil || !probe.Success || source.Addr() != hostWAN {
				t.Fatalf("hosting GUI death interrupted node %d egress: %+v err=%v", index, probe, err)
			}
		}
	}
	runPrivateGUIRestarts(t, ctx, lab, namespaces[5], base, "private-host", checkTraffic, nil)
}

func runPrivateGUIRestarts(t *testing.T, ctx context.Context, lab *lab, namespace, base, profile string, checkTraffic func(), interaction func(*childProcess, []string, string)) {
	t.Helper()
	ui := assertNormalUserExecutable(t, "IRVIO_PRIVATE_UI")
	graphics := filepath.Join(base, "gui")
	if err := os.Mkdir(graphics, 0700); err != nil {
		t.Fatal(err)
	}
	xserver := lab.startChild("private-xvfb", namespace, "Xvfb", "-displayfd", "1", "-screen", "0", "1280x1000x24", "-nolisten", "tcp", "-ac")
	defer xserver.stop()
	displayFile := filepath.Join(lab.artifactDir, "private-xvfb.stdout.log")
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := waitForFile(ready, displayFile); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(displayFile)
	if err != nil {
		t.Fatal(err)
	}
	display, err := strconv.Atoi(strings.TrimSpace(string(value)))
	if err != nil || display < 0 {
		t.Fatal("invalid Xvfb display")
	}
	busAddress := ""
	if os.Getenv("IRVIO_PRIVATE_GUI_DBUS") == "1" {
		busPath := filepath.Join(graphics, "session-bus")
		busAddress = "unix:path=" + busPath
		bus := lab.startChild("private-session-bus", namespace, "dbus-daemon", "--session", "--nofork", "--print-address=1", "--address="+busAddress)
		defer bus.stop()
		busReady, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := waitForFile(busReady, filepath.Join(lab.artifactDir, "private-session-bus.stdout.log"))
		cancel()
		if err != nil {
			t.Fatal("isolated GUI session bus:", err)
		}
	}
	environment := []string{"DISPLAY=:" + strconv.Itoa(display), "GDK_BACKEND=x11", "LIBGL_ALWAYS_SOFTWARE=1",
		"HOME=" + graphics, "XDG_RUNTIME_DIR=" + graphics, "XDG_CONFIG_HOME=" + filepath.Join(graphics, "config"),
		"XDG_DATA_HOME=" + filepath.Join(graphics, "data"), "DBUS_SESSION_BUS_ADDRESS=" + busAddress,
		"HYDRA_CONTROL_SOCKET=" + filepath.Join(base, profile, "control.sock")}
	for iteration := 0; iteration < 2; iteration++ {
		label := fmt.Sprintf("private-gui-%d", iteration)
		gui := lab.startChild(label, namespace, "env", append(environment, ui)...)
		// Bound native-window startup; screenshots additionally require manual rendered-state review.
		windowArgs := append([]string{"15", "env"}, environment...)
		windowArgs = append(windowArgs, "xdotool", "search", "--sync", "--onlyvisible", "--pid", strconv.Itoa(gui.command.Process.Pid), "--name", "IRVIO")
		windowCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		var window string
		for {
			output, err := suite.command(windowCtx, "ip", append([]string{"netns", "exec", namespace, "timeout"}, windowArgs...)...)
			if err == nil && strings.TrimSpace(output) != "" {
				window = strings.TrimSpace(output)
				break
			}
			if windowCtx.Err() != nil {
				cancel()
				t.Fatal("native window did not become discoverable:", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		cancel()
		time.Sleep(5 * time.Second)
		lab.inNamespace(namespace, "env", append(environment, "scrot", filepath.Join(lab.artifactDir, label+".png"))...)
		checkTraffic()
		if interaction != nil {
			interaction(gui, environment, window)
			return
		}
		if err := syscall.Kill(-gui.command.Process.Pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		select {
		case <-gui.done:
		case <-time.After(5 * time.Second):
			t.Fatal("GUI survived SIGKILL")
		}
		for sample := 0; sample < 5; sample++ {
			checkTraffic()
			time.Sleep(200 * time.Millisecond)
		}
	}
	t.Logf("real %s GUI twice SIGKILLed; daemon intent and user-plane egress survived", profile)
}
