// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"encoding/binary"
	"fmt"
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

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"golang.org/x/sys/windows"
)

func TestWindowsTunnelControlProbeHelper(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_CONTROL_HELPER") != "1" {
		t.Skip("isolated pre-BIND packet probe")
	}
	if sid := os.Getenv("IRVIO_WINDOWS_WAN_SID"); sid != "" {
		token := windows.GetCurrentProcessToken()
		u, err := token.GetTokenUser()
		if err != nil || u.User.Sid.String() != sid {
			t.Fatal("wrong probe identity")
		}
		groups, err := token.GetTokenGroups()
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range groups.AllGroups() {
			if g.Sid.String() == "S-1-5-32-544" {
				t.Fatal("ordinary probe has administrator group")
			}
		}
		fmt.Println("CONTROL_NONADMIN_VERIFIED")
	}
	source := netip.MustParseAddrPort(os.Getenv("IRVIO_WINDOWS_CONTROL_SOURCE"))
	target := os.Getenv("IRVIO_WINDOWS_CONTROL_TARGET")
	proto := os.Getenv("IRVIO_WINDOWS_CONTROL_PROTOCOL")
	d := net.Dialer{Timeout: 1200 * time.Millisecond}
	if strings.HasPrefix(proto, "tcp") {
		d.LocalAddr = net.TCPAddrFromAddrPort(source)
	} else {
		d.LocalAddr = net.UDPAddrFromAddrPort(source)
	}
	conn, err := d.DialContext(context.Background(), proto, target)
	if err == nil {
		defer conn.Close()
		conn.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = conn.Write([]byte("irvio-prebind-packet-oracle"))
	}
	fmt.Println("CONTROL_PROBE_ATTEMPTED")
}

