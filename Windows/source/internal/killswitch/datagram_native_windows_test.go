// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

func datagramMux(t *testing.T, local netip.AddrPort, classifier socketmux.Classifier) *socketmux.Mux {
	t.Helper()
	c := socketmux.DefaultConfig()
	c.IPv4Bind = local.Addr()
	c.IPv6Policy = socketmux.FamilyDisabled
	c.Port = int(local.Port())
	c.Classifier = classifier
	m, err := socketmux.New(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m
}

func datagramEcho(m *socketmux.Mux, target netip.AddrPort, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if _, err := m.Data().WriteTo(ctx, []byte(token), target); err != nil {
		return err
	}
	r, err := m.Data().Read(ctx)
	if err != nil {
		return err
	}
	if r.Source != target || string(r.Payload) != token {
		return fmt.Errorf("wrong UDP echo")
	}
	return nil
}

func TestWindowsDatagramProbeHelper(t *testing.T) {
	if os.Getenv("IRVIO_DATAGRAM_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	local, err := netip.ParseAddrPort(os.Getenv("IRVIO_DATAGRAM_LOCAL"))
	if err != nil {
		t.Fatal(err)
	}
	m := datagramMux(t, local, nil)
	fmt.Println("DATAGRAM_BOUND")
	target := netip.MustParseAddrPort("192.168.23.2:49505")
	err = datagramEcho(m, target, os.Getenv("IRVIO_DATAGRAM_TOKEN"))
	fmt.Printf("DATAGRAM_DELIVERED=%t\n", err == nil)
}

func TestWindowsNativeDatagramSockets(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_DATAGRAM_TEST") != "1" {
		t.Skip("SYSTEM VM and external UDP oracle required")
	}
	ctx := context.Background()
	c := nativeProtectionConfig(t)
	store, err := wfp.NewStore(c.Owner)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Inspect(ctx)
	if err != nil || prior.Installed {
		t.Fatal("preexisting protection", err)
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
	user, _ := nativeProbeUser(t)
	defer user.Close()
	registry, err := stunprobe.NewRegistry(stunprobe.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	m := datagramMux(t, netip.AddrPortFrom(path.Binding.Source, 0), socketmux.NewClassifierChain(stunprobe.NewClassifier(registry)))
	local, _ := m.LocalAddr(socketmux.IPv4)
	stun, err := stunprobe.NewClient(m.Control(), registry, stunprobe.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stun.Close(); err != nil {
			t.Error(err)
		}
	})
	target := netip.MustParseAddrPort("192.168.23.2:49505")
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
	o, err := u.OwnUnderlay(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := datagramEcho(m, target, "DENIED-before-grant"); err == nil {
		t.Fatal("UDP escaped before grant")
	}
	scope := WindowsDatagramSocket{Interface: path.Binding.LUID, Local: local}
	grant, err := o.AcquireDatagramSocket(ctx, 1, scope)
	if err != nil {
		t.Fatal(err)
	}
	for cycle := range 20 {
		for _, port := range []uint16{49505, 49506} {
			token := fmt.Sprintf("irvio-udp-%d-cycle-%d-port-%d", os.Getpid(), cycle, port)
			if err := datagramEcho(m, netip.AddrPortFrom(target.Addr(), port), token); err != nil {
				t.Fatal("owned UDP", cycle, err)
			}
		}
		bounded, cancel := context.WithTimeout(ctx, time.Second)
		result, err := stun.Probe(bounded, target)
		cancel()
		if err != nil || result.Local != local || !result.Mapped.IsValid() {
			t.Fatal("production STUN probe", cycle, result, err)
		}
	}
	for cycle := range 20 {
		if err := grant.Close(); err != nil || len(o.datagrams) != 0 || len(b.network.DatagramSockets) != 0 {
			t.Fatal("UDP lease lifecycle revoke", cycle, err)
		}
		grant, err = o.AcquireDatagramSocket(ctx, 1, scope)
		if err != nil {
			t.Fatal(err)
		}
		if err := datagramEcho(m, target, fmt.Sprintf("irvio-udp-%d-lifecycle-%d", os.Getpid(), cycle)); err != nil {
			t.Fatal(err)
		}
	}
	wrong := datagramMux(t, netip.AddrPortFrom(local.Addr(), 0), nil)
	if err := datagramEcho(wrong, target, "DENIED-wrong-local-port"); err == nil {
		t.Fatal("UDP grant escaped local socket")
	}
	if err := wrong.Close(); err != nil {
		t.Fatal(err)
	}
	if err := o.Advance(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := datagramEcho(m, target, "DENIED-stale-generation"); err == nil {
		t.Fatal("old UDP flow survived revoke")
	}
	if _, err := o.AcquireDatagramSocket(ctx, 1, scope); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("stale generation accepted", err)
	}
	if err := grant.Close(); err != nil {
		t.Fatal(err)
	}
	grant, err = o.AcquireDatagramSocket(ctx, 2, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := datagramEcho(m, target, fmt.Sprintf("irvio-udp-%d-new-generation", os.Getpid())); err != nil {
		t.Fatal(err)
	}
	if err := stun.Close(); err != nil {
		t.Fatal(err)
	}
	// Release the socket before its grant only here to simulate residue after a process crash.
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		binary, token string
		nonadmin      bool
	}{{self, "DENIED-nonadmin-reuse", true}, {filepath.Join(filepath.Dir(self), "foreign-datagram.test.exe"), "DENIED-foreign-app", false}} {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		started := time.Now()
		cmd := exec.CommandContext(bounded, tc.binary, "-test.run=^TestWindowsDatagramProbeHelper$", "-test.v")
		if tc.nonadmin {
			cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(user)}
		}
		cmd.Env = append(slicesWithoutProbeCredential(os.Environ()), "IRVIO_DATAGRAM_HELPER=1", "IRVIO_DATAGRAM_LOCAL="+local.String(), "IRVIO_DATAGRAM_TOKEN="+tc.token)
		out, err := cmd.CombinedOutput()
		contextErr := bounded.Err()
		cancel()
		if err != nil || !strings.Contains(string(out), "DATAGRAM_BOUND") || !strings.Contains(string(out), "DATAGRAM_DELIVERED=false") {
			t.Fatalf("stale grant misuse %s: elapsed=%s context=%v error=%v %s", tc.token, time.Since(started), contextErr, err, out)
		}
		t.Logf("stale socket reuse denied: %s elapsed=%s", tc.token, time.Since(started))
	}
	if err := grant.Close(); err != nil {
		t.Fatal(err)
	}
	if len(o.datagrams) != 0 || len(b.network.DatagramSockets) != 0 {
		t.Fatal("UDP grants retained")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := i.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	restored := datagramMux(t, netip.AddrPortFrom(local.Addr(), 0), nil)
	if err := datagramEcho(restored, target, fmt.Sprintf("irvio-udp-%d-restored", os.Getpid())); err != nil {
		t.Fatal("ordinary WAN not restored", err)
	}
	t.Log("40 UDP echoes, 20 production STUN probes, 20 grant lifecycles, wrong-port/foreign-app/nonadmin/stale-generation refusal and cleanup PASS")
}
