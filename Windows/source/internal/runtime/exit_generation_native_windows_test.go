// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/windivert"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

func TestWindowsNativeExitGeneration(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_EXIT_GENERATION") != "1" || os.Getenv("IRVIO_DIVERT_TEST_SERVICE") != "running" {
		t.Skip("requires isolated SYSTEM VM with explicitly owned running driver")
	}
	oracle, err := netip.ParseAddr(os.Getenv("IRVIO_WINDOWS_NAT_DNS_ORACLE"))
	if err != nil || !oracle.Is4() || !oracle.IsGlobalUnicast() || oracle.IsPrivate() {
		t.Fatal("explicit public DNS oracle required")
	}
	path, err := winroute.ResolveUnderlay(oracle, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	question := func(id uint16) []byte {
		b := []byte{0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'o', 'r', 'g', 0, 0, 1, 0, 1}
		binary.BigEndian.PutUint16(b[:2], id)
		return b
	}
	var nonce [2]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	base := binary.BigEndian.Uint16(nonce[:])
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dialer := net.Dialer{Control: path.Binding.Control, Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "udp4", netip.AddrPortFrom(oracle, 53).String())
	if err != nil {
		t.Fatal("oracle preflight", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(question(base)); err != nil {
		t.Fatal(err)
	}
	answer := make([]byte, 4096)
	if n, err := conn.Read(answer); err != nil || n < 12 || binary.BigEndian.Uint16(answer[:2]) != base || answer[2]&0x80 == 0 {
		t.Fatal("oracle preflight", n, err)
	}
	conn.Close()
	c, binding := generationConfig(t)
	c.TUNName = fmt.Sprintf("IRVIO-G%d", os.Getpid())
	device, err := newWindowsExitDevice(ctx, c, filepath.Join(t.TempDir(), "control"))
	if device != nil {
		t.Cleanup(func() {
			if err := device.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	gen := generationForSource(t, path.Binding.Source)
	module, err := windivert.Load()
	if err != nil {
		t.Fatal(err)
	}
	g, err := newWindowsExitGeneration(ctx, c, device.tun, gen, module, []netip.Addr{path.Binding.Source, c.TUNAddresses[0].Addr()}, nil)
	if g != nil {
		t.Cleanup(func() {
			if err := g.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	var clients []generationClient
	for i := byte(2); i < 7; i++ {
		p := generationClientPeer(t, c, binding, gen, i, false)
		if err := g.AddDownstream(ctx, p.lease, p.binding, p.endpoint); err != nil {
			t.Fatal(err)
		}
		clients = append(clients, p)
	}
	t.Cleanup(func() {
		if t.Failed() && g.resources != nil && g.transport != nil {
			t.Logf("NAT=%+v error=%v bridge_drops=%d exit_bind=%+v exit_mux=%+v", g.resources.Stats(), g.resources.Err(), g.resources.Bridge().Dropped(), g.transport.Bind().Stats(), gen.Mux().Stats())
			for i, p := range clients {
				a, _ := g.transport.PeerStats(p.binding.Key)
				b, _ := p.transport.PeerStats(binding.Key)
				t.Logf("peer=%d exit=%+v client=%+v", i, a, b)
			}
		}
	})
	exchange := func(p generationClient, id uint16) {
		t.Logf("DNS_SEND lease=%d query=%d", p.lease.ID[0], id)
		reply := generationExchange(t, p.tun, generationPacket(p.lease.IPv4.Addr(), oracle, question(id)))
		if len(reply) < 40 || netip.AddrFrom4([4]byte(reply[12:16])) != oracle || netip.AddrFrom4([4]byte(reply[16:20])) != p.lease.IPv4.Addr() || binary.BigEndian.Uint16(reply[20:22]) != 53 || binary.BigEndian.Uint16(reply[22:24]) != 30000 || binary.BigEndian.Uint16(reply[28:30]) != id || reply[30]&0x80 == 0 || reply[31]&15 != 0 {
			t.Fatal("invalid encrypted DNS reply")
		}
		t.Logf("DNS_REPLY lease=%d query=%d", p.lease.ID[0], id)
	}
	for _, p := range clients {
		exchange(p, base+uint16(p.lease.ID[0]))
	}
	if err := g.RevokeLease(ctx, clients[0].lease, tunnellease.RevokeExplicitRelease); err != nil {
		t.Fatal(err)
	}
	exchange(clients[1], base+20)
	if err := g.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := net.InterfaceByName(c.TUNName); err == nil {
		t.Fatal("native Exit adapter residue")
	}
	t.Log("native_generation: five encrypted peers, six public DNS replies, isolated revoke, cleanup PASS")
}
