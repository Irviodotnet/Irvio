// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"golang.org/x/crypto/curve25519"
)

type exitPlaneTransport struct {
	current *transportgen.Generation
	commits int
	fail    bool
}

func (t *exitPlaneTransport) Activate(context.Context, networkstate.State) error { return nil }
func (t *exitPlaneTransport) Current() *transportgen.Generation                  { return t.current }
func (t *exitPlaneTransport) Commit(networkstate.Generation) error {
	t.commits++
	if t.fail {
		return errWindowsRouteInjected
	}
	return nil
}
func (*exitPlaneTransport) Close() error { return nil }

type exitPlaneAdapter struct {
	*generationChannelOwner
	dynamic []netip.Prefix
	closed  bool
	trace   *windowsRouteTrace
}

func (*exitPlaneAdapter) LUID() uint64                                { return 42 }
func (*exitPlaneAdapter) SetAutomaticSource(netip.Prefix, bool) error { return nil }
func (a *exitPlaneAdapter) AddAddresses(v []netip.Prefix) error {
	a.dynamic = slices.Clone(v)
	return a.trace.step("add-address")
}
func (a *exitPlaneAdapter) RemoveAddresses(v []netip.Prefix) error {
	if err := a.trace.step("remove-address"); err != nil {
		return err
	}
	if len(a.dynamic) != 0 && !slices.Equal(a.dynamic, v) {
		return ErrInvalidConfig
	}
	a.dynamic = nil
	return nil
}
func (a *exitPlaneAdapter) Close() error { a.closed = true; return nil }

