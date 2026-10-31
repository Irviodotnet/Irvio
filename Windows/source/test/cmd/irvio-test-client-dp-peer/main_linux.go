// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"
	"github.com/vishvananda/netlink"
	"golang.org/x/crypto/curve25519"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/sessioncontrol"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/Irviodotnet/Irvio/test/support/clientdplab"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	bind, err := netip.ParseAddrPort(os.Getenv("IRVIO_DP_PEER_LISTEN"))
	nonce := os.Getenv("IRVIO_DP_PEER_NONCE")
	if err != nil || !bind.Addr().Is4() || !bind.Addr().IsPrivate() || bind.Port() == 0 || len(nonce) != 64 || os.Geteuid() != 0 {
		return fmt.Errorf("explicit private IPv4 lab endpoint, 64-character nonce and root required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	listener, err := net.ListenTCP("tcp4", net.TCPAddrFromAddrPort(bind))
	if err != nil {
		return err
	}
	defer listener.Close()
	listener.SetDeadline(time.Now().Add(60 * time.Second))
	fmt.Println("LAB_PEER_LISTENING")
	conn, err := listener.AcceptTCP()
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(120 * time.Second))
	decoder, encoder := json.NewDecoder(io.LimitReader(conn, 4096)), json.NewEncoder(conn)
	var hello clientdplab.Hello
	if err := decoder.Decode(&hello); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(nonce), []byte(hello.Nonce)) != 1 {
		return fmt.Errorf("fixture authorization rejected")
	}
	client, err := peer.Decode(hello.Peer)
	if err != nil || hello.PublicKey == (awg.Key{}) {
		return fmt.Errorf("invalid client")
	}
	identity, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return err
	}
	id, err := peer.IDFromPrivateKey(identity)
	if err != nil {
		return err
	}
	var private, public awg.Key
	if _, err := rand.Read(private[:]); err != nil {
		return err
	}
	pub, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		return err
	}
	copy(public[:], pub)
	const tunName = "irvio-dp-lab"
	if _, err := net.InterfaceByName(tunName); err == nil {
		return fmt.Errorf("fixture TUN already exists")
	}
	pool := netip.MustParsePrefix("10.253.239.0/24")
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	for _, r := range routes {
		if r.Dst == nil {
			continue
		}
		prefix, parseErr := netip.ParsePrefix(r.Dst.String())
		if parseErr == nil && prefix.Bits() != 0 && prefix.Overlaps(pool) {
			return fmt.Errorf("fixture pool overlaps a host route")
		}
	}
	owner, err := tunnel.NewLinux(tunnel.Config{Name: tunName, Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.239.1/32")}})
	if err != nil {
		return err
	}
	defer owner.Close()
	_, destination, _ := net.ParseCIDR("10.253.239.2/32")
	route := netlink.Route{LinkIndex: owner.IfIndex(), Dst: destination, Scope: netlink.SCOPE_LINK, Protocol: 99}
	if err := netlink.RouteAdd(&route); err != nil {
		return err
	}
	defer netlink.RouteDel(&route)
	cfg := socketmux.DefaultConfig()
	cfg.IPv4Bind, cfg.IPv6Policy = bind.Addr(), socketmux.FamilyDisabled
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.Classifier = socketmux.ClassifierFunc(func(p []byte, _ netip.AddrPort, _ socketmux.AddressFamily) socketmux.PacketClass {
		if bytes.Equal(p, []byte(nonce)) {
			return socketmux.Control
		}
		return socketmux.Data
	})
	mux, err := socketmux.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer mux.Close()
	udp, _ := mux.LocalAddr(socketmux.IPv4)
	allocation := tunnellease.DefaultConfig(id)
	allocation.IPv4Pool = pool
	allocation.Reserved = []netip.Addr{netip.MustParseAddr("10.253.239.1")}
	allocator, err := tunnellease.New(allocation)
	if err != nil {
		return err
	}
	defer allocator.Close()
	var transport *awg.Transport
	lifeCfg := tunnellease.DefaultLifecycleConfig(allocator, tunnellease.ResourceRevokerFunc(func(_ context.Context, _ tunnellease.Lease, _ tunnellease.RevocationReason) error {
		if transport != nil {
			return transport.RemovePeer(hello.PublicKey)
		}
		return nil
	}))
	lifeCfg.RequireSessionToken = true
	life, err := tunnellease.NewLifecycle(lifeCfg)
	if err != nil {
		return err
	}
	defer life.Close(context.Background())
	var token tunnellease.SessionToken
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	lease, err := life.AllocateForSession(client, token)
	if err != nil {
		return err
	}
	if lease.IPv4.String() != "10.253.239.2/32" {
		return fmt.Errorf("unexpected fixture allocation")
	}
	now := time.Now().UTC()
	announcement := &irviov1.NodeAnnouncement{ProtocolVersion: protocol.ProtocolVersion, NodeId: []byte(id), SessionEpoch: make([]byte, 16), Incarnation: 1, Sequence: 1, Capabilities: protocol.CapExit, Country: "DE", Libp2PAddrs: []string{fmt.Sprintf("/ip4/%s/tcp/%d", bind.Addr(), bind.Port())}, AwgPublicKey: public[:], AwgKeyEpoch: 1, CreatedAtUnix: now.Add(-time.Second).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix()}
	announcement.SessionEpoch[0] = 1
	signed, err := protocol.SealAnnouncement(identity, announcement, now)
	if err != nil {
		return err
	}
	if err := encoder.Encode(clientdplab.Ready{Peer: id.String(), PublicKey: public, Lease: lease, UDP: udp, SignedDescriptor: signed}); err != nil {
		return err
	}
	mapped, err := mux.Control().Read(ctx)
	if err != nil {
		return err
	}
	borrowed, err := owner.Borrow()
	if err != nil {
		return err
	}
	transport, err = awg.NewTransport(awg.Config{Mux: mux, NetworkGeneration: 1, TUN: borrowed, PrivateKey: private, Peers: []awg.PeerConfig{{RemotePeer: client, PublicKey: hello.PublicKey, AllowedIPs: []netip.Prefix{lease.IPv4}, Endpoint: awg.SelectedEndpoint{
		NetworkGeneration: 1, SessionGeneration: 1, RemotePeer: client, Address: mapped.Source, Candidate: iceconnect.CandidateSnapshot{Type: ice.CandidateTypeServerReflexive, Network: icesession.NetworkUDP4, Address: mapped.Source.Addr(), Port: mapped.Source.Port(), Component: 1},
	}}}})
	if err != nil {
		borrowed.Close()
		return err
	}
	defer transport.Close()
	controlListener, err := sessioncontrol.ListenTunnel(ctx, "tcp4", "10.253.239.1:51822", tunName)
	if err != nil {
		return err
	}
	server, err := sessioncontrol.NewServer(controlListener, identity, sessioncontrol.LifecycleAuthority{Lifecycle: life}, 10*time.Second)
	if err != nil {
		controlListener.Close()
		return err
	}
	defer server.Close()
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("10.253.239.1"), Port: 49497})
	if err != nil {
		return err
	}
	defer echo.Close()
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		buf := make([]byte, 2048)
		for {
			n, a, e := echo.ReadFromUDP(buf)
			if e != nil {
				return
			}
			if a.IP.String() == "10.253.239.2" {
				_, _ = echo.WriteToUDP(buf[:n], a)
			}
		}
	}()
	defer func() { echo.Close(); <-echoDone }()
	if err := encoder.Encode(clientdplab.Mapped{Endpoint: mapped.Source}); err != nil {
		return err
	}
	fmt.Println("LAB_AWG_PEER_READY")
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if life.Stats().Revoked != 0 {
			result := clientdplab.Result{Released: true, Remaining: len(allocator.Active()), Renewed: life.Stats().Renewed}
			if result.Remaining != 0 || result.Renewed == 0 {
				return fmt.Errorf("lease lifecycle incomplete")
			}
			if err := encoder.Encode(result); err != nil {
				return err
			}
			fmt.Println("REAL_NOISE_BIND_RENEW_RELEASE_PASS")
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(fmt.Errorf("lease was not released"), ctx.Err())
		case <-ticker.C:
		}
	}
}
