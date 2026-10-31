// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/sys/windows"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

func nativeDPOwner() wfp.Config {
	return wfp.Config{Provider: windows.GUID{Data1: 0xb6f22314, Data2: 0xe698, Data3: 0x4ed9, Data4: [8]byte{0x90, 0x35, 0x83, 0x15, 0x3a, 0x1a, 0x66, 0x17}},
		Sublayer: windows.GUID{Data1: 0xcb7e4d92, Data2: 0x1176, Data3: 0x4edf, Data4: [8]byte{0x9a, 0x5c, 0x76, 0xde, 0x38, 0x2e, 0x1c, 0x53}},
		Name:     "IRVIO native SessionInstaller lab", Marker: "irvio-client-dp-v1", ServiceName: "IRVIO-W05-Owner", Weight: 49200}
}

func TestWindowsNativeClientDPCleanup(t *testing.T) {
	if os.Getenv("IRVIO_CLIENT_DP_CLEANUP") != "1" {
		t.Skip("explicit lab cleanup")
	}
	s, err := wfp.NewStore(nativeDPOwner())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Inspect(context.Background())
	if err != nil || snap.Installed {
		t.Fatal("WFP residue", err)
	}
}

type nativeDPCloser func() error

func (f nativeDPCloser) Close() error { return f() }

