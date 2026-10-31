// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/sessioncontrol"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/Irviodotnet/Irvio/test/support/clientdplab"
)

func TestWindowsNativeClientDataPlaneNetworkBind(t *testing.T) {
	if os.Getenv("IRVIO_CLIENT_DP_NETWORK_BIND") != "1" {
		t.Skip("disposable SYSTEM VM and Linux fixture required")
	}
	testWindowsNativeNetworkBind(t, false)
}

func testWindowsNativeNetworkBind(t *testing.T, dualRole bool) {
	target, err := netip.ParseAddrPort(os.Getenv("IRVIO_DP_PEER_LISTEN"))
	nonce := os.Getenv("IRVIO_DP_PEER_NONCE")
	if err != nil || !target.Addr().Is4() || !target.Addr().IsPrivate() || len(nonce) != 64 {
		t.Fatal("explicit private lab target and nonce required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	path, err := winroute.ResolveUnderlay(target.Addr(), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dualRole {
		nativeExitPhysicalDNSProbe(t, path.Binding, true)
	}
	dialer := net.Dialer{Control: path.Binding.Control}
	coord, err := dialer.DialContext(ctx, "tcp4", target.String())
	if err != nil {
		t.Fatal(err)
	}
	defer coord.Close()
	coord.SetDeadline(time.Now().Add(85 * time.Second))
	decoder, encoder := json.NewDecoder(io.LimitReader(coord, 8192)), json.NewEncoder(coord)
	identity, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(identity)
	if err != nil {
		t.Fatal(err)
	}
	private, public, _ := nativeDPIdentity(t)
	if err := encoder.Encode(clientdplab.Hello{Nonce: nonce, Peer: id.String(), PublicKey: public}); err != nil {
		t.Fatal(err)
	}
	var ready clientdplab.Ready
	if err := decoder.Decode(&ready); err != nil {
		t.Fatal(err)
	}
	remote, err := peer.Decode(ready.Peer)
	if err != nil || ready.Lease.ClientPeer != id || ready.Lease.ExitPeer != remote || ready.UDP.Addr() != target.Addr() {
		t.Fatal("bad fixture readiness")
	}
	config := transportgen.DefaultConfig(transportgen.BindPlannerFunc(func(networkstate.Snapshot) (transportgen.BindPlan, error) {
		return transportgen.BindPlan{IPv4: path.Binding.Source}, nil
	}))
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	builder, err := transportgen.NewBuilder(config)
	if err != nil {
		t.Fatal(err)
	}
	transports, err := NewTransportOwner(builder)
	if err != nil {
		t.Fatal(err)
	}
	defer transports.Close()
	state := networkstate.State{Generation: 1, Snapshot: networkstate.Snapshot{Links: []networkstate.Link{{Index: int(path.Binding.Index), NativeID: path.Binding.LUID, Up: true, Addresses: []netip.Prefix{netip.PrefixFrom(path.Binding.Source, 32)}}}}}
	if err := transports.Activate(ctx, state); err != nil {
		t.Fatal(err)
	}
	generation := transports.Current()
	if _, err := generation.Mux().Control().WriteTo(ctx, []byte(nonce), ready.UDP); err != nil {
		t.Fatal(err)
	}
	var mapped clientdplab.Mapped
	if err := decoder.Decode(&mapped); err != nil {
		t.Fatal(err)
	}
	if !mapped.Endpoint.IsValid() {
		t.Fatal("missing observed lab UDP mapping")
	}
	store, err := wfp.NewStore(nativeDPOwner())
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Inspect(ctx)
	if err != nil || before.Installed {
		t.Fatal("preexisting policy", err)
	}
	defer func() {
		if err := store.Remove(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	app, err := wfp.ApplicationIDForPath(exe)
	if err != nil {
		t.Fatal(err)
	}
	policy := killswitch.Policy{TUNName: "IRVIO-DP", BypassMark: 1}
	backend, err := killswitch.NewWindowsBackend(killswitch.WindowsBackendConfig{Policy: policy, Owner: nativeDPOwner(), Network: killswitch.WindowsNetwork{Application: app, Underlay: []killswitch.WindowsBypass{{Interface: path.Binding.LUID, Source: path.Binding.Source, Remote: ready.UDP, Protocol: 17}}}})
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
	if dualRole {
		nativeExitPhysicalDNSProbe(t, path.Binding, false)
	}
	var plane interface {
		SessionInstaller
		io.Closer
	}
	var dual *nativeExitBindPlane
	if dualRole {
		dual, err = newNativeExitBindPlane(t, ctx, identity, private, public, transports, updater, ready)
		if dual != nil {
			plane = dual
		}
	} else {
		plane, err = NewClientDataPlane(ClientDataPlaneConfig{LocalPeer: id.String(), PrivateKey: private, TUNName: "IRVIO-DP", Transports: transports, Platform: ClientDataPlanePlatform{Protection: updater, JournalDirectory: filepath.Join(t.TempDir(), "private"), DNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}})
	}
	if plane != nil {
		defer func() {
			if err := plane.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	if dual != nil {
		dual.exchange(t, "before-own-bind")
	}
	var control *sessioncontrol.ReconnectingClient
	request := InstallRequest{LocalPeer: id, RemotePeer: remote, Transport: generation, ICE: &iceconnect.ConnectedSession{}, RemoteAWGKey: ready.PublicKey, RemoteAWGKeyEpoch: 1, Lease: ready.Lease,
		SelectedEndpoint: awg.SelectedEndpoint{NetworkGeneration: 1, SessionGeneration: 1, RemotePeer: remote, Address: ready.UDP, Candidate: iceconnect.CandidateSnapshot{Type: ice.CandidateTypeHost, Network: icesession.NetworkUDP4, Address: ready.UDP.Addr(), Port: ready.UDP.Port(), Component: 1}},
		TunnelControl:    []netip.Prefix{netip.MustParsePrefix("10.253.239.1/32")}, TunnelControlEndpoint: netip.MustParseAddrPort("10.253.239.1:51822")}
	request.BeforeUserRoutes = func(bindCtx context.Context) (io.Closer, error) {
		nativeDPRoundTrip(t, false)
		snapshot, err := store.Inspect(bindCtx)
		if err != nil {
			return nil, err
		}
		for _, rule := range snapshot.Rules {
			if rule.Priority == 200 {
				t.Fatal("user tunnel permission before BIND")
			}
		}
		invalidLease := ready.Lease
		invalidLease.Session[0] ^= 0xff
		wrong, rejectErr := sessioncontrol.Dial(bindCtx, request.TunnelControlEndpoint.String(), identity, remote,
			invalidLease, sessioncontrol.TunnelDialer("IRVIO-DP"), 5*time.Second)
		if wrong != nil {
			_ = wrong.Close()
		}
		if !errors.Is(rejectErr, sessioncontrol.ErrRemote) {
			t.Fatalf("wrong session token was not explicitly rejected: %v", rejectErr)
		}
		t.Log("authenticated peer with wrong lease session token rejected by real Linux authority")
		control, err = sessioncontrol.DialReconnecting(bindCtx, sessioncontrol.ReconnectingConfig{Address: request.TunnelControlEndpoint.String(), PrivateKey: identity, ExpectedExit: remote, Lease: ready.Lease, Dial: sessioncontrol.TunnelDialer("IRVIO-DP"), Timeout: 10 * time.Second})
		if err != nil {
			return nil, err
		}
		t.Log("real Noise BIND completed through physical underlay + AWG + Wintun")
		return nativeDPCloser(func() error {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer releaseCancel()
			return errors.Join(control.Release(releaseCtx, control.Current()), control.Close())
		}), nil
	}
	resource, err := plane.Install(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.(SessionPromoter).Promote(ctx); err != nil {
		t.Fatal(err)
	}
	nativeDPRoundTrip(t, true)
	if dual != nil {
		dual.exchange(t, "own-connected")
		dual.checkAutomaticSource(t)
		nativeExitPhysicalDNSProbe(t, path.Binding, false)
	}
	renewed, err := control.Renew(ctx, control.Current())
	if err != nil || renewed.Generation <= ready.Lease.Generation {
		t.Fatal("real renewal failed", err)
	}
	if err := resource.Close(); err != nil {
		t.Fatal(err)
	}
	if dual != nil {
		dual.exchange(t, "after-own-release")
		nativeExitPhysicalDNSProbe(t, path.Binding, false)
	}
	if err := plane.Close(); err != nil {
		t.Fatal(err)
	}
	if err := installation.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if dualRole {
		nativeExitPhysicalDNSProbe(t, path.Binding, true)
	}
	var result clientdplab.Result
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.Released || result.Remaining != 0 || result.Renewed != 1 {
		t.Fatalf("Linux authority result: %+v", result)
	}
	t.Log("real Linux lease authority confirmed RENEW and RELEASE, zero active leases; synthetic ICE fixture only")
}
