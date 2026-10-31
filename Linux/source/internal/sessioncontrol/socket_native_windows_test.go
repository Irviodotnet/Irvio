// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

const sessionTUNName = "IRVIO-Session"

func sessionNativeOwner(t *testing.T) *tunnel.Windows {
	t.Helper()
	if os.Getenv("IRVIO_SESSION_NATIVE_TEST") != "1" {
		t.Skip("requires isolated elevated Windows VM and verified Wintun DLL")
	}
	owner, err := tunnel.NewWindows(tunnel.Config{Name: sessionTUNName, Addresses: []netip.Prefix{
		netip.MustParsePrefix("10.253.248.1/24"), netip.MustParsePrefix("fd77:248::1/64"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	return owner
}

func TestNativeSessionTunnelDial(t *testing.T) {
	owner := sessionNativeOwner(t)
	dial := TunnelDialer(owner.Name())
	device, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { device.Close() })
	packets := make(chan []byte, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer, sizes := make([]byte, 65535), make([]int, 1)
		for {
			if _, err := device.Read([][]byte{buffer}, sizes, 0); err != nil {
				return
			}
			select {
			case packets <- append([]byte(nil), buffer[:sizes[0]]...):
			default:
			}
		}
	}()
	t.Cleanup(func() {
		device.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("TUN read did not stop")
		}
	})
	for _, target := range []string{"10.253.248.2", "fd77:248::2"} {
		t.Run(target, func(t *testing.T) {
			ip := netip.MustParseAddr(target)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				conn, err := dial(ctx, "tcp", net.JoinHostPort(target, "49194"))
				if conn != nil {
					conn.Close()
					result <- fmt.Errorf("unanswered TUN SYN unexpectedly connected")
					return
				}
				result <- err
			}()
			found := false
			for !found {
				select {
				case packet := <-packets:
					found = sessionSYN(packet, ip)
				case err := <-result:
					t.Fatalf("dial ended before TUN SYN: %v", err)
				case <-ctx.Done():
					t.Fatal("no TCP SYN on specified TUN")
				}
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled TUN dial: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled TUN dial did not return")
			}
			t.Log("TCP SYN observed only through explicitly selected TUN; cancellation PASS")
		})
	}
	if _, err := winroute.ResolveTunnel(netip.MustParseAddr("192.168.23.2"), owner.LUID()); err == nil {
		t.Fatal("unrouted remote target silently used WAN/default route")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if conn, err := dial(ctx, "tcp", "192.168.23.2:49194"); err == nil || conn != nil {
		t.Fatal("tunnel dial silently fell back to a physical route")
	}
}

func sessionSYN(p []byte, target netip.Addr) bool {
	offset := 40
	if target.Is4() {
		if len(p) < 40 || p[0]>>4 != 4 || p[9] != 6 || netip.AddrFrom4([4]byte(p[16:20])) != target || netip.AddrFrom4([4]byte(p[12:16])).String() != "10.253.248.1" {
			return false
		}
		offset = int(p[0]&15) * 4
	} else if len(p) < 60 || p[0]>>4 != 6 || p[6] != 6 || netip.AddrFrom16([16]byte(p[24:40])) != target || netip.AddrFrom16([16]byte(p[8:24])).String() != "fd77:248::1" {
		return false
	}
	return offset >= 20 && len(p) >= offset+20 && binary.BigEndian.Uint16(p[offset+2:offset+4]) == 49194 && p[offset+13]&0x12 == 0x02
}

func TestNativeSessionTunnelListenerNoise(t *testing.T) {
	owner := sessionNativeOwner(t)
	for _, source := range []string{"10.253.248.1", "fd77:248::1"} {
		t.Run(source, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			listener, err := ListenTunnel(ctx, "tcp", net.JoinHostPort(source, "0"), owner.Name())
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			raw, err := listener.(*net.TCPListener).SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			index, err := winroute.SocketInterface(raw, netip.MustParseAddr(source).Is4())
			if err != nil || index != uint32(owner.IfIndex()) {
				t.Fatalf("listener interface: %d %v", index, err)
			}
			exitKey, exitID := sessionIdentity(t)
			clientKey, clientID := sessionIdentity(t)
			authority := &testAuthority{lease: testLease(exitID, clientID), releaseDone: make(chan struct{})}
			server, err := NewServer(listener, exitKey, authority, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			// Local kernel loopback tests listener/Noise only; the dial test separately inspects remote TUN SYNs.
			client, err := Dial(ctx, listener.Addr().String(), clientKey, exitID, authority.lease, (&net.Dialer{}).DialContext, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			lease, err := client.Renew(ctx, authority.lease)
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Release(ctx, lease); err != nil {
				t.Fatal(err)
			}
			client.Close()
			select {
			case <-authority.releaseDone:
			case <-ctx.Done():
				t.Fatal("lease release failed")
			}
			t.Log("exact-address native listener + Noise BIND/RENEW/RELEASE PASS")
		})
	}
	if _, err := ListenTunnel(context.Background(), "tcp", "127.0.0.1:0", owner.Name()); err == nil {
		t.Fatal("non-TUN source accepted")
	}
	physical, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, []uint64{owner.LUID()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ListenTunnel(context.Background(), "tcp", net.JoinHostPort(physical.Binding.Source.String(), "0"), owner.Name()); err == nil {
		t.Fatal("physical source accepted by TUN listener")
	}
	if _, err := captureTunnelInterface(physical.Name); err == nil {
		t.Fatal("physical NIC accepted as tunnel")
	}
	if _, err := winroute.ResolveTunnel(netip.MustParseAddr("192.168.23.2"), physical.Binding.LUID); err == nil {
		t.Fatal("physical NIC accepted for tunnel dial")
	}
}

func TestNativeSessionTunnelIdentityFence(t *testing.T) {
	owner := sessionNativeOwner(t)
	dial := TunnelDialer(owner.Name())
	identity, err := captureTunnelInterface(owner.Name())
	if err != nil {
		t.Fatal(err)
	}
	wrong := identity
	wrong.guid = windows.GUID{Data1: 1}
	if err := wrong.validate(); err == nil {
		t.Fatal("changed GUID accepted")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := identity.validate(); err == nil {
		t.Fatal("removed interface accepted")
	}
	replacement := sessionNativeOwner(t)
	next, err := captureTunnelInterface(replacement.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("replacement old LUID=%d index=%d GUID=%s; new LUID=%d index=%d GUID=%s", identity.luid, identity.index, identity.guid, next.luid, next.index, next.guid)
	if next == identity {
		t.Fatal("fixture reused the complete native identity")
	}
	if conn, err := dial(context.Background(), "tcp", "10.253.248.2:49194"); err == nil || conn != nil {
		t.Fatal("old dialer followed a replacement interface")
	}
	if _, err := captureTunnelInterface(replacement.Name()); err != nil {
		t.Fatal("replacement is not independently usable", err)
	}
	for _, family := range []string{"0.0.0.0:0", "[::]:0"} {
		if l, err := ListenTunnel(context.Background(), "tcp", family, replacement.Name()); err == nil || l != nil {
			t.Fatal("wildcard listener accepted")
		}
	}
}
