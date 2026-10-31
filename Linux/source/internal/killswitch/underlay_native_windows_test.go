// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/multiformats/go-multiaddr"
)

func TestWindowsNativeOwnedUnderlay(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_PROTECTION_TEST") != "1" {
		t.Skip("requires disposable SYSTEM VM and mini-PC echo oracle")
	}
	ctx := context.Background()
	c := nativeProtectionConfig(t)
	store, err := wfp.NewStore(c.Owner)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Inspect(ctx)
	if err != nil || prior.Installed {
		t.Fatal("existing protection fixture", err)
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
	c.Network.Application, err = wfp.ApplicationIDForPath(self)
	if err != nil {
		t.Fatal(err)
	}
	path, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	user, sid := nativeProbeUser(t)
	defer user.Close()
	probe := func(stage string, want bool) {
		t.Helper()
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(bounded, self, "-test.run=^TestWindowsWANProbeHelper$")
		cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(user)}
		cmd.Env = append(slicesWithoutProbeCredential(os.Environ()), "IRVIO_WINDOWS_WAN_HELPER=1", "IRVIO_WINDOWS_WAN_STAGE=owned-"+stage, "IRVIO_WINDOWS_WAN_SID="+sid)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ordinary probe: %v %s", err, out)
		}
		var result map[string]bool
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "WAN_RESULT ") {
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "WAN_RESULT ")), &result); err != nil {
					t.Fatal(err)
				}
			}
		}
		if !strings.Contains(string(out), "WAN_NONADMIN_VERIFIED") || len(result) != 2 || result["tcp"] != want || result["udp"] != want {
			t.Fatalf("%s ordinary WAN=%v want=%v", stage, result, want)
		}
		t.Logf("%s ordinary TCP/UDP=%v", stage, want)
	}
	probe("baseline", true)
	b, err := NewWindowsBackend(c)
	if err != nil {
		t.Fatal(err)
	}
	installation, _, err := b.Ensure(ctx, c.Policy)
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
	generation := uint64(1)
	factory, err := controlsocket.DialerForOwnedBindings(func(ctx context.Context, target netip.AddrPort) (winroute.Binding, io.Closer, error) {
		lease, err := o.Acquire(ctx, generation, WindowsBypass{Interface: path.Binding.LUID, Source: path.Binding.Source, Remote: target, Protocol: 6})
		if lease == nil {
			return path.Binding, nil, err
		}
		return path.Binding, lease, err
	})
	if err != nil {
		t.Fatal(err)
	}
	dial := func(port uint16) (net.Conn, error) {
		d, err := factory(multiaddr.StringCast(fmt.Sprintf("/ip4/192.168.23.2/tcp/%d", port)))
		if err != nil {
			return nil, err
		}
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return d.DialContext(bounded, "tcp4", fmt.Sprintf("192.168.23.2:%d", port))
	}
	echo := func(conn net.Conn, tag string) error {
		conn.SetDeadline(time.Now().Add(time.Second))
		message := fmt.Sprintf("irvio-nat-wfp-owned-%d-%s\n", os.Getpid(), tag)
		if _, err := io.WriteString(conn, message); err != nil {
			return err
		}
		buf := make([]byte, len(message))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return err
		}
		if string(buf) != message {
			return fmt.Errorf("wrong echo")
		}
		return nil
	}
	first, err := dial(49494)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := dial(49494)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if len(b.network.Underlay) != 1 {
		t.Fatal("native duplicate grant")
	}
	if err := echo(first, "first"); err != nil {
		t.Fatal(err)
	}
	if err := echo(second, "second"); err != nil {
		t.Fatal(err)
	}
	probe("protected", false)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := echo(second, "after-first-close"); err != nil {
		t.Fatal(err)
	}
	if err := o.Advance(ctx, 2); err != nil {
		t.Fatal(err)
	}
	generation = 2
	if err := echo(second, "revoked-must-not-arrive"); err == nil {
		t.Fatal("old TCP connection survived grant revocation")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	for n := range 20 {
		conn, err := dial(49494)
		if err != nil {
			t.Fatal(err)
		}
		err = echo(conn, fmt.Sprintf("cycle-%d", n))
		closeErr := conn.Close()
		if err != nil || closeErr != nil || len(b.network.Underlay) != 0 {
			t.Fatal("native lifecycle", err, closeErr)
		}
	}
	if conn, err := dial(49493); err == nil {
		conn.Close()
		t.Fatal("unused oracle port unexpectedly open")
	}
	if len(b.network.Underlay) != 0 || len(o.leases) != 0 {
		t.Fatal("failed dial left grant")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	probe("owner-closed", false)
	if err := installation.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	probe("cleanup", true)
	after, err := store.Inspect(ctx)
	if err != nil || after.Installed {
		t.Fatal("protection cleanup residue", err)
	}
	t.Log("20 TCP grant lifecycles, duplicate ownership, stale-flow revocation, failed dial cleanup: PASS")
}
