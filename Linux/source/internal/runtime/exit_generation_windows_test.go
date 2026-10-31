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
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"
	"golang.org/x/crypto/curve25519"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/exitbridge"
	"github.com/Irviodotnet/Irvio/internal/exitnat"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type generationChannelOwner struct {
	channel  *tuntest.ChannelTUN
	device   tun.Device
	borrowed int
}

func newGenerationChannelOwner() *generationChannelOwner {
	c := tuntest.NewChannelTUN()
	return &generationChannelOwner{channel: c, device: c.TUN()}
}
func (*generationChannelOwner) Name() string { return "test-exit" }
func (*generationChannelOwner) IfIndex() int { return 1 }
func (*generationChannelOwner) Addresses() []netip.Prefix {
	return []netip.Prefix{netip.MustParsePrefix("10.253.245.1/24")}
}
func (o *generationChannelOwner) Borrow() (tun.Device, error) { o.borrowed++; return o.device, nil }
func (o *generationChannelOwner) Close() error                { return o.device.Close() }

type generationWirePort struct {
	port    uint16
	packets chan []byte
}

func (p *generationWirePort) Port() uint16 { return p.port }
func (*generationWirePort) Close() error   { return nil }
func (p *generationWirePort) ReceiveIPv4(ctx context.Context) ([]byte, error) {
	select {
	case b := <-p.packets:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type generationWire struct {
	mu        sync.Mutex
	ports     map[uint16]*generationWirePort
	failClose atomic.Bool
}

func (w *generationWire) Close() error {
	if w.failClose.Load() {
		return errors.New("injected NAT close failure")
	}
	return nil
}
func (w *generationWire) SendIPv4(ctx context.Context, b []byte) error {
	w.mu.Lock()
	p := w.ports[binary.BigEndian.Uint16(b[20:22])]
	w.mu.Unlock()
	if p == nil {
		return errors.New("unreserved source port")
	}
	select {
	case p.packets <- nativeDPReply(b):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (w *generationWire) backend(deliver exitnat.Emit) (*exitnat.Backend, error) {
	w.ports = make(map[uint16]*generationWirePort)
	c := exitnat.DefaultConfig(exitnat.Path{Generation: 1, Interface: 1, Source: netip.MustParseAddr("198.51.100.23")}, nil)
	return exitnat.NewBackend(c, w, func(context.Context, exitnat.Path, uint8) (exitnat.PacketPort, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		p := &generationWirePort{port: uint16(32000 + len(w.ports)), packets: make(chan []byte, 8)}
		w.ports[p.port] = p
		return p, nil
	}, deliver)
}

func generationIdentity(t *testing.T, r BindingResolver) (awg.Key, ResolvedBinding) {
	t.Helper()
	identity, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(identity)
	if err != nil {
		t.Fatal(err)
	}
	var private awg.Key
	if _, err := rand.Read(private[:]); err != nil {
		t.Fatal(err)
	}
	public, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	a := &irviov1.NodeAnnouncement{ProtocolVersion: protocol.ProtocolVersion, NodeId: []byte(id), SessionEpoch: make([]byte, 16), Incarnation: 1, Sequence: 1, Capabilities: protocol.CapExit, Country: "DE", Libp2PAddrs: []string{"/ip4/198.51.100.10/tcp/4001"}, AwgPublicKey: public, AwgKeyEpoch: 1, CreatedAtUnix: now.Add(-time.Second).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix()}
	a.SessionEpoch[0] = 1
	envelope, err := protocol.SealAnnouncement(identity, a, now)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := r.ResolveSigned(envelope, id)
	if err != nil {
		t.Fatal(err)
	}
	return private, resolved
}

func generationEndpoint(t *testing.T, id peer.ID, gen *transportgen.Generation) awg.SelectedEndpoint {
	t.Helper()
	address, ok := gen.Mux().LocalAddr(socketmux.IPv4)
	if !ok {
		t.Fatal("missing generation IPv4 endpoint")
	}
	return awg.SelectedEndpoint{NetworkGeneration: gen.ID(), SessionGeneration: 1, RemotePeer: id, Address: address, Candidate: iceconnect.CandidateSnapshot{Type: ice.CandidateTypeHost, Network: icesession.NetworkUDP4, Address: address.Addr(), Port: address.Port(), Component: 1}}
}

func generationConfig(t *testing.T) (ExitServerConfig, ResolvedBinding) {
	t.Helper()
	r := BindingResolver{Registry: awgbinding.NewRegistry()}
	private, binding := generationIdentity(t, r)
	return ExitServerConfig{LocalPeer: binding.Binding.PeerID, PrivateKey: private, Bindings: r, MaxPending: 16, M12Enabled: true, SessionControlPort: 7890, TUNName: "test-exit", TUNAddresses: []netip.Prefix{netip.MustParsePrefix("10.253.245.1/24")}, IPv4Pool: netip.MustParsePrefix("10.253.245.0/24")}, binding
}

func generationPacket(source, destination netip.Addr, payload []byte) []byte {
	b := make([]byte, 28+len(payload))
	b[0], b[8], b[9] = 0x45, 64, 17
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	s, d := source.As4(), destination.As4()
	copy(b[12:16], s[:])
	copy(b[16:20], d[:])
	binary.BigEndian.PutUint16(b[10:12], nativeDPChecksum(b[:20]))
	binary.BigEndian.PutUint16(b[20:22], 30000)
	binary.BigEndian.PutUint16(b[22:24], 53)
	binary.BigEndian.PutUint16(b[24:26], uint16(len(b)-20))
	copy(b[28:], payload)
	return b
}

func generationExchange(t *testing.T, c *tuntest.ChannelTUN, packet []byte) []byte {
	t.Helper()
	select {
	case c.Outbound <- packet:
	case <-time.After(3 * time.Second):
		t.Fatal("AWG input blocked")
	}
	select {
	case reply := <-c.Inbound:
		return reply
	case <-time.After(5 * time.Second):
		t.Fatal("encrypted packet reply missing")
	}
	return nil
}

type generationClient struct {
	binding   ResolvedBinding
	endpoint  awg.SelectedEndpoint
	lease     tunnellease.Lease
	tun       *tuntest.ChannelTUN
	transport *awg.Transport
}

func generationClientPeer(t *testing.T, c ExitServerConfig, exitBinding ResolvedBinding, generation *transportgen.Generation, index byte, upstream bool) generationClient {
	t.Helper()
	private, binding := generationIdentity(t, c.Bindings)
	remote := generationForSource(t, generation.BindPlan().IPv4)
	channel := tuntest.NewChannelTUN()
	lease := tunnellease.Lease{ID: tunnellease.ID{index}, Session: tunnellease.SessionToken{index}, ExitPeer: c.LocalPeer, ClientPeer: binding.Binding.PeerID, Generation: 1, IPv4: netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 253, 245, index}), 32), ValidUntil: time.Now().Add(time.Minute)}
	allowed := netip.MustParsePrefix("0.0.0.0/0")
	if upstream {
		lease.ClientPeer, lease.ExitPeer = c.LocalPeer, binding.Binding.PeerID
		lease.IPv4 = netip.MustParsePrefix("10.253.244.3/32")
		allowed = lease.IPv4
	}
	transport, err := awg.NewTransport(awg.Config{Mux: remote.Mux(), NetworkGeneration: remote.ID(), TUN: channel.TUN(), PrivateKey: private, Peers: []awg.PeerConfig{{RemotePeer: c.LocalPeer, PublicKey: exitBinding.Key, Endpoint: generationEndpoint(t, c.LocalPeer, generation), AllowedIPs: []netip.Prefix{allowed}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Error(err)
		}
	})
	return generationClient{binding: binding, endpoint: generationEndpoint(t, binding.Binding.PeerID, remote), lease: lease, tun: channel, transport: transport}
}

func generationForSource(t *testing.T, source netip.Addr) *transportgen.Generation {
	t.Helper()
	c := transportgen.DefaultConfig(transportgen.BindPlannerFunc(func(networkstate.Snapshot) (transportgen.BindPlan, error) {
		return transportgen.BindPlan{IPv4: source}, nil
	}))
	c.Validation.AllowLoopback = source.IsLoopback()
	c.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	b, err := transportgen.NewBuilder(c)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := winroute.ForSource(source)
	if err != nil {
		t.Fatal(err)
	}
	state := networkstate.State{Generation: 1, Snapshot: networkstate.Snapshot{Links: []networkstate.Link{{Index: int(binding.Index), NativeID: binding.LUID, Up: true, Addresses: []netip.Prefix{netip.PrefixFrom(source, source.BitLen())}}}}}
	g, err := b.Build(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Error(err)
		}
	})
	return g
}

