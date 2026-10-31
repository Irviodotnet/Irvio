// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitbridge

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/winfirewall"
)

func bridgeChecksum(p []byte) uint16 {
	var sum uint32
	for len(p) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(p))
		p = p[2:]
	}
	if len(p) != 0 {
		sum += uint32(p[0]) << 8
	}
	for sum > 65535 {
		sum = sum&65535 + sum>>16
	}
	return ^uint16(sum)
}

func bridgeUDPReply(t *testing.T, packet []byte) []byte {
	t.Helper()
	m, ok := frame(packet)
	if !ok || !m.udp {
		t.Fatal("invalid kernel UDP packet")
	}
	p := append([]byte(nil), packet...)
	copy(p[12:16], m.dst.AsSlice())
	copy(p[16:20], m.src.AsSlice())
	binary.BigEndian.PutUint16(p[20:22], m.dport)
	binary.BigEndian.PutUint16(p[22:24], m.sport)
	p[10], p[11], p[26], p[27] = 0, 0, 0, 0
	binary.BigEndian.PutUint16(p[10:12], bridgeChecksum(p[:20]))
	return p
}

func nativeSocket(t *testing.T, ip netip.Addr) *net.UDPConn {
	t.Helper()
	s, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, 0)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return s
}

func socketRoundtrip(t *testing.T, ctx context.Context, socket *net.UDPConn, remote *hostTUN, dest netip.Addr, tag byte) {
	t.Helper()
	payload := []byte{19, 29, 39, tag}
	if _, err := socket.WriteToUDPAddrPort(payload, netip.AddrPortFrom(dest, 49299)); err != nil {
		t.Fatal(err)
	}
	select {
	case packet := <-remote.write:
		if !bytes.Equal(packet[28:], payload) {
			t.Fatal("unexpected outbound native packet")
		}
		remote.read <- bridgeUDPReply(t, packet)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	buffer := make([]byte, 128)
	n, from, err := socket.ReadFromUDPAddrPort(buffer)
	if err != nil || from.Addr() != dest || !bytes.Equal(buffer[:n], payload) {
		t.Fatalf("native UDP reply: %s %x %v", from, buffer[:n], err)
	}
}

// TestNativeWindowsDualRoleBridge proves Wintun demultiplexing, not Internet NAT or full Core admission.
func TestNativeWindowsDualRoleBridge(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_BRIDGE_TEST") != "1" {
		t.Skip("requires elevated isolated Windows VM and signed wintun.dll")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctrl, ownAddr := netip.MustParseAddr("10.253.246.1"), netip.MustParseAddr("10.253.247.1")
	remoteAddr := netip.MustParseAddr("10.253.247.2")
	owner, err := tunnel.NewWindows(tunnel.Config{Name: fmt.Sprintf("IRVIO-B%05d", os.Getpid()%100000), Addresses: []netip.Prefix{netip.PrefixFrom(ctrl, 24), netip.PrefixFrom(ownAddr, 24)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			row := windows.MibIfRow2{InterfaceLuid: owner.LUID()}
			if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
				return
			}
			if time.Now().After(deadline) {
				t.Error("native bridge adapter residue after bounded PnP completion")
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	borrowed, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	userSocket := nativeSocket(t, ownAddr)
	controlEndpoint := netip.AddrPortFrom(ctrl, 7890)
	controlRule, err := winfirewall.OpenOwnedControl(filepath.Join(t.TempDir(), "firewall"), owner.LUID(), controlEndpoint, netip.PrefixFrom(ctrl, 24).Masked())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := controlRule.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := controlRule.Install(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenTCP("tcp4", net.TCPAddrFromAddrPort(controlEndpoint))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	var forwarded atomic.Uint64
	var bridgePointer atomic.Pointer[Bridge]
	b, err := New(Config{TUN: borrowed, Control: controlEndpoint, Limit: 8, Forward: func(ctx context.Context, p *Peer, packet []byte) error {
		if m, ok := frame(packet); !ok || !m.udp {
			return ErrConfig
		}
		forwarded.Add(1)
		return bridgePointer.Load().Reply(ctx, p, bridgeUDPReply(t, packet))
	}})
	if err != nil {
		borrowed.Close()
		t.Fatal(err)
	}
	bridgePointer.Store(b)
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := b.Attach(ownAddr, true); err != nil {
		t.Fatal(err)
	}
	exit := newEncryptedNode(t, b)
	upHost := newHostTUN()
	up := newEncryptedNode(t, upHost)
	addEncryptedPeer(t, exit, up, netip.MustParsePrefix("0.0.0.0/0"))
	addEncryptedPeer(t, up, exit, netip.PrefixFrom(ownAddr, 32))
	handshake(t, ctx, up, exit)
	var downs []*encryptedNode
	var tokens []*Peer
	for i := 0; i < 5; i++ {
		addr := netip.AddrFrom4([4]byte{10, 253, 246, byte(i + 2)})
		p, err := b.Attach(addr, false)
		if err != nil {
			t.Fatal(err)
		}
		h := newHostTUN()
		d := newEncryptedNode(t, h)
		addEncryptedPeer(t, exit, d, netip.PrefixFrom(addr, 32))
		addEncryptedPeer(t, d, exit, netip.MustParsePrefix("0.0.0.0/0"))
		handshake(t, ctx, d, exit)
		packet := udp(addr, internet, 30000, 53, byte(i+10))
		binary.BigEndian.PutUint16(packet[10:12], bridgeChecksum(packet[:20]))
		h.read <- packet
		expectPacket(t, ctx, h.write, bridgeUDPReply(t, packet))
		socketRoundtrip(t, ctx, userSocket, upHost, remoteAddr, byte(i+20))
		nativeControlRoundtrip(t, ctx, listener, h, addr, controlEndpoint, uint16(31000+i))
		downs = append(downs, d)
		tokens = append(tokens, p)
	}
	if forwarded.Load() != 5 {
		t.Fatalf("forwarded count=%d", forwarded.Load())
	}
	for i, p := range tokens {
		if err := b.Retire(ctx, p); err != nil {
			t.Fatal(err)
		}
		if err := exit.transport.RemovePeer(downs[i].public); err != nil {
			t.Fatal(err)
		}
		if err := b.Forget(p); err != nil {
			t.Fatal(err)
		}
	}
	socketRoundtrip(t, ctx, userSocket, upHost, remoteAddr, 99)
	if len(upHost.write) != 0 || b.Err() != nil {
		t.Fatal("unexpected upstream packet or bridge failure", b.Err())
	}
	t.Log("native Wintun + five encrypted downstream peers + own upstream + exact TCP control + downstream teardown PASS")
}

func controlSegment(src, dst netip.Addr, sport, dport uint16, seq, ack uint32, flags byte, payload []byte) []byte {
	p := make([]byte, 40+len(payload))
	copy(p, tcp(src, dst, sport, dport, 0))
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	binary.BigEndian.PutUint32(p[24:28], seq)
	binary.BigEndian.PutUint32(p[28:32], ack)
	p[33] = flags
	binary.BigEndian.PutUint16(p[34:36], 65535)
	copy(p[40:], payload)
	binary.BigEndian.PutUint16(p[10:12], bridgeChecksum(p[:20]))
	pseudo := append([]byte(nil), p[12:20]...)
	pseudo = append(pseudo, 0, 6, byte((len(p)-20)>>8), byte(len(p)-20))
	pseudo = append(pseudo, p[20:]...)
	binary.BigEndian.PutUint16(p[36:38], bridgeChecksum(pseudo))
	return p
}

// nativeControlRoundtrip drives a bounded raw TCP client through AWG into the real Windows listener.
func nativeControlRoundtrip(t *testing.T, ctx context.Context, listener *net.TCPListener, remote *hostTUN, src netip.Addr, dst netip.AddrPort, port uint16) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	read := func() []byte {
		select {
		case p := <-remote.write:
			m, ok := frame(p)
			if !ok || !m.tcp || m.src != dst.Addr() || m.dst != src || m.sport != dst.Port() || m.dport != port {
				t.Fatalf("unexpected control frame: %x", p)
			}
			return p
		case <-ctx.Done():
			t.Fatal("native TCP control timeout", ctx.Err())
			return nil
		}
	}
	remote.read <- controlSegment(src, dst.Addr(), port, dst.Port(), 1000, 0, 2, nil)
	synack := read()
	if synack[33]&0x12 != 0x12 || binary.BigEndian.Uint32(synack[28:32]) != 1001 {
		t.Fatal("missing SYN-ACK")
	}
	ack := binary.BigEndian.Uint32(synack[24:28]) + 1
	remote.read <- controlSegment(src, dst.Addr(), port, dst.Port(), 1001, ack, 0x18, []byte{73})
	listener.SetDeadline(time.Now().Add(5 * time.Second))
	conn, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	payload := make([]byte, 1)
	if _, err := io.ReadFull(conn, payload); err != nil || payload[0] != 73 {
		t.Fatal("native TCP payload", payload, err)
	}
	if _, err := conn.Write([]byte{74}); err != nil {
		t.Fatal(err)
	}
	for {
		packet := read()
		start := 20 + int(packet[32]>>4)*4
		if start == len(packet) {
			continue
		}
		if !bytes.Equal(packet[start:], []byte{74}) || binary.BigEndian.Uint32(packet[24:28]) != ack {
			t.Fatal("native TCP reply mismatch")
		}
		remote.read <- controlSegment(src, dst.Addr(), port, dst.Port(), 1002, ack+1, 0x14, nil)
		return
	}
}
