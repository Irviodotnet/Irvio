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

// Real conntrack replies, both IP families; no network outside this namespace.
func TestKernelDownloadOnlyLocalUserReplies(t *testing.T) {
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
	ip("-6", "addr", "add", "fd77::1/64", "dev", "hydra0", "nodad")
	ip("link", "add", "wan0", "type", "dummy")
	defer ip("link", "del", "wan0")
	ip("link", "set", "wan0", "up")
	tun, _ := net.InterfaceByName("hydra0")
	inject, _ := net.InterfaceByName("inject0")
	ip("neigh", "replace", "10.77.0.2", "lladdr", inject.HardwareAddr.String(), "dev", "hydra0", "nud", "permanent")
	ip("-6", "neigh", "replace", "fd77::2", "lladdr", inject.HardwareAddr.String(), "dev", "hydra0", "nud", "permanent")
	ctx := context.Background()
	ks, _, err := (killswitch.NFTBackend{}).Ensure(ctx, killswitch.Policy{TUNName: "hydra0", BypassMark: linuxroute.IrvioBypassMark})
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Remove(ctx)
	fw, err := (exitfirewall.NFTBackend{}).Apply(ctx, exitfirewall.Policy{TUNName: "hydra0", WANName: "wan0", ClientIPv4: netip.MustParsePrefix("10.77.0.0/24"), ClientIPv6: netip.MustParsePrefix("fd77::/64")})
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()
	addresses := []netip.Addr{netip.MustParseAddr("10.77.0.1"), netip.MustParseAddr("fd77::1")}
	if err := fw.SetLocalTunnelAddresses(ctx, addresses); err != nil {
		t.Fatal(err)
	}
	reader := fw.(interface{ DownloadBytes() (uint64, error) })
	read := func() uint64 {
		t.Helper()
		n, e := reader.DownloadBytes()
		if e != nil {
			t.Fatal(e)
		}
		return n
	}
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	checksum := func(data []byte) uint16 {
		var sum uint32
		for i := 0; i+1 < len(data); i += 2 {
			sum += uint32(binary.BigEndian.Uint16(data[i:]))
		}
		if len(data)%2 != 0 {
			sum += uint32(data[len(data)-1]) << 8
		}
		for sum > 65535 {
			sum = (sum & 65535) + (sum >> 16)
		}
		return ^uint16(sum)
	}
	for _, v6 := range []bool{false, true} {
		for _, mark := range []uint32{0, linuxroute.IrvioTunnelControlMark, linuxroute.IrvioBypassMark} {
			network, local, remote := "udp4", "10.77.0.1", "10.77.0.2"
			if v6 {
				network, local, remote = "udp6", "fd77::1", "fd77::2"
			}
			c, e := net.ListenUDP(network, &net.UDPAddr{IP: net.ParseIP(local)})
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := c.SyscallConn()
			var sockErr error
			e = raw.Control(func(s uintptr) { sockErr = unix.SetsockoptInt(int(s), unix.SOL_SOCKET, unix.SO_MARK, int(mark)) })
			if e != nil || sockErr != nil {
				t.Fatalf("mark: %v %v", e, sockErr)
			}
			if _, e = c.WriteToUDP([]byte{1}, &net.UDPAddr{IP: net.ParseIP(remote), Port: 18081}); e != nil {
				t.Fatal(e)
			}
			udp := make([]byte, 108)
			binary.BigEndian.PutUint16(udp, 18081)
			binary.BigEndian.PutUint16(udp[2:], uint16(c.LocalAddr().(*net.UDPAddr).Port))
			binary.BigEndian.PutUint16(udp[4:], 108)
			header := make([]byte, 20)
			ether := uint16(0x0800)
			if v6 {
				ether = 0x86dd
				header = make([]byte, 40)
				header[0] = 0x60
				header[6] = 17
				header[7] = 64
				binary.BigEndian.PutUint16(header[4:], 108)
				copy(header[8:24], net.ParseIP(remote).To16())
				copy(header[24:40], net.ParseIP(local).To16())
				pseudo := append([]byte(nil), header[8:40]...)
				pseudo = append(pseudo, 0, 0, 0, 108, 0, 0, 0, 17)
				pseudo = append(pseudo, udp...)
				check := checksum(pseudo)
				if check == 0 {
					check = 65535
				}
				binary.BigEndian.PutUint16(udp[6:], check)
			} else {
				header[0] = 0x45
				header[8] = 64
				header[9] = 17
				binary.BigEndian.PutUint16(header[2:], 128)
				copy(header[12:16], net.ParseIP(remote).To4())
				copy(header[16:20], net.ParseIP(local).To4())
				binary.BigEndian.PutUint16(header[10:], checksum(header))
			}
			packet := make([]byte, 14)
			copy(packet, tun.HardwareAddr)
			copy(packet[6:], inject.HardwareAddr)
			binary.BigEndian.PutUint16(packet[12:], ether)
			packet = append(append(packet, header...), udp...)
			before := read()
			if e = unix.Sendto(fd, packet, 0, &unix.SockaddrLinklayer{Ifindex: inject.Index}); e != nil {
				t.Fatal(e)
			}
			c.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, 128)
			n, _, e := c.ReadFromUDP(buf)
			c.Close()
			if e != nil || n != 100 {
				t.Fatalf("reply v6=%v mark=%x n=%d err=%v", v6, mark, n, e)
			}
			want := uint64(len(header) + len(udp))
			if mark != 0 {
				want = 0
			}
			if got := read() - before; got != want {
				t.Fatalf("v6=%v mark=%x bytes=%d want=%d", v6, mark, got, want)
			}
		}
	}
	if got := read(); got != 276 {
		t.Fatalf("total=%d", got)
	}
	if err := fw.SetLocalTunnelAddresses(ctx, addresses); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 276 {
		t.Fatalf("address reconcile reset=%d", got)
	}
	if err := fw.SetLocalTunnelAddresses(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 276 {
		t.Fatalf("final bytes=%d", got)
	}
}
