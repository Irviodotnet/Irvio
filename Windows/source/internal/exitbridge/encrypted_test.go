// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitbridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"
	"golang.org/x/crypto/curve25519"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

// gatedReader pauses after the bridge returns plaintext but before AWG's AllowedIPs lookup.
type gatedReader struct {
	tun.Device
	entered, resume chan struct{}
	once            sync.Once
}

func (g *gatedReader) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	n, err := g.Device.Read(bufs, sizes, offset)
	if n == 1 && sizes[0] == 29 && bufs[0][offset+28] == 71 {
		g.once.Do(func() { close(g.entered); <-g.resume })
	}
	return n, err
}

type encryptedNode struct {
	transport *awg.Transport
	public    awg.Key
	id        peer.ID
	endpoint  awg.SelectedEndpoint
}

func newEncryptedNode(t *testing.T, device tun.Device) *encryptedNode {
	t.Helper()
	c := socketmux.DefaultConfig()
	c.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	c.IPv6Policy = socketmux.FamilyDisabled
	m, err := socketmux.New(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	var private, public awg.Key
	if _, err := rand.Read(private[:]); err != nil {
		t.Fatal(err)
	}
	p, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	copy(public[:], p)
	identity, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(identity)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := m.LocalAddr(socketmux.IPv4)
	if !ok {
		t.Fatal("missing bound endpoint")
	}
	transport, err := awg.NewTransport(awg.Config{Mux: m, NetworkGeneration: 1, TUN: device, PrivateKey: private, MaxPeers: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Error(err)
		}
	})
	return &encryptedNode{transport, public, id, awg.SelectedEndpoint{NetworkGeneration: 1, SessionGeneration: 1, RemotePeer: id, Address: a, Candidate: iceconnect.CandidateSnapshot{Type: ice.CandidateTypeHost, Network: icesession.NetworkUDP4, Address: a.Addr(), Port: a.Port(), Component: 1}}}
}

func addEncryptedPeer(t *testing.T, local, remote *encryptedNode, prefix netip.Prefix) {
	t.Helper()
	if err := local.transport.AddPeer(awg.PeerConfig{RemotePeer: remote.id, PublicKey: remote.public, Endpoint: remote.endpoint, AllowedIPs: []netip.Prefix{prefix}}); err != nil {
		t.Fatal(err)
	}
}

func handshake(t *testing.T, ctx context.Context, a, b *encryptedNode) {
	t.Helper()
	if err := a.transport.TriggerHandshake(b.public); err != nil {
		t.Fatal(err)
	}
	if _, err := a.transport.WaitHandshake(ctx, b.public); err != nil {
		t.Fatal(err)
	}
	if _, err := b.transport.WaitHandshake(ctx, a.public); err != nil {
		t.Fatal(err)
	}
}

func expectPacket(t *testing.T, ctx context.Context, c <-chan []byte, expected []byte) {
	t.Helper()
	select {
	case packet := <-c:
		if !bytes.Equal(packet, expected) {
			t.Fatalf("unexpected packet: got %x want %x", packet, expected)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// TestEncryptedSeparationAndReuse uses real loopback AWG with test-coordinated keys and no native adapter.
func TestEncryptedSeparationAndReuse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	forwarded := make(chan []byte, 16)
	b, host, old, _ := fixture(t, func(_ context.Context, p *Peer, packet []byte) error {
		if p.Address() != downstream {
			return ErrStale
		}
		forwarded <- append([]byte(nil), packet...)
		return nil
	})
	gate := &gatedReader{Device: b, entered: make(chan struct{}), resume: make(chan struct{})}
	exit := newEncryptedNode(t, gate)
	var resume sync.Once
	t.Cleanup(func() { resume.Do(func() { close(gate.resume) }) })
	downHost, upHost := newHostTUN(), newHostTUN()
	down, up := newEncryptedNode(t, downHost), newEncryptedNode(t, upHost)
	addEncryptedPeer(t, exit, down, netip.PrefixFrom(downstream, 32))
	addEncryptedPeer(t, down, exit, netip.MustParsePrefix("0.0.0.0/0"))
	addEncryptedPeer(t, exit, up, netip.MustParsePrefix("0.0.0.0/0"))
	addEncryptedPeer(t, up, exit, netip.PrefixFrom(own, 32))
	handshake(t, ctx, down, exit)
	handshake(t, ctx, up, exit)

	packet := udp(downstream, internet, 30000, 53, 1)
	downHost.read <- packet
	expectPacket(t, ctx, forwarded, packet)
	packet = tcp(downstream, control.Addr(), 30000, control.Port(), 2)
	downHost.read <- packet
	expectPacket(t, ctx, host.write, packet)
	packet = tcp(control.Addr(), downstream, control.Port(), 30000, 3)
	host.read <- packet
	expectPacket(t, ctx, downHost.write, packet)
	packet = udp(own, internet, 30001, 53, 4)
	host.read <- packet
	expectPacket(t, ctx, upHost.write, packet)
	packet = udp(internet, own, 53, 30001, 5)
	upHost.read <- packet
	expectPacket(t, ctx, host.write, packet)
	if len(upHost.write) != 0 || len(host.write) != 0 {
		t.Fatal("downstream packet escaped its lane")
	}

	packet = udp(internet, downstream, 53, 30000, 71)
	if err := b.Reply(ctx, old, packet); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	packet[28] = 72
	if err := b.Reply(ctx, old, packet); err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	err := b.Retire(short, old)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("ownership released before actual AWG lookup", err)
	}
	resume.Do(func() { close(gate.resume) })
	if err := b.Retire(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := exit.transport.RemovePeer(down.public); err != nil {
		t.Fatal(err)
	}
	if err := b.Forget(old); err != nil {
		t.Fatal(err)
	}
	freshHost := newHostTUN()
	fresh := newEncryptedNode(t, freshHost)
	freshPeer, err := b.Attach(downstream, false)
	if err != nil {
		t.Fatal(err)
	}
	addEncryptedPeer(t, exit, fresh, netip.PrefixFrom(downstream, 32))
	addEncryptedPeer(t, fresh, exit, netip.MustParsePrefix("0.0.0.0/0"))
	handshake(t, ctx, fresh, exit)
	if err := b.Reply(ctx, old, packet); !errors.Is(err, ErrStale) {
		t.Fatal("retired token accepted", err)
	}
	packet[28] = 73
	if err := b.Reply(ctx, freshPeer, packet); err != nil {
		t.Fatal(err)
	}
	expectPacket(t, ctx, freshHost.write, packet)
	packet = udp(own, internet, 30001, 53, 74)
	host.read <- packet
	expectPacket(t, ctx, upHost.write, packet)
	if len(freshHost.write) != 0 || len(host.write) != 0 || len(forwarded) != 0 {
		t.Fatal("stale reply or cross-lane packet survived replacement")
	}
}
