// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package trafficstats_test

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"github.com/Irviodotnet/Irvio/internal/exitfirewall"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
)

// Use isolated mini-PC namespaces; dummy links measure accepted IP bytes, while product tests verify delivery.
func TestKernelUserBytesExcludeControlAndSafetyDrops(t *testing.T) {
	if os.Getenv("IRVIO_STATS_KERNEL_TEST") != "1" {
		t.Skip("requires isolated privileged netns")
	}
	self, _ := os.Readlink("/proc/self/ns/net")
	init, _ := os.Readlink("/proc/1/ns/net")
	if self == "" || self == init || os.Geteuid() != 0 {
		t.Fatal("refuse non-isolated kernel test")
	}
	ip := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %s %v", args, out, err)
		}
	}
	ip("link", "set", "lo", "up")
	ip("link", "add", "hydra0", "type", "dummy")
	defer ip("link", "del", "hydra0")
	ip("link", "set", "hydra0", "up")
	ip("addr", "add", "198.18.222.1/24", "dev", "hydra0")
	ip("-6", "addr", "add", "fd77::1/64", "dev", "hydra0", "nodad")
	ctx := context.Background()
	installation, _, err := (killswitch.NFTBackend{}).Ensure(ctx, killswitch.Policy{TUNName: "hydra0", BypassMark: linuxroute.IrvioBypassMark})
	if err != nil {
		t.Fatal(err)
	}
	defer installation.Remove(ctx)
	reader := installation.(interface{ TrafficBytes() (uint64, error) })
	read := func() uint64 {
		t.Helper()
		value, err := reader.TrafficBytes()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	send := func(network, target string, mark uint32) {
		t.Helper()
		c, err := net.ListenUDP(network, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		raw, err := c.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var markErr error
		if err := raw.Control(func(fd uintptr) { markErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(mark)) }); err != nil {
			t.Fatal(err)
		}
		if markErr != nil {
			t.Fatal(markErr)
		}
		addr, err := net.ResolveUDPAddr(network, target)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = c.WriteToUDP(make([]byte, 100), addr)
	}
	if got := read(); got != 0 {
		t.Fatalf("initial=%d", got)
	}
	send("udp4", "198.18.222.2:18081", 0)
	send("udp6", "[fd77::2]:18081", 0)
	if got := read(); got != 128+148 {
		t.Fatalf("inner IPv4/IPv6 bytes=%d want=276", got)
	}
	for _, mark := range []uint32{linuxroute.IrvioBypassMark, linuxroute.IrvioTunnelControlMark} {
		send("udp4", "198.18.222.2:18081", mark)
		send("udp6", "[fd77::2]:18081", mark)
	}
	send("udp4", "198.18.222.2:1900", 0)
	send("udp6", "[fd77::2]:11211", 0)
	if got := read(); got != 276 {
		t.Fatalf("control/dropped bytes counted: %d", got)
	}
	if err := installation.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 276 {
		t.Fatalf("reconcile reset counter: %d", got)
	}
	if err := installation.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 276 {
		t.Fatalf("final counter lost: %d", got)
	}
}

func TestKernelSharedBytesSurviveWANReplacement(t *testing.T) {
	if os.Getenv("IRVIO_STATS_KERNEL_TEST") != "1" {
		t.Skip("requires isolated privileged netns")
	}
	self, _ := os.Readlink("/proc/self/ns/net")
	init, _ := os.Readlink("/proc/1/ns/net")
	if self == "" || self == init || os.Geteuid() != 0 {
		t.Fatal("refuse non-isolated kernel test")
	}
	ip := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %s %v", args, out, err)
		}
	}
	ip("link", "add", "hydra0", "type", "veth", "peer", "name", "inject0")
	defer ip("link", "del", "hydra0")
	ip("link", "set", "hydra0", "up")
	ip("link", "set", "inject0", "up")
	ip("addr", "add", "10.77.0.1/24", "dev", "hydra0")
	ip("link", "add", "wan0", "type", "dummy")
	wanName := "wan0"
	defer func() { ip("link", "del", wanName) }()
	ip("link", "set", "wan0", "up")
	ip("addr", "add", "198.18.222.1/24", "dev", "wan0")
	ip("neigh", "add", "198.18.222.2", "lladdr", "02:00:00:00:00:22", "dev", "wan0", "nud", "permanent")
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	firewall, err := (exitfirewall.NFTBackend{}).Apply(ctx, exitfirewall.Policy{
		TUNName: "hydra0", WANName: "wan0", ClientIPv4: netip.MustParsePrefix("10.77.0.0/24"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer firewall.Close()
	reader := firewall.(interface{ TrafficBytes() (uint64, error) })
	read := func() uint64 {
		t.Helper()
		n, err := reader.TrafficBytes()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	inject, _ := net.InterfaceByName("inject0")
	tun, _ := net.InterfaceByName("hydra0")
	if inject == nil || tun == nil {
		t.Fatal("missing interfaces")
	}
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, int(0x0008))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	send := func(port uint16) {
		t.Helper()
		packet := make([]byte, 14+20+8+100)
		copy(packet[:6], tun.HardwareAddr)
		copy(packet[6:12], inject.HardwareAddr)
		binary.BigEndian.PutUint16(packet[12:14], 0x0800)
		h := packet[14:34]
		h[0], h[8], h[9] = 0x45, 64, 17
		binary.BigEndian.PutUint16(h[2:4], 128)
		copy(h[12:16], []byte{10, 77, 0, 2})
		copy(h[16:20], []byte{198, 18, 222, 2})
		var sum uint32
		for i := 0; i < len(h); i += 2 {
			sum += uint32(binary.BigEndian.Uint16(h[i : i+2]))
		}
		for sum > 0xffff {
			sum = (sum & 0xffff) + sum>>16
		}
		binary.BigEndian.PutUint16(h[10:12], ^uint16(sum))
		binary.BigEndian.PutUint16(packet[34:36], 30000)
		binary.BigEndian.PutUint16(packet[36:38], port)
		binary.BigEndian.PutUint16(packet[38:40], 108)
		if err := unix.Sendto(fd, packet, 0, &unix.SockaddrLinklayer{Ifindex: inject.Index, Protocol: 0x0008}); err != nil {
			t.Fatal(err)
		}
	}
	send(18081)
	for deadline := time.Now().Add(time.Second); read() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if got := read(); got != 128 {
		t.Fatalf("forwarded IP bytes=%d want=128", got)
	}
	send(1900)
	time.Sleep(20 * time.Millisecond)
	if got := read(); got != 128 {
		t.Fatalf("blocked forward counted: %d", got)
	}
	ip("link", "set", "wan0", "name", "wan1")
	wanName = "wan1"
	if err := firewall.(exitfirewall.WANPathReconciler).SetWANPath(ctx, "wan1", netip.Addr{}, netip.Addr{}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 128 {
		t.Fatalf("WAN rule replacement reset bytes: %d", got)
	}
	send(18081)
	for deadline := time.Now().Add(time.Second); read() == 128 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if got := read(); got != 256 {
		t.Fatalf("forwarding after WAN replacement: %d", got)
	}
	if err := firewall.Close(); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 256 {
		t.Fatalf("final forwarded count lost: %d", got)
	}
	download, err := firewall.(interface{ DownloadBytes() (uint64, error) }).DownloadBytes()
	if err != nil || download != 0 {
		t.Fatalf("forwarded traffic counted as own download: %d %v", download, err)
	}
}
