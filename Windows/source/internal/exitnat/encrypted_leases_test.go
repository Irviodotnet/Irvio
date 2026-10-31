// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"
	"golang.org/x/crypto/curve25519"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

type encryptedLeaseClient struct {
	id        peer.ID
	public    awg.Key
	endpoint  awg.SelectedEndpoint
	tun       *tuntest.ChannelTUN
	transport *awg.Transport
	lease     tunnellease.Lease
}

func encryptedKeys(t *testing.T) (awg.Key, awg.Key, peer.ID) {
	t.Helper()
	var private, public awg.Key
	if _, err := rand.Read(private[:]); err != nil {
		t.Fatal(err)
	}
	value, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	copy(public[:], value)
	identity, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(identity)
	if err != nil {
		t.Fatal(err)
	}
	return private, public, id
}

func encryptedMux(t *testing.T) *socketmux.Mux {
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
	return m
}

func encryptedEndpoint(t *testing.T, id peer.ID, mux *socketmux.Mux) awg.SelectedEndpoint {
	t.Helper()
	a, ok := mux.LocalAddr(socketmux.IPv4)
	if !ok {
		t.Fatal("missing IPv4 mux address")
	}
	return awg.SelectedEndpoint{NetworkGeneration: 1, SessionGeneration: 1, RemotePeer: id, Address: a, Candidate: iceconnect.CandidateSnapshot{Type: ice.CandidateTypeHost, Network: icesession.NetworkUDP4, Address: a.Addr(), Port: a.Port(), Component: 1}}
}

func dnsQuestion(id uint16) []byte {
	p := []byte{0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'o', 'r', 'g', 0, 0, 1, 0, 1}
	binary.BigEndian.PutUint16(p, id)
	return p
}

// encryptedLeaseScenario uses real AWG and Core lifecycle but test-coordinated keys/endpoints and channel TUNs.
func encryptedLeaseScenario(t *testing.T, factory func(Emit) (*Backend, error), oracle netip.Addr) {
	t.Helper()
	encryptedLeaseProbeScenario(t, factory, oracle, false)
}