func TestWindowsNativeClientDataPlane(t *testing.T) {
	if os.Getenv("IRVIO_CLIENT_DP_NATIVE") != "1" {
		t.Skip("disposable SYSTEM VM only; this fixture uses a test BIND callback, not network BIND")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := wfp.NewStore(nativeDPOwner())
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Inspect(ctx)
	if err != nil || prior.Installed {
		t.Fatal("preexisting WFP fixture", err)
	}
	t.Cleanup(func() {
		if err := store.Remove(context.Background()); err != nil {
			t.Error(err)
		}
	})
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	app, err := wfp.ApplicationIDForPath(self)
	if err != nil {
		t.Fatal(err)
	}
	policy := killswitch.Policy{TUNName: "IRVIO-DP", BypassMark: 1}
	backend, err := killswitch.NewWindowsBackend(killswitch.WindowsBackendConfig{Policy: policy, Owner: nativeDPOwner(), Network: killswitch.WindowsNetwork{Application: app}})
	if err != nil {
		t.Fatal(err)
	}
	installation, _, err := backend.Ensure(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	updater, err := backend.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	planner := transportgen.BindPlannerFunc(func(networkstate.Snapshot) (transportgen.BindPlan, error) {
		return transportgen.BindPlan{IPv4: netip.MustParseAddr("127.0.0.1")}, nil
	})
	cfg := transportgen.DefaultConfig(planner)
	cfg.Validation.AllowLoopback = true
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	builder, err := transportgen.NewBuilder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	transports, err := NewTransportOwner(builder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { transports.Close() })
	privateA, publicA, idA := nativeDPIdentity(t)
	privateB, publicB, idB := nativeDPIdentity(t)
	plane, err := NewClientDataPlane(ClientDataPlaneConfig{LocalPeer: idA.String(), PrivateKey: privateA, TUNName: "IRVIO-DP", Transports: transports,
		Platform: ClientDataPlanePlatform{Protection: updater, JournalDirectory: filepath.Join(t.TempDir(), "private"), DNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := plane.Close(); err != nil {
			t.Error(err)
		}
	})
	lease := tunnellease.Lease{ID: tunnellease.ID{1}, Session: tunnellease.SessionToken{1}, Generation: 1, ExitPeer: idB, ClientPeer: idA,
		IPv4: netip.MustParsePrefix("10.253.239.2/32"), ValidUntil: time.Now().Add(10 * time.Minute)}
	var previous io.Closer
	for gen := 1; gen <= 3; gen++ {
		func() {
			if err := transports.Activate(ctx, networkstate.State{Generation: networkstate.Generation(gen)}); err != nil {
				t.Fatal(err)
			}
			generation := transports.Current()
			mc := socketmux.DefaultConfig()
			mc.IPv4Bind = netip.MustParseAddr("127.0.0.1")
			mc.IPv6Policy = socketmux.FamilyDisabled
			remoteMux, err := socketmux.New(ctx, mc)
			if err != nil {
				t.Fatal(err)
			}
			defer remoteMux.Close()
			localAddress, _ := generation.Mux().LocalAddr(socketmux.IPv4)
			remoteAddress, _ := remoteMux.LocalAddr(socketmux.IPv4)
			endpoint := func(id peer.ID, a netip.AddrPort) awg.SelectedEndpoint {
				return awg.SelectedEndpoint{
					NetworkGeneration: generation.ID(), SessionGeneration: connsupervisor.SessionGeneration(gen), RemotePeer: id, Address: a,
					Candidate: iceconnect.CandidateSnapshot{Type: ice.CandidateTypeHost, Network: icesession.NetworkUDP4, Address: a.Addr(), Port: a.Port(), Component: 1}}
			}
			remoteTUN := tuntest.NewChannelTUN()
			remote, err := awg.NewTransport(awg.Config{Mux: remoteMux, NetworkGeneration: generation.ID(), TUN: remoteTUN.TUN(), PrivateKey: privateB,
				Peers: []awg.PeerConfig{{RemotePeer: idA, PublicKey: publicA, Endpoint: endpoint(idA, localAddress), AllowedIPs: []netip.Prefix{lease.IPv4}}}})
			if err != nil {
				t.Fatal(err)
			}
			defer remote.Close()
			done := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case packet := <-remoteTUN.Inbound:
						if reply := nativeDPReply(packet); reply != nil {
							select {
							case remoteTUN.Outbound <- reply:
							case <-done:
								return
							}
						}
					case <-done:
						return
					}
				}
			}()
			defer func() { close(done); wg.Wait() }()
			request := InstallRequest{LocalPeer: idA, RemotePeer: idB, Transport: generation, ICE: &iceconnect.ConnectedSession{}, SelectedEndpoint: endpoint(idB, remoteAddress), RemoteAWGKey: publicB, RemoteAWGKeyEpoch: 1, Lease: lease,
				TunnelControl: []netip.Prefix{netip.MustParsePrefix("10.253.239.1/32")}, TunnelControlEndpoint: netip.MustParseAddrPort("10.253.239.1:51822")}
			request.BeforeUserRoutes = func(context.Context) (io.Closer, error) {
				snap, err := store.Inspect(ctx)
				if err != nil {
					return nil, err
				}
				for _, rule := range snap.Rules {
					if rule.Priority == 200 {
						return nil, fmt.Errorf("user grant before BIND callback")
					}
				}
				iface, err := net.InterfaceByName("IRVIO-DP")
				if err != nil {
					return nil, err
				}
				row := windows.MibIfRow2{InterfaceIndex: uint32(iface.Index)}
				if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
					return nil, err
				}
				if _, err := winroute.ResolveTunnel(request.TunnelControlEndpoint.Addr(), row.InterfaceLuid); err != nil {
					return nil, err
				}
				nativeDPRoundTrip(t, false)
				return nativeDPCloser(func() error {
					if plane.networkFloor > generation.ID() {
						return nil
					}
					snap, err := store.Inspect(context.Background())
					if err != nil {
						return err
					}
					for _, rule := range snap.Rules {
						if rule.Priority == 200 {
							return nil
						}
					}
					return fmt.Errorf("control closed after tunnel permission was removed")
				}), nil
			}
			resource, err := plane.Install(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if err := resource.(SessionPromoter).Promote(ctx); err != nil {
				t.Fatal(err)
			}
			if previous != nil {
				if err := previous.Close(); err != nil {
					t.Fatal(err)
				}
			}
			nativeDPRoundTrip(t, true)
			stale, err := plane.Install(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if err := plane.ReconcileNetwork(ctx, networkstate.State{Generation: generation.ID() + 1}); err != nil {
				t.Fatal(err)
			}
			if err := stale.(SessionPromoter).Promote(ctx); !errors.Is(err, ErrGenerationStale) {
				t.Fatal("pre-migration staged session promoted", err)
			}
			if err := stale.Close(); err != nil {
				t.Fatal(err)
			}
			nativeDPRoundTrip(t, false)
			previous = resource
			t.Logf("generation=%d real Wintun/AWG roundtrip; pre-BIND and migration user packets blocked; stale promotion and Close fenced", gen)
		}()
	}
	if err := previous.Close(); err != nil {
		t.Fatal(err)
	}
	if err := plane.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := net.InterfaceByName("IRVIO-DP"); err == nil {
		t.Fatal("TUN residue")
	}
	if err := installation.Remove(ctx); err != nil {
		t.Fatal(err)
	}
}

func nativeDPIdentity(t *testing.T) (awg.Key, awg.Key, peer.ID) {
	t.Helper()
	var private, public awg.Key
	if _, err := rand.Read(private[:]); err != nil {
		t.Fatal(err)
	}
	pub, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	copy(public[:], pub)
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

func nativeDPRoundTrip(t *testing.T, want bool) {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("10.253.239.2")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(1200 * time.Millisecond))
	message := []byte("irvio-native-installer-roundtrip")
	_, writeErr := c.WriteToUDP(message, &net.UDPAddr{IP: net.ParseIP("10.253.239.1"), Port: 49497})
	buf := make([]byte, 256)
	n, _, readErr := c.ReadFromUDP(buf)
	if want && (writeErr != nil || readErr != nil || !bytes.Equal(buf[:n], message)) {
		t.Fatalf("AWG traffic: write=%v read=%v", writeErr, readErr)
	}
	if !want && readErr == nil {
		t.Fatal("user packet completed before BIND callback")
	}
}

func nativeDPReply(packet []byte) []byte {
	if len(packet) < 28 || packet[0] != 0x45 || packet[9] != 17 {
		return nil
	}
	p := append([]byte(nil), packet...)
	source := [4]byte(p[12:16])
	copy(p[12:16], p[16:20])
	copy(p[16:20], source[:])
	p[10], p[11] = 0, 0
	binary.BigEndian.PutUint16(p[10:12], nativeDPChecksum(p[:20]))
	u := p[20:]
	u[0], u[1], u[2], u[3] = u[2], u[3], u[0], u[1]
	u[6], u[7] = 0, 0
	pseudo := append([]byte(nil), p[12:20]...)
	pseudo = append(pseudo, 0, 17, byte(len(u)>>8), byte(len(u)))
	sum := nativeDPChecksum(append(pseudo, u...))
	if sum == 0 {
		sum = 0xffff
	}
	binary.BigEndian.PutUint16(u[6:8], sum)
	return p
}

func nativeDPChecksum(p []byte) uint16 {
	var sum uint32
	for len(p) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(p))
		p = p[2:]
	}
	if len(p) > 0 {
		sum += uint32(p[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