func TestWindowsNativePreBindControl(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_PROTECTION_TEST") != "1" {
		t.Skip("disposable SYSTEM VM with Wintun and non-admin probe user required")
	}
	ctx := context.Background()
	c := nativeProtectionConfig(t)
	store, err := wfp.NewStore(c.Owner)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Inspect(ctx)
	if err != nil || prior.Installed {
		t.Fatalf("preexisting native policy: %+v %v", prior, err)
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
	foreign := filepath.Join(filepath.Dir(self), "foreign-protected.test.exe")
	user, sid := nativeProbeUser(t)
	defer user.Close()
	owner, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-W05-BIND", Addresses: []netip.Prefix{
		netip.MustParsePrefix("10.253.245.1/24"), netip.MustParsePrefix("fd77:245::1/64"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	borrow, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[uint16]int{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf, sizes := make([]byte, 65535), []int{0}
		for {
			if _, err := borrow.Read([][]byte{buf}, sizes, 0); err != nil {
				return
			}
			p := buf[:sizes[0]]
			start := 0
			if len(p) >= 24 && p[0] == 0x45 && (p[9] == 6 || p[9] == 17) {
				start = 20
			} else if len(p) >= 44 && p[0]>>4 == 6 && (p[6] == 6 || p[6] == 17) {
				start = 40
			}
			if start != 0 {
				mu.Lock()
				seen[binary.BigEndian.Uint16(p[start:])]++
				mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() { borrow.Close(); wg.Wait() })
	var sourcePort uint16 = 60100
	denied := map[uint16]string{}
	probe := func(exe, stage, source, target, proto string, ordinary, want bool) {
		t.Helper()
		sourcePort++
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(pctx, exe, "-test.run=^TestWindowsTunnelControlProbeHelper$")
		cmd.Env = append(slicesWithoutProbeCredential(os.Environ()), "IRVIO_WINDOWS_CONTROL_HELPER=1",
			"IRVIO_WINDOWS_CONTROL_SOURCE="+netip.AddrPortFrom(netip.MustParseAddr(source), sourcePort).String(),
			"IRVIO_WINDOWS_CONTROL_TARGET="+target, "IRVIO_WINDOWS_CONTROL_PROTOCOL="+proto)
		if ordinary {
			cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(user)}
			cmd.Env = append(cmd.Env, "IRVIO_WINDOWS_WAN_SID="+sid)
		}
		started := time.Now()
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "CONTROL_PROBE_ATTEMPTED") ||
			(ordinary && !strings.Contains(string(out), "CONTROL_NONADMIN_VERIFIED")) {
			t.Fatalf("%s: probe failed: %v context=%v elapsed=%s %s", stage, err, pctx.Err(), time.Since(started), out)
		}
		time.Sleep(150 * time.Millisecond)
		mu.Lock()
		count := seen[sourcePort]
		mu.Unlock()
		if (count > 0) != want {
			t.Fatalf("%s: TUN packets=%d want=%v", stage, count, want)
		}
		if !want {
			denied[sourcePort] = stage
		}
		t.Logf("%s: TUN packets=%d permitted=%v", stage, count, want)
	}
	for _, family := range []struct{ source, target, suffix string }{
		{"10.253.245.1", "10.253.245.2:51822", "4"},
		{"fd77:245::1", "[fd77:245::2]:51822", "6"},
	} {
		probe(self, "baseline-"+family.suffix, family.source, family.target, "tcp"+family.suffix, false, true)
		probe(foreign, "baseline-foreign-"+family.suffix, family.source, family.target, "tcp"+family.suffix, false, true)
		probe(self, "baseline-ordinary-"+family.suffix, family.source, family.target, "tcp"+family.suffix, true, true)
		c.Network.TunnelControl = append(c.Network.TunnelControl, WindowsTunnelControl{
			Interface: owner.LUID(), Source: netip.MustParseAddr(family.source), Remote: netip.MustParseAddrPort(family.target),
		})
	}
	b, err := NewWindowsBackend(c)
	if err != nil {
		t.Fatal(err)
	}
	i, _, err := b.Ensure(ctx, c.Policy)
	if err != nil {
		t.Fatal(err)
	}
	u, err := b.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range c.Network.TunnelControl {
		suffix := "4"
		if e.Source.Is6() {
			suffix = "6"
		}
		probe(self, "control-"+suffix, e.Source.String(), e.Remote.String(), "tcp"+suffix, false, true)
		probe(foreign, "foreign-app-"+suffix, e.Source.String(), e.Remote.String(), "tcp"+suffix, false, false)
		probe(self, "nonadmin-"+suffix, e.Source.String(), e.Remote.String(), "tcp"+suffix, true, false)
		probe(self, "wrong-port-"+suffix, e.Source.String(), netip.AddrPortFrom(e.Remote.Addr(), 51823).String(), "tcp"+suffix, false, false)
		probe(self, "wrong-address-"+suffix, e.Source.String(), netip.AddrPortFrom(e.Remote.Addr().Next(), 51822).String(), "tcp"+suffix, false, false)
		probe(self, "udp-blocked-"+suffix, e.Source.String(), e.Remote.String(), "udp"+suffix, false, false)
	}
	if err := u.Update(ctx, WindowsNetwork{}); err != nil {
		t.Fatal(err)
	}
	for _, e := range c.Network.TunnelControl {
		suffix := "4"
		if e.Source.Is6() {
			suffix = "6"
		}
		probe(self, "revoked-"+suffix, e.Source.String(), e.Remote.String(), "tcp"+suffix, false, false)
	}
	if err := u.Update(ctx, WindowsNetwork{TunnelLUID: owner.LUID()}); err != nil {
		t.Fatal(err)
	}
	for _, e := range c.Network.TunnelControl {
		suffix := "4"
		if e.Source.Is6() {
			suffix = "6"
		}
		probe(self, "promoted-ordinary-"+suffix, e.Source.String(), e.Remote.String(), "tcp"+suffix, true, true)
	}
	if err := i.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	borrow.Close()
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	for port, stage := range denied {
		if seen[port] != 0 {
			t.Errorf("late forbidden packets: stage=%s port=%d count=%d", stage, port, seen[port])
		}
	}
}