func encryptedLeaseProbeScenario(t *testing.T, factory func(Emit) (*Backend, error), oracle netip.Addr, echo bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	exitMux := encryptedMux(t)
	exitPrivate, exitPublic, exitID := encryptedKeys(t)
	exitTUN := tuntest.NewChannelTUN()
	obfuscation := awg.ObfuscationConfig{JunkPacketCount: 1, JunkMin: 24, JunkMax: 24,
		InitPadding: 16, ResponsePadding: 16, CookiePadding: 8,
		InitHeader: 101, ResponseHeader: 202, CookieHeader: 303, TransportHeader: 404,
		SignaturePackets: [5]string{"<b 0x4859445241>"}}
	var exit *awg.Transport
	clients := make([]*encryptedLeaseClient, 0, 5)
	byPeer := make(map[peer.ID]*encryptedLeaseClient)
	resources, err := NewBridgedResources(BridgedConfig{
		TUN: exitTUN.TUN(), Control: netip.MustParseAddrPort("10.17.0.1:7890"), Limit: 8, Backend: factory,
		Install: func(_ context.Context, v tunnellease.Lease) error {
			c := byPeer[v.ClientPeer]
			if c == nil {
				return ErrDenied
			}
			return exit.AddPeer(awg.PeerConfig{RemotePeer: c.id, PublicKey: c.public, Endpoint: c.endpoint, AllowedIPs: []netip.Prefix{v.IPv4}})
		},
		Remove: func(_ context.Context, v tunnellease.Lease) error {
			c := byPeer[v.ClientPeer]
			if c == nil {
				return ErrDenied
			}
			err := exit.RemovePeer(c.public)
			if errors.Is(err, awg.ErrPeerNotFound) {
				return nil
			}
			return err
		},
	})
	if resources != nil {
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			if err := resources.Close(cleanupCtx); err != nil {
				t.Error(err)
			}
			if exit != nil {
				if err := exit.Close(); err != nil {
					t.Error(err)
				}
			}
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	b := resources.backend
	exit, err = awg.NewTransport(awg.Config{Mux: exitMux, NetworkGeneration: 1, TUN: resources.Bridge(), PrivateKey: exitPrivate, MaxPeers: 8, Obfuscation: obfuscation})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		mux := encryptedMux(t)
		private, public, id := encryptedKeys(t)
		client := &encryptedLeaseClient{id: id, public: public, endpoint: encryptedEndpoint(t, id, mux), tun: tuntest.NewChannelTUN()}
		client.transport, err = awg.NewTransport(awg.Config{Mux: mux, NetworkGeneration: 1, TUN: client.tun.TUN(), PrivateKey: private, Obfuscation: obfuscation, Peers: []awg.PeerConfig{{RemotePeer: exitID, PublicKey: exitPublic, Endpoint: encryptedEndpoint(t, exitID, exitMux), AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := client.transport.Close(); err != nil {
				t.Error(err)
			}
		})
		clients = append(clients, client)
		byPeer[id] = client
	}
	allocatorConfig := tunnellease.DefaultConfig(exitID)
	allocatorConfig.IPv4Pool = netip.MustParsePrefix("10.17.0.0/24")
	allocatorConfig.Reserved = []netip.Addr{netip.MustParseAddr("10.17.0.1")}
	allocator, err := tunnellease.New(allocatorConfig)
	if err != nil {
		t.Fatal(err)
	}
	lifecycleConfig := tunnellease.DefaultLifecycleConfig(allocator, resources)
	lifecycleConfig.Activator, lifecycleConfig.Renewer, lifecycleConfig.RequireSessionToken = resources, resources, true
	lifecycle, err := tunnellease.NewLifecycle(lifecycleConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lifecycle.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for i, c := range clients {
		v, err := lifecycle.AllocateProvisionalForSession(c.id, tunnellease.SessionToken{byte(i + 1)}, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		c.lease, err = lifecycle.ActivateSession(v.ClientPeer, v.ID, v.Generation, v.Session)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.transport.TriggerHandshake(exitPublic); err != nil {
			t.Fatal(err)
		}
		if _, err := c.transport.WaitHandshake(ctx, exitPublic); err != nil {
			t.Fatal(err)
		}
		if _, err := exit.WaitHandshake(ctx, c.public); err != nil {
			t.Fatal(err)
		}
	}
	var nonce [2]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	base := binary.BigEndian.Uint16(nonce[:])
	send := func(c *encryptedLeaseClient, source netip.Addr, port, tag uint16) {
		if err := resources.Err(); err != nil {
			t.Fatal(err)
		}
		packet := wire(UDP, source, oracle, port, 53, 0, dnsQuestion(tag))
		if echo {
			packet = echoWire(source, oracle, port, tag, []byte("irvio-encrypted-echo"))
		}
		select {
		case c.tun.Outbound <- packet:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	completedDNS := make(map[*encryptedLeaseClient]map[uint16]bool)
	var totalRetries int
	round := func(list []*encryptedLeaseClient, offset uint16) {
		for i, c := range list {
			send(c, c.lease.IPv4.Addr(), 30000, base+offset+uint16(i))
		}
		for i, c := range list {
			tag := base + offset + uint16(i)
			if completedDNS[c] == nil {
				completedDNS[c] = make(map[uint16]bool)
			}
			retries, err := awaitEncryptedDNS(ctx, c.tun.Inbound, nil, 500*time.Millisecond, func() error {
				if err := resources.Err(); err != nil {
					return err
				}
				send(c, c.lease.IPv4.Addr(), 30000, tag)
				return ctx.Err()
			}, func(packet []byte) (bool, error) {
				p, err := parse(packet)
				if echo {
					if err != nil || p.source != oracle || p.destination != c.lease.IPv4.Addr() || p.protocol != ICMP || packet[20] != 0 || p.sourcePort != 30000 || !bytes.Equal(packet[28:], []byte("irvio-encrypted-echo")) || p.destinationPort != tag && !completedDNS[c][p.destinationPort] {
						return false, fmt.Errorf("encrypted echo crossed lease or changed payload: %v", err)
					}
					return p.destinationPort == tag, nil
				}
				if err != nil || p.source != oracle || p.destination != c.lease.IPv4.Addr() || p.sourcePort != 53 || p.destinationPort != 30000 || len(packet) < 40 {
					return false, fmt.Errorf("encrypted reply tuple mismatch: %v", err)
				}
				id := binary.BigEndian.Uint16(packet[28:30])
				if packet[30]&0x80 == 0 || packet[31]&15 != 0 || id != tag && !completedDNS[c][id] {
					return false, fmt.Errorf("encrypted reply crossed lease or DNS failed")
				}
				return id == tag, nil
			})
			totalRetries += retries
			if err != nil {
				clientStats, _ := c.transport.PeerStats(exitPublic)
				exitStats, _ := exit.PeerStats(c.public)
				t.Fatalf("encrypted reply failed: round=%d index=%d retries=%d error=%v backend=%v stats=%+v dropped=%d bridgeDrops=%d client=%+v exit=%+v clientBind=%+v exitBind=%+v exitMux=%+v", offset, i, retries, err, b.Err(), b.Stats(), b.DroppedReplies(), resources.Bridge().Dropped(), clientStats, exitStats, c.transport.Bind().Stats(), exit.Bind().Stats(), exitMux.Stats())
			}
			completedDNS[c][tag] = true
		}
	}
	send(clients[0], clients[1].lease.IPv4.Addr(), 30001, base-1)
	round(clients, 0)
	wantFlows := 5
	if echo {
		wantFlows = 0
	}
	if b.Stats().Flows != wantFlows || b.Stats().Reserved != wantFlows || exit.PeerCount() != 5 {
		t.Fatal("spoofed traffic allocated a mapping", b.Stats())
	}
	old := clients[0].lease
	renewed, err := lifecycle.RenewSession(old.ClientPeer, old.ID, old.Generation, old.Session)
	if err != nil {
		t.Fatal(err)
	}
	clients[0].lease = renewed
	round(clients, 10)
	pumpStarted, pumpDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(pumpDone)
		for i := 0; i < 128; i++ {
			packet := wire(TCP, renewed.IPv4.Addr(), oracle, 30000, 25, 2, nil)
			select {
			case clients[0].tun.Outbound <- packet:
				if i == 0 {
					close(pumpStarted)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	select {
	case <-pumpStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := lifecycle.ReleaseSession(ctx, renewed.ClientPeer, renewed.ID, renewed.Generation, renewed.Session); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pumpDone:
	case <-ctx.Done():
		t.Fatal("AWG plaintext pump stuck after RELEASE")
	}
	if exit.PeerCount() != 4 || b.Stats().Leases != 4 {
		t.Fatal("RELEASE did not remove only its peer")
	}
	round(clients[1:], 20)
	if err := lifecycle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if exit.PeerCount() != 0 || b.Stats().Leases != 0 {
		t.Fatal("lifecycle close residue")
	}
	if err := resources.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.Stats() != (Stats{}) {
		t.Fatal("NAT close residue", b.Stats())
	}
	t.Logf("encrypted_nat: peers=5 replies=14 echo=%t probe_retries=%d renew=PASS release_isolation=PASS spoof=DROP cleanup=PASS", echo, totalRetries)
}

// awaitEncryptedDNS retries a test application's lossy UDP exchange, never Core recovery or native I/O errors.
func awaitEncryptedDNS(ctx context.Context, packets <-chan []byte, failures <-chan error, interval time.Duration, retry func() error, validate func([]byte) (bool, error)) (int, error) {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	retries := 0
	for {
		select {
		case <-ctx.Done():
			return retries, ctx.Err()
		case err, ok := <-failures:
			if !ok || err == nil {
				err = errors.New("DNS fixture failure channel ended without a response")
			}
			return retries, err
		case packet, ok := <-packets:
			if !ok {
				return retries, errors.New("DNS fixture receive closed")
			}
			done, err := validate(packet)
			if err != nil || done {
				return retries, err
			}
		case <-timer.C:
			if retries == 2 {
				return retries, fmt.Errorf("no DNS response after three attempts: %w", context.DeadlineExceeded)
			}
			retries++
			if err := retry(); err != nil {
				return retries, err
			}
			timer.Reset(interval)
		}
	}
}

type encryptedEcho struct {
	mu        sync.Mutex
	ports     map[uint16]*fakePacketPort
	closed    bool
	dropFirst bool
	duplicate bool
}

func (e *encryptedEcho) SendIPv4(ctx context.Context, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := parse(raw)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrStale
	}
	port := e.ports[p.sourcePort]
	if port == nil {
		return ErrStale
	}
	if e.dropFirst {
		e.dropFirst = false
		return nil
	}
	payload := append([]byte(nil), raw[28:]...)
	payload[2] |= 0x80
	count := 1
	if e.duplicate {
		count = 2
	}
	for i := 0; i < count; i++ {
		select {
		case port.packets <- wire(UDP, p.destination, p.source, p.destinationPort, p.sourcePort, 0, payload):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (e *encryptedEcho) Close() error { e.mu.Lock(); defer e.mu.Unlock(); e.closed = true; return nil }

func TestEncryptedLeaseNAT(t *testing.T) {
	t.Run("normal", func(t *testing.T) { testEncryptedLeaseNAT(t, false) })
	t.Run("loss-and-duplicates", func(t *testing.T) { testEncryptedLeaseNAT(t, true) })
}

func testEncryptedLeaseNAT(t *testing.T, impairment bool) {
	encryptedLeaseScenario(t, func(deliver Emit) (*Backend, error) {
		echo := &encryptedEcho{ports: make(map[uint16]*fakePacketPort), dropFirst: impairment, duplicate: impairment}
		path := Path{1, 9, netip.MustParseAddr("203.0.113.9")}
		return NewBackend(DefaultConfig(path, nil), echo, func(context.Context, Path, uint8) (PacketPort, error) {
			echo.mu.Lock()
			defer echo.mu.Unlock()
			p := &fakePacketPort{port: uint16(41000 + len(echo.ports)), packets: make(chan []byte, 16), fault: make(chan error, 1), closed: make(chan struct{})}
			echo.ports[p.port] = p
			return p, nil
		}, deliver)
	}, remote)
}
