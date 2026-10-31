// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/winroute"
)

func TestNativePhysicalInbound(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_INBOUND_TEST") != "1" {
		t.Skip("requires mini-PC inbound sender and isolated forwarded VM ports")
	}
	p, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(p.Binding.Source, 49507)))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	l, err := net.ListenTCP("tcp4", net.TCPAddrFromAddrPort(netip.AddrPortFrom(p.Binding.Source, 49508)))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	root := t.TempDir()
	udp, err := OpenOwnedInbound(filepath.Join(root, "udp"), p.Binding.LUID, netip.AddrPortFrom(p.Binding.Source, 49507), 17)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := udp.Close(); err != nil {
			t.Error(err)
		}
	})
	tcp, err := OpenOwnedInbound(filepath.Join(root, "tcp"), p.Binding.LUID, netip.AddrPortFrom(p.Binding.Source, 49508), 6)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tcp.Close(); err != nil {
			t.Error(err)
		}
	})
	blocked := func() {
		t.Helper()
		u.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err := u.ReadFromUDP(make([]byte, 128)); err == nil {
			t.Fatal("unsolicited UDP admitted without rule")
		} else if e, ok := err.(net.Error); !ok || !e.Timeout() {
			t.Fatal(err)
		}
		l.SetDeadline(time.Now().Add(2 * time.Second))
		if c, err := l.AcceptTCP(); err == nil {
			c.Close()
			t.Fatal("unsolicited TCP admitted without rule")
		} else if e, ok := err.(net.Error); !ok || !e.Timeout() {
			t.Fatal(err)
		}
	}
	blocked()
	if err := udp.Install(); err != nil {
		t.Fatal(err)
	}
	if err := tcp.Install(); err != nil {
		t.Fatal(err)
	}
	u.SetReadDeadline(time.Now().Add(8 * time.Second))
	b := make([]byte, 128)
	n, source, err := u.ReadFromUDP(b)
	if err != nil {
		t.Fatal("UDP permission", err)
	}
	if _, err := u.WriteToUDP(b[:n], source); err != nil {
		t.Fatal(err)
	}
	l.SetDeadline(time.Now().Add(8 * time.Second))
	for {
		c, err := l.AcceptTCP()
		if err != nil {
			t.Fatal("TCP permission", err)
		}
		c.SetDeadline(time.Now().Add(400 * time.Millisecond))
		_, err = c.Write([]byte("IRVIO-INBOUND-OK\n"))
		ack := make([]byte, 2)
		if err == nil {
			_, err = io.ReadFull(c, ack)
		}
		c.Close()
		if err == nil && string(ack) == "OK" {
			break
		}
	}
	if err := udp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tcp.Close(); err != nil {
		t.Fatal(err)
	}
	// Rebind only after revocation to exclude connections queued while the exception was valid.
	if err := u.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	u, err = net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(p.Binding.Source, 49507)))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	l, err = net.ListenTCP("tcp4", net.TCPAddrFromAddrPort(netip.AddrPortFrom(p.Binding.Source, 49508)))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	blocked()
	if err := RecoverControl(filepath.Join(root, "udp")); err != nil {
		t.Fatal(err)
	}
	if err := RecoverControl(filepath.Join(root, "tcp")); err != nil {
		t.Fatal(err)
	}
	t.Log("physical TCP/UDP blocked before install; exact inbound admitted; blocked after removal; journal cleanup PASS")
}