func exitPlaneFixture(t *testing.T) (*windowsExitDataPlane, ResolvedBinding, *generationWire, *exitPlaneAdapter) {
	t.Helper()
	c, binding := generationConfig(t)
	c.Platform = ClientDataPlanePlatform{Protection: &killswitch.WindowsNetworkUpdater{}, JournalDirectory: t.TempDir(), DNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}
	g := newRuntimeGeneration(t)
	c.Transports = &exitPlaneTransport{current: g}
	wire := &generationWire{}
	a := &exitPlaneAdapter{generationChannelOwner: newGenerationChannelOwner(), trace: &windowsRouteTrace{}}
	p := &windowsExitDataPlane{config: c, device: &windowsExitDevice{tun: a}, pending: make(map[tunnellease.SessionToken]*pendingExitSession), retired: make(map[tunnellease.SessionToken]*pendingExitSession), leases: make(map[tunnellease.ID]*windowsExitLease), changed: make(chan struct{}), ownProtection: a.trace}
	p.openGeneration = func(ctx context.Context, g *transportgen.Generation) (*windowsExitGeneration, error) {
		return openWindowsExitGeneration(ctx, p.config, a, g, wire.backend, &p.traffic)
	}
	p.configureOwn = func(ctx context.Context, u *windowsExitOwnSession) error {
		u.routing.routes, u.routing.dns = windowsRouteFake{a.trace}, windowsDNSFake{a.trace}
		r := u.request
		r.BeforeUserRoutes = func(ctx context.Context) (io.Closer, error) {
			control, err := u.request.BeforeUserRoutes(ctx)
			u.control = control
			return control, err
		}
		_, err := u.routing.activate(ctx, a.LUID(), r, c.Platform.DNSServers)
		return err
	}
	t.Cleanup(func() {
		wire.failClose.Store(false)
		a.trace.fail = ""
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p, binding, wire, a
}

func exitPlaneAdmit(t *testing.T, p *windowsExitDataPlane, c generationClient) {
	t.Helper()
	s := &pendingExitSession{token: c.lease.Session, remote: c.lease.ClientPeer, key: c.binding.Key, keyEpoch: c.binding.Binding.KeyEpoch, endpoint: c.endpoint, ice: &iceconnect.ConnectedSession{}, createdAt: time.Now()}
	if err := p.registerPending(s); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.ActivateLease(ctx, c.lease); err != nil {
		t.Fatal(err)
	}
}

func exitPlaneRequest(p *windowsExitDataPlane, c generationClient, x *windowsRouteTrace) InstallRequest {
	return InstallRequest{LocalPeer: p.config.LocalPeer, RemotePeer: c.binding.Binding.PeerID, Transport: p.config.Transports.Current(), ICE: &iceconnect.ConnectedSession{}, SelectedEndpoint: c.endpoint,
		RemoteAWGKey: c.binding.Key, RemoteAWGKeyEpoch: c.binding.Binding.KeyEpoch, Lease: c.lease,
		TunnelControl: []netip.Prefix{netip.MustParsePrefix("10.253.244.1/32")}, TunnelControlEndpoint: netip.MustParseAddrPort("10.253.244.1:7890"),
		BeforeUserRoutes: func(context.Context) (io.Closer, error) {
			if err := x.step("bind"); err != nil {
				return nil, err
			}
			return windowsControlFake{x}, nil
		},
	}
}

func exitPlaneForwardedReply(packet []byte) []byte {
	reply := nativeDPReply(packet)
	reply[8] -= 2
	reply[10], reply[11] = 0, 0
	binary.BigEndian.PutUint16(reply[10:12], nativeDPChecksum(reply[:20]))
	return reply
}

func TestWindowsExitPlaneFiveClientsAndOwnSession(t *testing.T) {
	p, binding, _, a := exitPlaneFixture(t)
	var clients []generationClient
	for i := byte(2); i < 7; i++ {
		c := generationClientPeer(t, p.config, binding, p.config.Transports.Current(), i, false)
		exitPlaneAdmit(t, p, c)
		clients = append(clients, c)
	}
	gen := p.active
	up := generationClientPeer(t, p.config, binding, p.config.Transports.Current(), 10, true)
	r := exitPlaneRequest(p, up, a.trace)
	staged, err := p.Install(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := staged.(SessionPromoter).Promote(ctx); err != nil {
		t.Fatal(err)
	}
	if p.active != gen || p.upstream == nil || !p.upstream.ready || len(p.leases) != 5 || a.borrowed != 1 {
		t.Fatal("own promotion replaced downstream generation")
	}
	for _, c := range clients {
		packet := generationPacket(c.lease.IPv4.Addr(), netip.MustParseAddr("1.1.1.1"), []byte("downstream"))
		if got := generationExchange(t, c.tun, packet); !slices.Equal(got, exitPlaneForwardedReply(packet)) {
			t.Fatalf("forwarded reply mismatch: got=%x want=%x", got, exitPlaneForwardedReply(packet))
		}
	}
	own := generationPacket(up.lease.IPv4.Addr(), netip.MustParseAddr("8.8.8.8"), []byte("own upstream"))
	select {
	case a.channel.Outbound <- own:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case got := <-up.tun.Inbound:
		if !slices.Equal(got, own) {
			t.Fatal("own path mismatch")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := p.RevokeLease(ctx, clients[0].lease, tunnellease.RevokeAdministrative); err != nil {
		t.Fatal(err)
	}
	if p.upstream == nil || !p.upstream.ready || p.active != gen {
		t.Fatal("downstream revoke destroyed own upstream")
	}
	a.trace.events = nil
	if err := staged.Close(); err != nil {
		t.Fatal(err)
	}
	if p.upstream != nil || p.active != gen || len(p.leases) != 4 || len(a.dynamic) != 0 {
		t.Fatal("own close destroyed downstream")
	}
	if !reflect.DeepEqual(a.trace.events, []string{"release", "revoke", "close-dns", "close-routes", "remove-address"}) {
		t.Fatal("wrong cleanup order", a.trace.events)
	}
	packet := generationPacket(clients[1].lease.IPv4.Addr(), netip.MustParseAddr("1.1.1.1"), []byte("survives-own-close"))
	if got := generationExchange(t, clients[1].tun, packet); !slices.Equal(got, exitPlaneForwardedReply(packet)) {
		t.Fatal("remaining downstream broken")
	}
	if err := staged.Close(); err != nil {
		t.Fatal(err)
	}
	if err := staged.(SessionPromoter).Promote(ctx); !errors.Is(err, ErrClosed) {
		t.Fatal("closed stage rearmed", err)
	}
}

func TestWindowsExitPlaneOwnFailuresRetainCleanup(t *testing.T) {
	for _, stage := range []string{"add-address", "control-grant", "control-route", "bind", "remove-control-route", "user-route", "dns", "user-grant", "release", "revoke", "close-dns", "close-routes", "remove-address"} {
		t.Run(stage, func(t *testing.T) {
			p, binding, _, a := exitPlaneFixture(t)
			up := generationClientPeer(t, p.config, binding, p.config.Transports.Current(), 10, true)
			s, err := p.Install(context.Background(), exitPlaneRequest(p, up, a.trace))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cleanupStage := slices.Contains([]string{"release", "revoke", "close-dns", "close-routes", "remove-address"}, stage)
			if !cleanupStage {
				a.trace.fail = stage
			}
			err = s.(SessionPromoter).Promote(ctx)
			if cleanupStage {
				if err != nil {
					t.Fatal(err)
				}
				a.trace.fail = stage
				err = s.Close()
			}
			if !errors.Is(err, errWindowsRouteInjected) {
				t.Fatal("missing failure", err)
			}
			if cleanupStage && p.upstream == nil {
				t.Fatal("lost failed cleanup owner")
			}
			a.trace.fail = ""
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if p.upstream != nil || len(a.dynamic) != 0 || p.active.upstream != nil {
				t.Fatal("upstream residue")
			}
		})
	}
}

func TestWindowsExitPlaneCommitFailureRetainsGeneration(t *testing.T) {
	p, binding, wire, _ := exitPlaneFixture(t)
	c := generationClientPeer(t, p.config, binding, p.config.Transports.Current(), 2, false)
	p.config.Transports.(*exitPlaneTransport).fail = true
	s := &pendingExitSession{token: c.lease.Session, remote: c.lease.ClientPeer, key: c.binding.Key, keyEpoch: c.binding.Binding.KeyEpoch, endpoint: c.endpoint, ice: &iceconnect.ConnectedSession{}, createdAt: time.Now()}
	if err := p.registerPending(s); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.ActivateLease(ctx, c.lease); !errors.Is(err, errWindowsRouteInjected) {
		t.Fatal(err)
	}
	if p.active == nil || p.ready {
		t.Fatal("failed committed generation lost or published")
	}
	wire.failClose.Store(true)
	if err := p.Close(); err == nil || p.active == nil || p.device == nil {
		t.Fatal("failed generation owner lost", err)
	}
	wire.failClose.Store(false)
	if err := p.Close(); err != nil || p.active != nil || p.device != nil {
		t.Fatal("cleanup retry failed", err)
	}
}

func TestWindowsExitJournalRetryUsesDurableRecovery(t *testing.T) {
	closed, recovered := 0, 0
	c := windowsExitJournalCleanup{close: func() error { closed++; return errWindowsRouteInjected }, recover: func(ctx context.Context) error {
		recovered++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded recovery")
		}
		if recovered == 1 {
			return errWindowsRouteInjected
		}
		return nil
	}}
	if err := c.Close(); !errors.Is(err, errWindowsRouteInjected) {
		t.Fatal(err)
	}
	if err := c.Close(); !errors.Is(err, errWindowsRouteInjected) {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil || closed != 1 || recovered != 2 {
		t.Fatal("non-idempotent recovery", closed, recovered, err)
	}
}

func TestWindowsExitPlaneOldStageCannotCloseReplacement(t *testing.T) {
	p, binding, _, a := exitPlaneFixture(t)
	up := generationClientPeer(t, p.config, binding, p.config.Transports.Current(), 10, true)
	r := exitPlaneRequest(p, up, a.trace)
	first, err := p.Install(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Install(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := second.(SessionPromoter).Promote(ctx); err != nil {
		t.Fatal(err)
	}
	current := p.upstream
	if err := first.(SessionPromoter).Promote(ctx); !errors.Is(err, ErrGenerationStale) {
		t.Fatal("stale stage promoted", err)
	}
	if err := first.Close(); err != nil || p.upstream != current || !current.ready {
		t.Fatal("old close destroyed replacement", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsExitPlaneFailedPromotionAndRollbackRetainOwner(t *testing.T) {
	p, binding, _, a := exitPlaneFixture(t)
	up := generationClientPeer(t, p.config, binding, p.config.Transports.Current(), 10, true)
	s, err := p.Install(context.Background(), exitPlaneRequest(p, up, a.trace))
	if err != nil {
		t.Fatal(err)
	}
	p.configureOwn = func(context.Context, *windowsExitOwnSession) error {
		a.trace.fail = "remove-address"
		return errWindowsRouteInjected
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.(SessionPromoter).Promote(ctx); !errors.Is(err, errWindowsRouteInjected) {
		t.Fatal(err)
	}
	if p.upstream == nil || p.upstream.ready || len(a.dynamic) != 1 {
		t.Fatal("failed promotion dropped rollback owner")
	}
	a.trace.fail = ""
	if err := s.Close(); err != nil || p.upstream != nil || len(a.dynamic) != 0 {
		t.Fatal("failed promotion cleanup could not resume", err)
	}
}

func TestWindowsExitPlaneOwnRotationRetainsExactCleanupKey(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			p, binding, _, a := exitPlaneFixture(t)
			up := generationClientPeer(t, p.config, binding, p.config.Transports.Current(), 10, true)
			s, err := p.Install(context.Background(), exitPlaneRequest(p, up, a.trace))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.(SessionPromoter).Promote(ctx); err != nil {
				t.Fatal(err)
			}
			private := awg.Key{77}
			pub, err := curve25519.X25519(private[:], curve25519.Basepoint)
			if err != nil {
				t.Fatal(err)
			}
			key := awg.Key(pub)
			if commit {
				if err := up.transport.UpdatePrivateKey(private); err != nil {
					t.Fatal(err)
				}
			}
			rotator := s.(SessionAWGKeyRotator)
			if err := rotator.StageRemoteAWGKey(up.binding.Key, key, up.binding.Binding.PeerID); err != nil {
				t.Fatal(err)
			}
			if commit {
				if err := rotator.CommitRemoteAWGKey(ctx, up.binding.Key, key, up.binding.Binding.PeerID); err != nil {
					t.Fatal(err)
				}
				if p.upstream.request.RemoteAWGKey != key || p.active.upstream.binding.Key != key {
					t.Fatal("cleanup key was not promoted")
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			for _, k := range []awg.Key{up.binding.Key, key} {
				if _, err := p.active.transport.PeerStats(k); !errors.Is(err, awg.ErrPeerNotFound) {
					t.Fatal("rotation peer residue", err)
				}
			}
		})
	}
}

func TestWindowsExitPlaneDoesNotRetireCurrentForStaleNetwork(t *testing.T) {
	p, _, _, _ := exitPlaneFixture(t)
	if err := p.ensureGeneration(context.Background(), p.config.Transports.Current()); err != nil {
		t.Fatal(err)
	}
	gen := p.active
	if err := p.ReconcileNetwork(context.Background(), networkstate.State{Generation: gen.generation.ID()}); err != nil || p.active != gen || !p.ready {
		t.Fatal(err)
	}
	if err := p.ReconcileNetwork(context.Background(), networkstate.State{Generation: gen.generation.ID() + 1}); err != nil || p.active != nil || p.ready {
		t.Fatal("network transition retained old physical NAT", err)
	}
	if err := p.ensureGeneration(context.Background(), p.config.Transports.Current()); !errors.Is(err, ErrGenerationStale) {
		t.Fatal("old transport reopened while new network is pending", err)
	}
	if err := p.ReconcileNetwork(context.Background(), networkstate.State{Generation: gen.generation.ID()}); !errors.Is(err, ErrGenerationStale) {
		t.Fatal("stale network event regressed fence", err)
	}
}
