// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/crypto/curve25519"
)

// This gate uses the real ExitServer/allocator/NAT, but supplies ICE endpoints directly, not via discovery.
func TestWindowsNativeExitServerComposition(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_EXIT_SERVER") != "1" || os.Getenv("IRVIO_DIVERT_TEST_SERVICE") != "running" {
		t.Skip("requires isolated SYSTEM VM and owned running driver")
	}
	oracle, err := netip.ParseAddr(os.Getenv("IRVIO_WINDOWS_NAT_DNS_ORACLE"))
	if err != nil || !oracle.Is4() || !oracle.IsGlobalUnicast() || oracle.IsPrivate() {
		t.Fatal("explicit public DNS oracle required")
	}
	path, err := winroute.ResolveUnderlay(oracle, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c, _ := generationConfig(t)
	public, err := curve25519.X25519(c.PrivateKey[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	lab := newWindowsRuntimeLab(t)
	node := lab.node(t, discovery.Config{Exit: true, Country: "DE", AWGPublicKey: public, AWGKeyEpoch: 1, DeferExitAdvertisement: true})
	c.Host, c.LocalPeer, c.IdentityPrivate = node.Host(), node.PeerID(), node.IdentityPrivateKey()
	c.Bindings = productionBindingResolver(node, c.Bindings.Registry)
	envelope := node.LocalSignedAnnouncement()
	binding, err := c.Bindings.ResolveSigned(envelope, node.PeerID())
	if err != nil {
		t.Fatal(err)
	}
	c.SignedDescriptor = node.LocalSignedAnnouncement
	c.TUNName = fmt.Sprintf("IRVIO-S%d", os.Getpid())
	c.WANName = "native-test-underlay"
	c.Platform.JournalDirectory = filepath.Join(t.TempDir(), "new-runtime-journal")
	gen := generationForSource(t, path.Binding.Source)
	c.Transports = &exitPlaneTransport{current: gen}
	production := ProductionConfig{Platform: c.Platform, TUNName: c.TUNName, ExitWANName: c.WANName,
		ExitTUNAddresses: c.TUNAddresses, ExitIPv4Pool: c.IPv4Pool, ExitIPv6Pool: c.IPv6Pool,
		LeaseTTL: c.LeaseTTL, LeaseSweep: c.LeaseSweep, Obfuscation: c.Obfuscation}
	s, err := newProductionExit(ctx, node, c.PrivateKey, c.Transports, c.Bindings, production, 10*time.Second, lab.logger,
		M12Config{Enabled: true, SessionControlPort: c.SessionControlPort}, nil)
	if s != nil {
		t.Cleanup(func() {
			if err := s.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	p := s.plane.(*windowsExitDataPlane)
	if s.m12 == nil || len(s.m12.controls) != 1 {
		t.Fatal("M12 control listener not composed")
	}
	var clients []generationClient
	for i := byte(2); i < 7; i++ {
		client := generationClientPeer(t, c, binding, gen, i, false)
		lease, err := s.lifecycle.AllocateProvisionalForSession(client.binding.Binding.PeerID, client.lease.Session, time.Now().Add(10*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		client.lease = lease
		if err := p.registerPending(&pendingExitSession{token: lease.Session, remote: lease.ClientPeer, key: client.binding.Key, keyEpoch: client.binding.Binding.KeyEpoch, endpoint: client.endpoint, ice: &iceconnect.ConnectedSession{}, createdAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		lease, err = s.lifecycle.ActivateSession(lease.ClientPeer, lease.ID, lease.Generation, lease.Session)
		if err != nil {
			t.Fatal(err)
		}
		client.lease = lease
		clients = append(clients, client)
	}
	exchange := func(client generationClient, id uint16) {
		question := []byte{0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'o', 'r', 'g', 0, 0, 1, 0, 1}
		binary.BigEndian.PutUint16(question[:2], id)
		reply := generationExchange(t, client.tun, generationPacket(client.lease.IPv4.Addr(), oracle, question))
		if len(reply) < 40 || netip.AddrFrom4([4]byte(reply[12:16])) != oracle || netip.AddrFrom4([4]byte(reply[16:20])) != client.lease.IPv4.Addr() || binary.BigEndian.Uint16(reply[28:30]) != id || reply[30]&0x80 == 0 || reply[31]&15 != 0 {
			t.Fatal("invalid public DNS reply")
		}
	}
	for i, client := range clients {
		exchange(client, uint16(i+10))
	}
	first := clients[0].lease
	if err := s.lifecycle.ReleaseSession(ctx, first.ClientPeer, first.ID, first.Generation, first.Session); err != nil {
		t.Fatal(err)
	}
	if len(p.leases) != 4 || len(s.lifecycle.Status()) != 4 {
		t.Fatal("RELEASE did not revoke exactly one lease")
	}
	exchange(clients[1], 20)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := net.InterfaceByName(c.TUNName); err == nil {
		t.Fatal("Exit adapter residue")
	}
	t.Log("ExitServer constructor + M12 listener + five allocator-owned leases + six public DNS replies + isolated release + cleanup PASS; ICE/discovery and own protected session are separate gates")
}
