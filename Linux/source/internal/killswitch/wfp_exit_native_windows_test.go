// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/sessioncontrol"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winfirewall"
	"golang.org/x/sys/windows"
)

func exitControlTCP(source, target netip.Addr, sport, dport uint16, seq, ack uint32, flags byte) []byte {
	p := make([]byte, 40)
	p[0], p[8], p[9] = 0x45, 64, 6
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	copy(p[12:16], source.AsSlice())
	copy(p[16:20], target.AsSlice())
	binary.BigEndian.PutUint16(p[20:22], sport)
	binary.BigEndian.PutUint16(p[22:24], dport)
	binary.BigEndian.PutUint32(p[24:28], seq)
	binary.BigEndian.PutUint32(p[28:32], ack)
	p[32], p[33] = 0x50, flags
	binary.BigEndian.PutUint16(p[34:36], 65535)
	checksum := func(b []byte) uint16 {
		var sum uint32
		for i := 0; i < len(b); i += 2 {
			sum += uint32(binary.BigEndian.Uint16(b[i:]))
		}
		for sum>>16 != 0 {
			sum = (sum & 65535) + (sum >> 16)
		}
		return ^uint16(sum)
	}
	binary.BigEndian.PutUint16(p[10:12], checksum(p[:20]))
	pseudo := append(append([]byte(nil), p[12:20]...), 0, 6, 0, 20)
	pseudo = append(pseudo, p[20:]...)
	binary.BigEndian.PutUint16(p[36:38], checksum(pseudo))
	return p
}

