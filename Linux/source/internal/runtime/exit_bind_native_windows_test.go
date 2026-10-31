// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/Irviodotnet/Irvio/test/support/clientdplab"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestWindowsNativeExitOwnNetworkBind(t *testing.T) {
	if os.Getenv("IRVIO_EXIT_DP_NETWORK_BIND") != "1" || os.Getenv("IRVIO_DIVERT_TEST_SERVICE") != "running" {
		t.Skip("requires disposable SYSTEM VM, owned driver and Linux session-control fixture")
	}
	testWindowsNativeNetworkBind(t, true)
}

type nativeExitBindPlane struct {
	server   *ExitServer
	clients  []generationClient
	sequence uint16
}

func (p *nativeExitBindPlane) Install(ctx context.Context, r InstallRequest) (io.Closer, error) {
	return p.server.ClientInstaller().Install(ctx, r)
}
func (p *nativeExitBindPlane) Close() error { return p.server.Close(context.Background()) }

func newNativeExitBindPlane(t *testing.T, ctx context.Context, identity crypto.PrivKey, private, public awg.Key, transports *TransportOwner, updater *killswitch.WindowsNetworkUpdater, ready clientdplab.Ready) (*nativeExitBindPlane, error) {
	t.Helper()
	h, err := libp2p.New(libp2p.Identity(identity), libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC()
	a := &irviov1.NodeAnnouncement{ProtocolVersion: protocol.ProtocolVersion, NodeId: []byte(h.ID()), SessionEpoch: make([]byte, 16), Incarnation: 1, Sequence: 1, Capabilities: protocol.CapExit, Country: "DE", Libp2PAddrs: []string{"/ip4/198.51.100.10/tcp/4001"}, AwgPublicKey: public[:], AwgKeyEpoch: 1, CreatedAtUnix: now.Add(-time.Second).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix()}
	a.SessionEpoch[0] = 1
	envelope, err := protocol.SealAnnouncement(identity, a, now)
	if err != nil {
		return nil, err
	}
	bindings := BindingResolver{Registry: awgbinding.NewRegistry(), Directory: &bindingDirectory{envelope: envelope}}
	self, err := bindings.ResolveSigned(envelope, h.ID())
	if err != nil {
		return nil, err
	}
	remote, err := peer.Decode(ready.Peer)
	if err != nil {
		return nil, err
	}
	if _, err := bindings.ResolveSigned(ready.SignedDescriptor, remote); err != nil {
		return nil, err
	}
	c := ExitServerConfig{Host: h, LocalPeer: h.ID(), PrivateKey: private, IdentityPrivate: identity, Transports: transports, Bindings: bindings,
		Authorizer: icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }), SignalConfig: icesignal.DefaultConfig(), SignedDescriptor: func() []byte { return envelope },
		TUNName: "IRVIO-DP", TUNAddresses: []netip.Prefix{netip.MustParsePrefix("10.253.245.1/24")}, IPv4Pool: netip.MustParsePrefix("10.253.245.0/24"), WANName: "native-underlay", MaxPending: 16, M12Enabled: true, SessionControlPort: 7890,
		Platform: ClientDataPlanePlatform{Protection: updater, JournalDirectory: filepath.Join(t.TempDir(), "private"), DNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")}},
	}
	s, err := NewExitServer(ctx, c)
	if s == nil {
		return nil, err
	}
	p := &nativeExitBindPlane{server: s}
	if err != nil {
		return p, err
	}
	for i := byte(2); i < 7; i++ {
		client := generationClientPeer(t, c, self, transports.Current(), i, false)
		lease, err := s.lifecycle.AllocateProvisionalForSession(client.binding.Binding.PeerID, client.lease.Session, time.Now().Add(15*time.Second))
		if err != nil {
			return p, err
		}
		client.lease = lease
		if err := s.plane.registerPending(&pendingExitSession{token: lease.Session, remote: lease.ClientPeer, key: client.binding.Key, keyEpoch: client.binding.Binding.KeyEpoch, endpoint: client.endpoint, ice: &iceconnect.ConnectedSession{}, createdAt: time.Now()}); err != nil {
			return p, err
		}
		lease, err = s.lifecycle.ActivateSession(lease.ClientPeer, lease.ID, lease.Generation, lease.Session)
		if err != nil {
			return p, err
		}
		client.lease = lease
		p.clients = append(p.clients, client)
	}
	return p, nil
}