func TestWindowsExitGenerationEncryptedDualRole(t *testing.T) {
	c, binding := generationConfig(t)
	gen := newRuntimeGeneration(t)
	owner, wire := newGenerationChannelOwner(), &generationWire{}
	var traffic exitbridge.Traffic
	g, err := openWindowsExitGeneration(context.Background(), c, owner, gen, wire.backend, &traffic)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		wire.failClose.Store(false)
		if err := g.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	var clients []generationClient
	for i := byte(2); i < 7; i++ {
		p := generationClientPeer(t, c, binding, gen, i, false)
		if err := g.AddDownstream(context.Background(), p.lease, p.binding, p.endpoint); err != nil {
			t.Fatal(err)
		}
		clients = append(clients, p)
	}
	upstream := generationClientPeer(t, c, binding, gen, 10, true)
	if err := g.AddUpstream(context.Background(), upstream.lease, upstream.binding, upstream.endpoint); err != nil {
		t.Fatal(err)
	}
	for _, p := range clients {
		packet := generationPacket(p.lease.IPv4.Addr(), netip.MustParseAddr("8.8.8.8"), []byte{p.lease.ID[0], 1, 2, 3})
		reply := generationExchange(t, p.tun, packet)
		if len(reply) != len(packet) || reply[28] != p.lease.ID[0] || netip.AddrFrom4([4]byte(reply[16:20])) != p.lease.IPv4.Addr() {
			t.Fatal("wrong downstream reply")
		}
	}
	select {
	case <-owner.channel.Inbound:
		t.Fatal("downstream entered host")
	default:
	}
	select {
	case <-upstream.tun.Inbound:
		t.Fatal("downstream entered upstream")
	default:
	}
	packet := generationPacket(upstream.lease.IPv4.Addr(), netip.MustParseAddr("9.9.9.9"), []byte("own traffic"))
	select {
	case owner.channel.Outbound <- packet:
	case <-time.After(time.Second):
		t.Fatal("host TUN blocked")
	}
	select {
	case received := <-upstream.tun.Inbound:
		select {
		case upstream.tun.Outbound <- nativeDPReply(received):
		case <-time.After(time.Second):
			t.Fatal("upstream reply blocked")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("own traffic did not reach upstream")
	}
	select {
	case reply := <-owner.channel.Inbound:
		if string(reply[28:]) != "own traffic" {
			t.Fatal("wrong own reply")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("own reply did not reach host")
	}
	p := clients[0]
	duplicate := p.lease
	duplicate.ID[0] = 30
	duplicate.IPv4 = netip.MustParsePrefix("10.253.245.30/32")
	if err := g.AddDownstream(context.Background(), duplicate, p.binding, p.endpoint); err == nil {
		t.Fatal("duplicate AWG peer accepted")
	}
	if _, err := g.transport.PeerStats(p.binding.Key); err != nil {
		t.Fatal("failed duplicate removed live peer", err)
	}
	renewed := p.lease
	renewed.Generation++
	renewed.ValidUntil = renewed.ValidUntil.Add(time.Minute)
	if err := g.RenewLease(context.Background(), p.lease, renewed); err != nil {
		t.Fatal(err)
	}
	if err := g.RevokeLease(context.Background(), p.lease, tunnellease.RevokeExplicitRelease); err == nil {
		t.Fatal("stale revision revoked renewed lease")
	}
	if err := g.RevokeLease(context.Background(), renewed, tunnellease.RevokeExplicitRelease); err != nil {
		t.Fatal(err)
	}
	if _, err := g.transport.PeerStats(upstream.binding.Key); err != nil {
		t.Fatal("downstream revoke damaged own upstream", err)
	}
	if err := g.RemoveUpstream(context.Background(), upstream.lease.ID, tunnellease.SessionToken{99}); err == nil {
		t.Fatal("stale upstream close accepted")
	}
	if err := g.RemoveUpstream(context.Background(), upstream.lease.ID, upstream.lease.Session); err != nil {
		t.Fatal(err)
	}
	p = clients[1]
	generationExchange(t, p.tun, generationPacket(p.lease.IPv4.Addr(), netip.MustParseAddr("8.8.8.8"), []byte("after own disconnect")))
	wire.failClose.Store(true)
	if err := g.Close(context.Background()); err == nil || g.transport == nil {
		t.Fatal("failed NAT cleanup discarded AWG reader", err)
	}
	wire.failClose.Store(false)
	if err := g.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gen.Lifecycle() != transportgen.LifecycleReady {
		t.Fatal("generation owner closed borrowed mux")
	}
	want := exitbridge.TrafficSnapshot{MyUpload: uint64(len(packet)), MyDownload: uint64(len(packet)), SharedUpload: uint64(5*32 + 28 + len("after own disconnect"))}
	if got := traffic.Snapshot(); got != want {
		t.Fatalf("encrypted generation totals=%+v want=%+v", got, want)
	}
}

var _ tunnel.DeviceOwner = (*generationChannelOwner)(nil)

func TestWindowsExitGenerationRejectsLateConstruction(t *testing.T) {
	c, _ := generationConfig(t)
	gen := newRuntimeGeneration(t)
	owner, wire := newGenerationChannelOwner(), &generationWire{}
	g, err := openWindowsExitGeneration(context.Background(), c, owner, gen, func(deliver exitnat.Emit) (*exitnat.Backend, error) { gen.MarkStale(); return wire.backend(deliver) }, nil)
	if g != nil || !errors.Is(err, ErrGenerationStale) {
		t.Fatal("late construction published", err)
	}
}

func TestWindowsExitGenerationRollbackAndFencing(t *testing.T) {
	c, binding := generationConfig(t)
	gen := newRuntimeGeneration(t)
	owner := newGenerationChannelOwner()
	boom := errors.New("injected backend constructor failure")
	if g, err := openWindowsExitGeneration(context.Background(), c, owner, gen, func(exitnat.Emit) (*exitnat.Backend, error) { return nil, boom }, nil); g != nil || !errors.Is(err, boom) {
		t.Fatal("backend constructor rollback failed", err)
	}
	if owner.borrowed != 1 {
		t.Fatal("incorrect borrow count")
	}
	owner = newGenerationChannelOwner()
	wire := &generationWire{}
	g, err := openWindowsExitGeneration(context.Background(), c, owner, gen, wire.backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	p := generationClientPeer(t, c, binding, gen, 2, false)
	for _, tc := range []struct {
		name   string
		mutate func(*generationClient)
	}{
		{"wrong network generation", func(p *generationClient) { p.endpoint.NetworkGeneration++ }},
		{"wrong peer", func(p *generationClient) { p.endpoint.RemotePeer = c.LocalPeer }},
		{"unsigned key", func(p *generationClient) { p.binding.Key[0] ^= 1 }},
		{"unknown key epoch", func(p *generationClient) { p.binding.Binding.KeyEpoch++ }},
		{"foreign Exit", func(p *generationClient) { p.lease.ExitPeer = p.lease.ClientPeer }},
		{"foreign pool", func(p *generationClient) { p.lease.IPv4 = netip.MustParsePrefix("10.253.244.4/32") }},
		{"no session", func(p *generationClient) { p.lease.Session = tunnellease.SessionToken{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := p
			tc.mutate(&bad)
			if err := g.AddDownstream(context.Background(), bad.lease, bad.binding, bad.endpoint); err == nil {
				t.Fatal("invalid promotion accepted")
			}
			if len(g.peers) != 0 {
				t.Fatal("failed authorization changed lease ownership")
			}
		})
	}
	if err := g.AddDownstream(context.Background(), p.lease, p.binding, p.endpoint); err != nil {
		t.Fatal(err)
	}
	gen.MarkStale()
	renewed := p.lease
	renewed.Generation++
	if err := g.RenewLease(context.Background(), p.lease, renewed); err == nil {
		t.Fatal("stale generation renewed")
	}
	if err := g.AddDownstream(context.Background(), p.lease, p.binding, p.endpoint); err == nil {
		t.Fatal("stale generation promoted")
	}
	if err := g.RevokeLease(context.Background(), p.lease, tunnellease.RevokeShutdown); err != nil {
		t.Fatal("stale generation cannot drain", err)
	}
}
