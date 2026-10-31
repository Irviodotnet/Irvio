// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func incomingEcho(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			reader := bufio.NewReader(io.LimitReader(conn, 512))
			line, err := reader.ReadString('\n')
			if err == nil && strings.HasPrefix(line, "irvio-incoming-") {
				if _, err := io.WriteString(conn, line); err == nil {
					reader.ReadString('\n')
				}
			}
		}()
	}
}

func TestWindowsIncomingListenerHelper(t *testing.T) {
	if os.Getenv("IRVIO_INCOMING_HELPER") != "1" {
		t.Skip("child process only")
	}
	if want := os.Getenv("IRVIO_INCOMING_EXPECT_SID"); want != "" {
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil || user.User.Sid.String() != want {
			t.Fatal("unexpected child identity", err)
		}
	}
	path, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := &controlsocket.WindowsListeners{}
	defer owner.Close()
	l, err := owner.Listen(context.Background(), path.Binding, 49498, func(context.Context, winroute.Binding, netip.AddrPort) (io.Closer, error) {
		return closeFunction(func() error { return nil }), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("INCOMING_HELPER_READY")
	incomingEcho(l)
}

func TestWindowsNativeControlListener(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_INCOMING_TEST") != "1" {
		t.Skip("SYSTEM VM, mini-PC oracle and QEMU port-forward required")
	}
	ctx := context.Background()
	config := nativeProtectionConfig(t)
	store, err := wfp.NewStore(config.Owner)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Inspect(ctx)
	if err != nil || prior.Installed {
		t.Fatal("preexisting policy", err)
	}
	t.Cleanup(func() {
		if err := store.Remove(ctx); err != nil {
			t.Error(err)
		}
	})
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(filepath.Dir(self), "foreign-incoming.test.exe")
	config.Network.Application, err = wfp.ApplicationIDForPath(self)
	if err != nil {
		t.Fatal(err)
	}
	path, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	user, userSID := nativeProbeUser(t)
	defer user.Close()
	start := func(exe string, token syscall.Token) func() {
		t.Helper()
		deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
		cmd := exec.CommandContext(deadline, exe, "-test.run=^TestWindowsIncomingListenerHelper$")
		cmd.Env = append(slicesWithoutProbeCredential(os.Environ()), "IRVIO_INCOMING_HELPER=1")
		if token != 0 {
			cmd.SysProcAttr = &syscall.SysProcAttr{Token: token}
			cmd.Env = append(cmd.Env, "IRVIO_INCOMING_EXPECT_SID="+userSID)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		var once sync.Once
		stop := func() { once.Do(func() { cancel(); cmd.Wait() }) }
		t.Cleanup(stop)
		scanner := bufio.NewScanner(out)
		var lines []string
		for scanner.Scan() {
			if scanner.Text() == "INCOMING_HELPER_READY" {
				return stop
			}
			if len(lines) < 10 {
				lines = append(lines, scanner.Text())
			}
		}
		stop()
		t.Fatal("incoming helper failed before readiness", lines)
		return nil
	}
	probe := func(stage string, want bool) {
		t.Helper()
		d := net.Dialer{Timeout: 3 * time.Second, Control: path.Binding.Control, LocalAddr: net.TCPAddrFromAddrPort(netip.AddrPortFrom(path.Binding.Source, 0))}
		conn, err := d.DialContext(ctx, "tcp4", "192.168.23.2:49497")
		if err != nil {
			t.Fatal("oracle control", err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := json.NewEncoder(conn).Encode(map[string]string{"stage": stage}); err != nil {
			t.Fatal(err)
		}
		var result struct {
			Stage string `json:"stage"`
			OK    bool   `json:"ok"`
		}
		if err := json.NewDecoder(conn).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if result.Stage != stage || result.OK != want {
			t.Fatalf("%s reply=%v want=%v", stage, result.OK, want)
		}
		t.Logf("%s user-payload echo=%v", stage, result.OK)
	}
	for _, p := range []struct {
		name, exe string
		token     syscall.Token
	}{{"baseline-owner", self, 0}, {"baseline-user", self, syscall.Token(user)}, {"baseline-foreign", foreign, 0}} {
		stop := start(p.exe, p.token)
		probe(p.name, true)
		stop()
	}
	b, err := NewWindowsBackend(config)
	if err != nil {
		t.Fatal(err)
	}
	i, _, err := b.Ensure(ctx, config.Policy)
	if err != nil {
		t.Fatal(err)
	}
	u, err := b.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	o, err := u.OwnUnderlay(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Error(err)
		}
	})
	rpc, err := o.Acquire(ctx, 1, WindowsBypass{Interface: path.Binding.LUID, Source: path.Binding.Source, Remote: netip.MustParseAddrPort("192.168.23.2:49497"), Protocol: 6})
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.Close()
	listeners := &controlsocket.WindowsListeners{}
	t.Cleanup(func() {
		if err := listeners.Close(); err != nil {
			t.Error(err)
		}
	})
	l, err := listeners.Listen(ctx, path.Binding, 49498, func(ctx context.Context, binding winroute.Binding, local netip.AddrPort) (io.Closer, error) {
		lease, err := o.AcquireListener(ctx, 1, WindowsControlListener{Interface: binding.LUID, Local: local})
		if lease == nil {
			return nil, err
		}
		return lease, err
	})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	go func() { defer close(served); incomingEcho(l) }()
	probe("protected-owner", true)
	device, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-Incoming", Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.243.1/24")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := device.Close(); err != nil {
			t.Error(err)
		}
	})
	routes, err := winroute.OpenTunnelRoutes(device.LUID(), filepath.Join(t.TempDir(), "routes"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := routes.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := u.UpdateTunnel(ctx, device.LUID(), nil); err != nil {
		t.Fatal(err)
	}
	if err := routes.Ensure(ctx, true, false); err != nil {
		t.Fatal(err)
	}
	selected, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil || selected.Binding.LUID != device.LUID() {
		t.Fatal("competing default route was not selected", selected, err)
	}
	probe("protected-owner-tunnel-default", true)
	if err := routes.Close(); err != nil {
		t.Fatal(err)
	}
	if err := u.UpdateTunnel(ctx, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	<-served
	probe("listener-closed", false)
	stale, err := o.AcquireListener(ctx, 1, WindowsControlListener{Interface: path.Binding.LUID, Local: netip.AddrPortFrom(path.Binding.Source, 49498)})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		name, exe string
		token     syscall.Token
	}{{"stale-user", self, syscall.Token(user)}, {"stale-foreign", foreign, 0}} {
		stop := start(p.exe, p.token)
		probe(p.name, false)
		stop()
	}
	stop := start(self, 0)
	probe("stale-owner", true)
	if err := stale.Close(); err != nil {
		stop()
		t.Fatal(err)
	}
	probe("revoked-owner", false)
	stop()
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := i.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	stop = start(self, syscall.Token(user))
	probe("cleanup-user", true)
	stop()
}