func (p *nativeExitBindPlane) exchange(t *testing.T, stage string) {
	t.Helper()
	defer func() {
		if !t.Failed() {
			return
		}
		plane := p.server.plane.(*windowsExitDataPlane)
		g := plane.active
		if g == nil {
			t.Log("missing active Exit generation")
			return
		}
		t.Logf("stage=%s NAT=%+v error=%v bridge_drops=%d bind=%+v mux=%+v own=%v", stage, g.resources.Stats(), g.resources.Err(), g.resources.Bridge().Dropped(), g.transport.Bind().Stats(), g.generation.Mux().Stats(), plane.upstream != nil)
		for i, c := range p.clients {
			stats, err := g.transport.PeerStats(c.binding.Key)
			t.Logf("downstream=%d stats=%+v error=%v", i, stats, err)
		}
	}()
	for _, client := range p.clients {
		p.sequence++
		q := []byte{0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'o', 'r', 'g', 0, 0, 1, 0, 1}
		binary.BigEndian.PutUint16(q[:2], p.sequence)
		oracle := netip.MustParseAddr("1.1.1.1")
		t.Logf("DNS_SEND stage=%s query=%d source=%s", stage, p.sequence, client.lease.IPv4.Addr())
		reply := generationExchange(t, client.tun, generationPacket(client.lease.IPv4.Addr(), oracle, q))
		if len(reply) < 40 || netip.AddrFrom4([4]byte(reply[12:16])) != oracle || netip.AddrFrom4([4]byte(reply[16:20])) != client.lease.IPv4.Addr() || binary.BigEndian.Uint16(reply[28:30]) != p.sequence || reply[30]&0x80 == 0 || reply[31]&15 != 0 {
			t.Fatal("invalid downstream DNS reply", stage)
		}
		t.Logf("DNS_REPLY stage=%s query=%d", stage, p.sequence)
	}
	t.Log("five protected downstream DNS replies", stage)
}

func (p *nativeExitBindPlane) checkAutomaticSource(t *testing.T) {
	t.Helper()
	c, err := net.DialTimeout("udp4", "10.253.239.1:49497", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.LocalAddr().(*net.UDPAddr).IP.String() != "10.253.239.2" {
		t.Fatal("Exit address selected for own user traffic", c.LocalAddr())
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	message := []byte("automatic-own-source")
	if _, err := c.Write(message); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 64)
	n, err := c.Read(b)
	if err != nil || string(b[:n]) != string(message) {
		t.Fatal("automatic own source roundtrip", err)
	}
}

func nativeExitPhysicalDNSProbe(t *testing.T, binding winroute.Binding, want bool) {
	t.Helper()
	d := net.Dialer{Control: binding.Control, Timeout: time.Second}
	c, err := d.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		if want {
			t.Fatal("physical WAN preflight", err)
		}
		return
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(1200 * time.Millisecond))
	q := []byte{0, 91, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'o', 'r', 'g', 0, 0, 1, 0, 1}
	_, writeErr := c.Write(q)
	b := make([]byte, 2048)
	n, readErr := c.Read(b)
	if want && (writeErr != nil || readErr != nil || n < 12 || b[0] != 0 || b[1] != 91 || b[2]&0x80 == 0) {
		t.Fatal("physical WAN probe", writeErr, readErr)
	}
	if !want && readErr == nil {
		t.Fatal("ordinary physical WAN traffic passed protection")
	}
}
