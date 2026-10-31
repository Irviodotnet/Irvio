// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/wintest"
)

func TestNativeSessionFixedPortLifecycle(t *testing.T) {
	if os.Getenv("IRVIO_SESSION_FIXED_PORT_TEST") != "1" {
		t.Skip("requires an isolated elevated Windows VM with no active IRVIO runtime")
	}
	for cycle := 0; cycle < 100; cycle++ {
		if !t.Run(fmt.Sprint(cycle), func(t *testing.T) {
			owner, err := tunnel.NewWindows(tunnel.Config{Name: sessionTUNName,
				Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.248.1/24")}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := owner.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := owner.SetAutomaticSource(owner.Addresses()[0], false); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			listener, err := ListenTunnel(ctx, "tcp4", "10.253.248.1:51822", owner.Name())
			if err != nil {
				capture, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				output, diagnostic := exec.CommandContext(capture, "netsh.exe", "interface", "ipv4", "show", "excludedportrange", "protocol=tcp").CombinedOutput()
				t.Fatalf("listen cycle %d: %v; exclusion oracle: %v\n%s", cycle, err, diagnostic, output)
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
		}) {
			return
		}
		if (cycle+1)%10 == 0 {
			runtime.GC()
			t.Logf("idle cycle=%d handle_types=%v goroutines=%d", cycle+1, wintest.HandleTypes(), runtime.NumGoroutine())
		}
	}
}
