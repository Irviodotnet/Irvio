// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnel

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"
	"golang.org/x/crypto/curve25519"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestWindowsNativeAWGGenerationReplacement(t *testing.T) {
	nativeTUNEnabled(t)
	w := nativeOwner(t, 4)
	for generation := 1; generation <= 3; generation++ {
		func() {
			aMux, bMux := nativeAWGMux(t), nativeAWGMux(t)
			defer aMux.Close()
			defer bMux.Close()
			privateA, publicA, idA := nativeAWGIdentity(t)
			privateB, publicB, idB := nativeAWGIdentity(t)
			aEndpoint, _ := aMux.LocalAddr(socketmux.IPv4)
			bEndpoint, _ := bMux.LocalAddr(socketmux.IPv4)
			path := func(id peer.ID, addr netip.AddrPort) awg.SelectedEndpoint {
				return awg.SelectedEndpoint{
					NetworkGeneration: networkstate.Generation(generation), SessionGeneration: connsupervisor.SessionGeneration(generation), RemotePeer: id, Address: addr,
					Candidate: iceconnect.CandidateSnapshot{Type: ice.CandidateTypeHost, Network: icesession.NetworkUDP4, Address: addr.Addr(), Port: addr.Port(), Component: 1},
				}
			}
			b, err := w.Borrow()
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			remote := tuntest.NewChannelTUN()
			obfuscation := awg.ObfuscationConfig{JunkPacketCount: 1, JunkMin: 24, JunkMax: 24, InitPadding: 16, ResponsePadding: 16, CookiePadding: 8, InitHeader: 101, ResponseHeader: 202, CookieHeader: 303, TransportHeader: 404}
			local, err := awg.NewTransport(awg.Config{Mux: aMux, NetworkGeneration: networkstate.Generation(generation), TUN: b, PrivateKey: privateA, Obfuscation: obfuscation,
				Peers: []awg.PeerConfig{{RemotePeer: idB, PublicKey: publicB, Endpoint: path(idB, bEndpoint), AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.253.242.2/32"), netip.MustParsePrefix("fd77:242::2/128")}}}})
			if err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			other, err := awg.NewTransport(awg.Config{Mux: bMux, NetworkGeneration: networkstate.Generation(generation), TUN: remote.TUN(), PrivateKey: privateB, Obfuscation: obfuscation,
				Peers: []awg.PeerConfig{{RemotePeer: idA, PublicKey: publicA, Endpoint: path(idA, aEndpoint), AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.253.242.1/32"), netip.MustParsePrefix("fd77:242::1/128")}}}})
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			if err := local.TriggerHandshake(publicB); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := local.WaitHandshake(ctx, publicB); err != nil {
				t.Fatal(err)
			}
			if _, err := other.WaitHandshake(ctx, publicA); err != nil {
				t.Fatal(err)
			}
			for _, pair := range [][2]string{{"10.253.242.1", "10.253.242.2"}, {"fd77:242::1", "fd77:242::2"}} {
				nativeEncryptedRoundTrip(t, remote, pair[0], pair[1], generation)
			}
			for _, transport := range []*awg.Transport{local, other} {
				done := make(chan error, 1)
				go func() { done <- transport.Close() }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("AWG Close stuck on borrowed TUN")
				}
			}
			assertNativeOwner(t, w)
		}()
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	requireNativeGone(t, w)
}

func nativeAWGMux(t *testing.T) *socketmux.Mux {
	t.Helper()
	config := socketmux.DefaultConfig()
	config.IPv6Policy = socketmux.FamilyDisabled
	config.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	mux, err := socketmux.New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mux.Close() })
	return mux
}

func nativeAWGIdentity(t *testing.T) (awg.Key, awg.Key, peer.ID) {
	t.Helper()
	var private, public awg.Key
	if _, err := rand.Read(private[:]); err != nil {
		t.Fatal(err)
	}
	p, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	copy(public[:], p)
	k, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return private, public, id
}

func nativeEncryptedRoundTrip(t *testing.T, remote *tuntest.ChannelTUN, source, dest string, generation int) {
	t.Helper()
	network := "udp4"
	if netip.MustParseAddr(source).Is6() {
		network = "udp6"
	}
	socket, err := net.ListenUDP(network, &net.UDPAddr{IP: net.ParseIP(source)})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	socket.SetDeadline(time.Now().Add(5 * time.Second))
	payload := []byte(fmt.Sprintf("native-encrypted-%s-%d", network, generation))
	if _, err := socket.WriteToUDP(payload, &net.UDPAddr{IP: net.ParseIP(dest), Port: 49497}); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case p := <-remote.Inbound:
			if !bytes.HasSuffix(p, payload) {
				continue
			}
			reply := nativeUDPReply(p)
			if reply == nil {
				t.Fatal("invalid decrypted IP/UDP")
			}
			select {
			case remote.Outbound <- reply:
			case <-timer.C:
				t.Fatal("AWG return timeout")
			}
		case <-timer.C:
			t.Fatal("AWG did not decrypt kernel packet")
		}
		break
	}
	buffer := make([]byte, 256)
	n, from, err := socket.ReadFromUDP(buffer)
	if err != nil || !bytes.Equal(buffer[:n], payload) || !from.IP.Equal(net.ParseIP(dest)) {
		t.Fatalf("AWG reply mismatch: %v", err)
	}
	t.Logf("native %s generation=%d kernel/Wintun/AWG/mux/encrypted-peer roundtrip PASS", network, generation)
}
