// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/windivert"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestNativeBackendFiveLeaseDNS(t *testing.T) {
	oracle := os.Getenv("IRVIO_WINDOWS_NAT_DNS_ORACLE")
	if oracle == "" {
		t.Skip("requires explicit public DNS oracle in isolated Windows lab")
	}
	path := nativePortPath(t)
	server, err := netip.ParseAddr(oracle)
	if err != nil || !server.Is4() || server.IsPrivate() || !server.IsGlobalUnicast() {
		t.Fatal("invalid public oracle")
	}
	m, err := windivert.Load()
	if err != nil {
		t.Fatal(err)
	}
	replies := make(chan []byte, 16)
	b, err := NewWindowsBackend(DefaultConfig(path, nil), m, func(ctx context.Context, _ Path, packet []byte) error {
		select {
		case replies <- packet:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if b != nil {
		t.Cleanup(func() {
			if err := b.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	if os.Getenv("IRVIO_DIVERT_TEST_SERVICE") == "absent" {
		if !errors.Is(err, windivert.ErrService) {
			t.Fatal("missing driver accepted", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	query := func(id uint16) []byte {
		p := []byte{0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'o', 'r', 'g', 0, 0, 1, 0, 1}
		binary.BigEndian.PutUint16(p, id)
		return p
	}
	var nonce [2]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	base := binary.BigEndian.Uint16(nonce[:])
	// Verify the oracle independently so underlay failure is not mislabeled a translator defect.
	binding, err := winroute.ForSource(path.Source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dialer := net.Dialer{Control: binding.Control, Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "udp4", net.JoinHostPort(oracle, "53"))
	if err != nil {
		t.Fatal("oracle underlay preflight", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(query(base)); err != nil {
		t.Fatal(err)
	}
	answer := make([]byte, 4096)
	n, err := conn.Read(answer)
	if err != nil || n < 12 || binary.BigEndian.Uint16(answer[:2]) != base || answer[2]&0x80 == 0 {
		t.Fatal("oracle underlay preflight", n, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	var leases []*Lease
	for i := byte(2); i < 7; i++ {
		lease, err := b.AddLease(tunnellease.Lease{ID: tunnellease.ID{i}, Session: tunnellease.SessionToken{i}, ClientPeer: peer.ID(fmt.Sprint("native-client-", i)), ExitPeer: peer.ID("native-exit"), Generation: 1, IPv4: netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 17, 0, i}), 32), ValidUntil: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
		request := wire(UDP, lease.value.IPv4.Addr(), server, 30000, 53, 0, query(base+uint16(i)))
		if err := b.Outbound(ctx, lease, request); err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[netip.Addr]bool)
	for len(seen) < 5 {
		select {
		case packet := <-replies:
			p, err := parse(packet)
			if err != nil || p.source != server || p.sourcePort != 53 || p.destinationPort != 30000 || len(packet) < 40 {
				t.Fatal("bad translated DNS reply", err)
			}
			id := p.destination.As4()[3]
			if id < 2 || id > 6 || binary.BigEndian.Uint16(packet[28:30]) != base+uint16(id) || packet[30]&0x80 == 0 || packet[31]&15 != 0 {
				t.Fatal("DNS answer crossed lease or failed")
			}
			seen[p.destination] = true
		case <-ctx.Done():
			t.Fatalf("native replies=%d/5 stats=%+v dropped=%d backend=%v: %v", len(seen), b.Stats(), b.DroppedReplies(), b.Err(), ctx.Err())
		}
	}
	if s := b.Stats(); s.Leases != 5 || s.Flows != 5 || s.Reserved != 5 {
		t.Fatal(s)
	}
	for _, l := range leases {
		if err := b.Revoke(l); err != nil {
			t.Fatal(err)
		}
	}
	if s := b.Stats(); s.Leases != 0 || s.Flows != 0 || s.Reserved != 5 {
		t.Fatal("revoke lost quarantine", s)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if b.Stats() != (Stats{}) {
		t.Fatal("cleanup residue", b.Stats())
	}
	t.Log("native_backend_dns_replies=5 leases=5 shared_inner_port=30000 cleanup=PASS")
}

func TestNativeEncryptedLeaseNAT(t *testing.T) {
	oracle := os.Getenv("IRVIO_WINDOWS_NAT_DNS_ORACLE")
	if oracle == "" || os.Getenv("IRVIO_DIVERT_TEST_SERVICE") != "running" {
		t.Skip("requires installer-owned running driver and public DNS oracle")
	}
	server, err := netip.ParseAddr(oracle)
	if err != nil || !server.Is4() || server.IsPrivate() || !server.IsGlobalUnicast() {
		t.Fatal("invalid public DNS oracle")
	}
	path := nativePortPath(t)
	module, err := windivert.Load()
	if err != nil {
		t.Fatal(err)
	}
	encryptedLeaseScenario(t, func(deliver Emit) (*Backend, error) {
		return NewWindowsBackend(DefaultConfig(path, nil), module, deliver)
	}, server)
}
