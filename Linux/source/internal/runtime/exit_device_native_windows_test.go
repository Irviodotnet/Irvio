// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/sessioncontrol"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/winfirewall"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

func TestNativeWindowsExitDeviceOwnership(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_EXIT_DEVICE_TEST") != "1" {
		t.Skip("requires isolated elevated Windows VM and signed Wintun")
	}
	c := exitDeviceConfig()
	c.TUNName = fmt.Sprintf("IRVIO-X%d", os.Getpid())
	journal := filepath.Join(t.TempDir(), "control")
	for cycle := 0; cycle < 2; cycle++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		d, err := newWindowsExitDevice(ctx, c, journal)
		cancel()
		if d != nil {
			t.Cleanup(func() {
				if err := d.Close(); err != nil {
					t.Error(err)
				}
			})
		}
		if err != nil {
			t.Fatal(err)
		}
		control := c.TUNAddresses[0].Addr()
		endpoint := netip.AddrPortFrom(control, c.SessionControlPort)
		listener, err := sessioncontrol.ListenTunnel(context.Background(), "tcp", endpoint.String(), d.tun.Name())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		if source, err := winroute.ResolveTunnel(control.Next(), d.tun.LUID()); err == nil {
			t.Fatal("idle Exit control selected as user source", source)
		}
		own := netip.MustParsePrefix("10.253.244.3/32")
		if err := d.tun.(*tunnel.Windows).AddAddresses([]netip.Prefix{own}); err != nil {
			t.Fatal(err)
		}
		if source, err := winroute.ResolveTunnel(control.Next(), d.tun.LUID()); err != nil || source.Source != own.Addr() {
			t.Fatal("dual-role source mismatch", source, err)
		}
		other := c
		other.TUNName += "B"
		other.TUNAddresses = []netip.Prefix{netip.MustParsePrefix("10.253.246.1/24")}
		other.IPv4Pool = netip.MustParsePrefix("10.253.246.0/24")
		duplicate, err := newWindowsExitDevice(context.Background(), other, journal)
		if duplicate != nil {
			_ = duplicate.Close()
		}
		if err == nil || duplicate != nil {
			t.Fatal("live control journal shared", err)
		}
		if _, err := net.InterfaceByName(other.TUNName); err == nil {
			t.Fatal("failed creation leaked adapter")
		}
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		if err := winfirewall.RecoverControl(journal); err != nil {
			t.Fatal("closed control journal residue", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := net.InterfaceByName(c.TUNName); err != nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("Exit adapter survived Close")
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Logf("cycle=%d native Exit TUN/control readiness, source ownership, exclusive journal, rollback and teardown PASS", cycle)
	}
}
