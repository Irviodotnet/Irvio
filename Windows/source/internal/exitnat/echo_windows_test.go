// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/windivert"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestNativeBackendFiveLeaseEcho(t *testing.T) {
	value := os.Getenv("IRVIO_WINDOWS_NAT_ECHO_ORACLE")
	if value == "" || os.Getenv("IRVIO_DIVERT_TEST_SERVICE") != "running" {
		t.Skip("requires an isolated echo oracle and installer-owned running driver")
	}
	oracle, err := netip.ParseAddr(value)
	if err != nil || !oracle.Is4() || oracle.IsPrivate() || !oracle.IsGlobalUnicast() {
		t.Fatal("invalid public echo oracle")
	}
	path := nativePortPath(t)
	module, err := windivert.Load()
	if err != nil {
		t.Fatal(err)
	}
	replies := make(chan []byte, 32)
	b, err := NewWindowsBackend(DefaultConfig(path, nil), module, func(ctx context.Context, _ Path, packet []byte) error {
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
	if err != nil {
		t.Fatal(err)
	}
	var leases []*Lease
	for i := byte(2); i < 7; i++ {
		l, err := b.AddLease(tunnellease.Lease{ID: tunnellease.ID{i}, Session: tunnellease.SessionToken{i}, ClientPeer: peer.ID(fmt.Sprint("echo-client-", i)), ExitPeer: peer.ID("echo-exit"), Generation: 1, IPv4: netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 17, 0, i}), 32), ValidUntil: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, l)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for round := 0; round < 10; round++ {
		want := make(map[netip.Addr][]byte)
		for i, l := range leases {
			data := bytes.Repeat([]byte{byte(i + round*7)}, []int{0, 1, 16, 32, 1200}[i])
			want[l.value.IPv4.Addr()] = data
			if err := b.Outbound(ctx, l, echoWire(l.value.IPv4.Addr(), oracle, 42, uint16(round), data)); err != nil {
				t.Fatal(err)
			}
		}
		for len(want) != 0 {
			select {
			case packet := <-replies:
				p, err := parse(packet)
				if err != nil || p.protocol != ICMP || packet[20] != 0 || p.source != oracle || p.sourcePort != 42 || p.destinationPort != uint16(round) {
					t.Fatal("invalid native echo reply", err)
				}
				data, ok := want[p.destination]
				if !ok || !bytes.Equal(packet[28:], data) {
					t.Fatal("reply crossed lease, duplicated or changed payload")
				}
				delete(want, p.destination)
			case <-ctx.Done():
				t.Fatalf("round=%d missing=%d stats=%+v dropped=%d backend=%v: %v", round, len(want), b.Stats(), b.DroppedReplies(), b.Err(), ctx.Err())
			}
		}
		if s := b.Stats(); s != (Stats{Leases: 5}) {
			t.Fatal("echo completion retained a capture", s)
		}
	}
	for _, l := range leases {
		if err := b.Revoke(l); err != nil {
			t.Fatal(err)
		}
		if err := b.Outbound(ctx, l, echoWire(l.value.IPv4.Addr(), oracle, 42, 0, nil)); !errors.Is(err, ErrStale) {
			t.Fatal("revoked lease sent echo", err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if b.Stats() != (Stats{}) || b.DroppedReplies() != 0 {
		t.Fatal("residue or unexpected drops", b.Stats(), b.DroppedReplies())
	}
	t.Log("native_backend_echo_replies=50 leases=5 shared_id=42 payload_restore=PASS revoke=PASS cleanup=PASS")
}

func TestNativeEncryptedLeaseEcho(t *testing.T) {
	value := os.Getenv("IRVIO_WINDOWS_NAT_ECHO_ORACLE")
	if value == "" || os.Getenv("IRVIO_DIVERT_TEST_SERVICE") != "running" {
		t.Skip("requires an isolated echo oracle and installer-owned running driver")
	}
	oracle, err := netip.ParseAddr(value)
	if err != nil || !oracle.Is4() || oracle.IsPrivate() || !oracle.IsGlobalUnicast() {
		t.Fatal("invalid public echo oracle")
	}
	path := nativePortPath(t)
	module, err := windivert.Load()
	if err != nil {
		t.Fatal(err)
	}
	encryptedLeaseProbeScenario(t, func(deliver Emit) (*Backend, error) {
		return NewWindowsBackend(DefaultConfig(path, nil), module, deliver)
	}, oracle, true)
}

func TestNativeEncryptedEchoResourceLifetime(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_NAT_ECHO_ORACLE") == "" || os.Getenv("IRVIO_DIVERT_TEST_SERVICE") != "running" {
		t.Skip("requires isolated native echo fixture")
	}
	var warmup resourceWarmup
	warmed := false
	for i := 1; i <= 100; i++ {
		if !t.Run(fmt.Sprintf("warm-%03d", i), TestNativeEncryptedLeaseEcho) {
			t.Fatal("echo warmup failed")
		}
		if warmup.observe(sampleLeaseResources(t, -i)) {
			warmed = true
			break
		}
	}
	if !warmed {
		t.Fatal("echo resources did not stabilize in 100 warmup cycles")
	}
	for i := 1; i <= 100; i++ {
		if !t.Run(fmt.Sprintf("cycle-%03d", i), TestNativeEncryptedLeaseEcho) {
			t.Fatal("echo lifecycle failed")
		}
		if i%10 == 0 {
			if err := warmup.high.growth(sampleLeaseResources(t, i)); err != nil {
				t.Error(err)
			}
		}
	}
	t.Log("NATIVE_ENCRYPTED_ECHO cycles=100 leases_per_cycle=5 resource_envelope=PASS")
}