func TestWindowsNativeExitControlReplies(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_EXIT_CONTROL_TEST") != "1" {
		t.Skip("isolated SYSTEM VM and owned protection fixture required")
	}
	ctx := context.Background()
	c := nativeProtectionConfig(t)
	store, err := wfp.NewStore(c.Owner)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Inspect(ctx)
	if err != nil || prior.Installed {
		t.Fatal("preexisting protection", prior, err)
	}
	t.Cleanup(func() {
		if err := store.Remove(ctx); err != nil {
			t.Error(err)
		}
	})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c.Network.Application, err = wfp.ApplicationIDForPath(exe)
	if err != nil {
		t.Fatal(err)
	}
	wan := func(stage string, want bool) {
		t.Helper()
		if os.Getenv("IRVIO_WINDOWS_EXIT_CONTROL_WAN_TEST") != "1" {
			t.Log("WAN_ORACLE_NOT_EXERCISED")
			return
		}
		for _, path := range []string{exe, filepath.Join(filepath.Dir(exe), "foreign-protected.test.exe")} {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			command := exec.CommandContext(cctx, path, "-test.run=^TestWindowsWANProbeHelper$")
			command.Env = append(slicesWithoutProbeCredential(os.Environ()), "IRVIO_WINDOWS_WAN_HELPER=1", "IRVIO_WINDOWS_WAN_STAGE=exit-control-"+stage)
			out, err := command.CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("WAN probe: %v %s", err, out)
			}
			var result map[string]bool
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "WAN_RESULT ") {
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "WAN_RESULT ")), &result); err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(result) != 2 || result["tcp"] != want || result["udp"] != want {
				t.Fatal("WAN permission mismatch", stage, path, result, want)
			}
			t.Logf("%s %s TCP/UDP allowed=%v", stage, filepath.Base(path), want)
		}
	}
	local, client := netip.MustParseAddr("10.253.245.1"), netip.MustParseAddr("10.253.245.2")
	owner, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-Reply", Addresses: []netip.Prefix{netip.PrefixFrom(local, 24)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	for i, port := range []uint16{7890, 7891} {
		id := windows.GUID{Data1: uint32(0x128adc01 + i), Data2: 0x7110, Data3: 0x4aa0, Data4: [8]byte{0x85, 0x11, 0x29, 0x3a, 0x15, 0x08, 0x19, 0x02}}
		rule, err := winfirewall.NewControl(id, owner.LUID(), netip.AddrPortFrom(local, port), netip.MustParsePrefix("10.253.245.0/24"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := rule.Close(); err != nil {
				t.Error(err)
			}
		})
		if err := rule.Install(); err != nil {
			t.Fatal(err)
		}
		listener, err := sessioncontrol.ListenTunnel(ctx, "tcp4", netip.AddrPortFrom(local, port).String(), owner.Name())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
	}
	device, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[[2]uint16][][]byte{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf, sizes := make([]byte, 65535), []int{0}
		for {
			if _, err := device.Read([][]byte{buf}, sizes, 0); err != nil {
				return
			}
			p := buf[:sizes[0]]
			if len(p) < 40 || p[0] != 0x45 || p[9] != 6 {
				continue
			}
			key := [2]uint16{binary.BigEndian.Uint16(p[20:22]), binary.BigEndian.Uint16(p[22:24])}
			mu.Lock()
			seen[key] = append(seen[key], append([]byte(nil), p...))
			mu.Unlock()
		}
	}()
	t.Cleanup(func() { device.Close(); <-done })
	count := func(key [2]uint16) (int, []byte) {
		mu.Lock()
		defer mu.Unlock()
		if len(seen[key]) == 0 {
			return 0, nil
		}
		return len(seen[key]), seen[key][0]
	}
	var next uint16 = 61000
	denied := map[[2]uint16]string{}
	probe := func(stage string, port uint16, remote netip.Addr, want bool) {
		t.Helper()
		next++
		seq := uint32(next) * 100
		p := exitControlTCP(remote, local, next, port, seq, 0, 2)
		if n, err := device.Write([][]byte{p}, 0); err != nil || n != 1 {
			t.Fatal("inject SYN", n, err)
		}
		key := [2]uint16{port, next}
		deadline := time.Now().Add(1200 * time.Millisecond)
		for time.Now().Before(deadline) {
			if n, _ := count(key); n > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		n, reply := count(key)
		if (n != 0) != want {
			t.Fatalf("%s: reply packets=%d want=%v", stage, n, want)
		}
		if want {
			if reply[33]&0x12 != 0x12 || binary.BigEndian.Uint32(reply[28:32]) != seq+1 || netip.AddrFrom4([4]byte(reply[12:16])) != local || netip.AddrFrom4([4]byte(reply[16:20])) != remote {
				t.Fatal("invalid SYNACK", stage)
			}
			rst := exitControlTCP(remote, local, next, port, seq+1, binary.BigEndian.Uint32(reply[24:28])+1, 0x14)
			if _, err := device.Write([][]byte{rst}, 0); err != nil {
				t.Fatal(err)
			}
		} else {
			denied[key] = stage
			rst := exitControlTCP(remote, local, next, port, seq+1, 0, 4)
			if _, err := device.Write([][]byte{rst}, 0); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("%s: SYNACK=%v", stage, want)
	}
	ordinary := func(stage string, want bool) {
		t.Helper()
		next++
		d := net.Dialer{Timeout: 250 * time.Millisecond, LocalAddr: net.TCPAddrFromAddrPort(netip.AddrPortFrom(local, next))}
		conn, _ := d.DialContext(ctx, "tcp4", netip.AddrPortFrom(client, 443).String())
		if conn != nil {
			conn.Close()
			t.Fatal("unanswered host probe connected")
		}
		key := [2]uint16{next, 443}
		n, _ := count(key)
		if (n > 0) != want {
			t.Fatalf("%s: ordinary packets=%d want=%v", stage, n, want)
		}
		if !want {
			denied[key] = stage
		}
		t.Logf("%s: ordinary packets=%d", stage, n)
	}
	outside := netip.MustParseAddr("10.253.245.130")
	probe("baseline-control", 7890, client, true)
	probe("baseline-other-port", 7891, client, true)
	probe("baseline-outside-pool", 7890, outside, true)
	ordinary("baseline-host", true)
	wan("baseline", true)
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
	probe("protected-without-Exit-grant", 7890, client, false)
	grant := &WindowsExitControl{Interface: owner.LUID(), Local: netip.AddrPortFrom(local, 7890), Clients: netip.MustParsePrefix("10.253.245.0/25")}
	if err := u.UpdateExitControl(ctx, grant); err != nil {
		t.Fatal(err)
	}
	probe("protected-control", 7890, client, true)
	probe("protected-wrong-port", 7891, client, false)
	probe("protected-outside-pool", 7890, outside, false)
	ordinary("protected-host", false)
	wan("protected", false)
	prebind := []WindowsTunnelControl{{Interface: owner.LUID(), Source: netip.MustParseAddr("10.253.245.3"), Remote: netip.MustParseAddrPort("10.253.245.129:51822")}}
	if err := u.UpdateTunnel(ctx, 0, prebind); err != nil {
		t.Fatal(err)
	}
	probe("own-prebind-control", 7890, client, true)
	ordinary("own-prebind-host", false)
	if err := u.UpdateUnderlay(ctx, c.Network.Application, nil); err != nil {
		t.Fatal(err)
	}
	probe("underlay-reconcile-control", 7890, client, true)
	if err := u.UpdateTunnel(ctx, owner.LUID(), nil); err != nil {
		t.Fatal(err)
	}
	probe("own-connected-control", 7890, client, true)
	if err := u.UpdateTunnel(ctx, 0, nil); err != nil {
		t.Fatal(err)
	}
	probe("own-recovering-control", 7890, client, true)
	ordinary("own-recovering-host", false)
	wan("recovering", false)
	if err := u.UpdateExitControl(ctx, nil); err != nil {
		t.Fatal(err)
	}
	probe("Exit-withdrawn", 7890, client, false)
	for key, stage := range denied {
		if n, _ := count(key); n != 0 {
			t.Fatal("late forbidden packet", stage, n)
		}
	}
	if err := i.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	probe("cleanup-restored", 7890, client, true)
	wan("restored", true)
	t.Log("real TCP Exit replies verified; no full daemon/ICE acceptance claimed")
}
