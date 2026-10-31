// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/reachability"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/libp2p/go-libp2p/core/peer"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
)

type windowsRuntimeLab struct {
	ctx     context.Context
	logger  *slog.Logger
	factory tcptransport.DialerForAddr
	calls   atomic.Int32
}

func newWindowsRuntimeLab(t *testing.T) *windowsRuntimeLab {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	lab := &windowsRuntimeLab{ctx: ctx, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	factory, err := controlsocket.DialerForBindings(func(_ context.Context, target netip.Addr) (winroute.Binding, error) {
		lab.calls.Add(1)
		source := netip.MustParseAddr("127.0.0.1")
		if target.Is6() {
			source = netip.IPv6Loopback()
		}
		return winroute.ForSource(source)
	})
	if err != nil {
		t.Fatal(err)
	}
	lab.factory = factory
	return lab
}

func (l *windowsRuntimeLab) node(t *testing.T, config discovery.Config) *discovery.Node {
	t.Helper()
	config.DataDir, config.DHTMode, config.Logger = filepath.Join(t.TempDir(), "profile"), "server", l.logger
	config.ControlDialer = l.factory
	node, err := discovery.Start(l.ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.Close(); err != nil {
			t.Error(err)
		}
	})
	return node
}

func (l *windowsRuntimeLab) config() PublicRoleConfig {
	return PublicRoleConfig{OperationTimeout: 3 * time.Second, ProofTTL: 30 * time.Second,
		ReverifyLead: 5 * time.Second, RetryDelay: time.Second, AllowTestAddresses: true,
		Logger: l.logger, ControlDialer: l.factory}
}

func (l *windowsRuntimeLab) role(t *testing.T, node *discovery.Node, config PublicRoleConfig) *publicRole {
	t.Helper()
	role, err := newPublicRole(l.ctx, node, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := role.Close(); err != nil {
			t.Error(err)
		}
	})
	return role
}

func runtimeLoopback(t *testing.T, node *discovery.Node) string {
	t.Helper()
	address, err := node.IPv4LoopbackAddress()
	if err != nil {
		t.Fatal(err)
	}
	return address
}

type windowsRuntimeEcho struct{}

func (windowsRuntimeEcho) HandleSignal(_ context.Context, _ peer.ID, value []byte) ([]byte, error) {
	return append([]byte(nil), value...), nil
}

func TestWindowsRuntimeQuorumAndPrivateDistributor(t *testing.T) {
	lab := newWindowsRuntimeLab(t)
	first, second := lab.node(t, discovery.Config{}), lab.node(t, discovery.Config{})
	lab.role(t, first, lab.config())
	lab.role(t, second, lab.config())
	seeds := []string{runtimeLoopback(t, first), runtimeLoopback(t, second)}
	subject := lab.node(t, discovery.Config{Bootstrap: seeds})
	config := lab.config()
	config.Requested, config.VerifierAddresses = true, seeds
	role := lab.role(t, subject, config)
	policy := rendezvous.DefaultConfig()
	policy.Admission.RegisterInterval = time.Millisecond
	m12, err := newM12Runtime(lab.ctx, subject, role, M12Config{Enabled: true, Serve: true, AllowTestAddresses: true, Policy: policy}, lab.logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m12.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if subject.LocalAnnouncement().Capabilities&protocol.CapRendezvous != 0 {
		t.Fatal("Rendezvous published before M11 quorum")
	}
	if err := role.Enable(lab.ctx, networkstate.State{Generation: 7, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !role.Status().IPv4.Ready(time.Now()) || role.Status().IPv4.Proof.Endpoint.NetworkGeneration != 7 {
		t.Fatal("native runtime failed M11 readiness")
	}
	waitRuntimeCondition(t, lab.ctx, func() bool {
		m12.mu.Lock()
		defer m12.mu.Unlock()
		return m12.service != nil && m12.privateService != nil
	})
	if subject.LocalAnnouncement().Capabilities&protocol.CapRendezvous == 0 {
		t.Fatal("Rendezvous did not become discoverable after quorum")
	}
	if lab.calls.Load() < 4 {
		t.Fatal("native TCP factory was not exercised")
	}
	contributor := lab.node(t, discovery.Config{Bootstrap: []string{runtimeLoopback(t, subject)}, Exit: true,
		DeferExitAdvertisement: true, AWGPublicKey: bytes.Repeat([]byte{7}, 32), AWGKeyEpoch: 1})
	if err := contributor.EnableSessionEstablishment(); err != nil {
		t.Fatal(err)
	}
	if err := contributor.SetPrivateExitReady(true); err != nil {
		t.Fatal(err)
	}
	policy.ObservedIPPolicy.AllowLoopback = true
	locator := [32]byte{41}
	registration, err := rendezvous.OpenPrivateRegistration(lab.ctx, rendezvous.RegistrationClientOptions{
		Host: contributor.Host(), Server: subject.PeerID(), Config: policy,
		SignedDescriptor: contributor.LocalSignedAnnouncement(), Handler: windowsRuntimeEcho{},
	}, locator)
	if err != nil {
		t.Fatal(err)
	}
	defer registration.Close(context.Background())
	client, err := rendezvous.NewClient(contributor.Host(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := client.LookupPrivate(lab.ctx, subject.PeerID(), contributor.PeerID(), locator)
	if err != nil || candidate.Descriptor.Subject != contributor.PeerID() {
		t.Fatal("private runtime lookup failed", err)
	}
	countries, err := client.ListLocalAvailableCountries(lab.ctx, subject.PeerID())
	if err != nil || len(countries) != 0 {
		t.Fatal("private registration contaminated global country index", countries, err)
	}
	if err := registration.Close(lab.ctx); err != nil {
		t.Fatal(err)
	}
	if err := role.Disable(lab.ctx); err != nil {
		t.Fatal(err)
	}
	waitRuntimeCondition(t, lab.ctx, func() bool {
		m12.mu.Lock()
		defer m12.mu.Unlock()
		return m12.service == nil && m12.privateService == nil &&
			subject.LocalAnnouncement().Capabilities&(protocol.CapRendezvous|protocol.CapPublicControlV4) == 0
	})
	if subject.LocalAnnouncement().Capabilities&(protocol.CapRendezvous|protocol.CapPublicControlV4) != 0 {
		t.Fatal("disabled public role stayed advertised")
	}
}

func waitRuntimeCondition(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal("runtime condition did not converge", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestWindowsRuntimeRequiresNativeControlPolicy(t *testing.T) {
	lab := newWindowsRuntimeLab(t)
	node := lab.node(t, discovery.Config{})
	config := lab.config()
	config.ControlDialer = nil
	role, err := newPublicRole(lab.ctx, node, config)
	if role != nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("mark-only/unbound public role accepted", err)
	}
}

func TestWindowsRuntimeRejectsFailedBoundRequest(t *testing.T) {
	lab := newWindowsRuntimeLab(t)
	first, second := lab.node(t, discovery.Config{}), lab.node(t, discovery.Config{})
	lab.role(t, first, lab.config())
	lab.role(t, second, lab.config())
	seeds := []string{runtimeLoopback(t, first), runtimeLoopback(t, second)}
	subject := lab.node(t, discovery.Config{Bootstrap: seeds})
	var denied atomic.Int32
	blocked, err := controlsocket.DialerForBindings(func(context.Context, netip.Addr) (winroute.Binding, error) {
		denied.Add(1)
		return winroute.Binding{}, errors.New("underlay generation unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	config := lab.config()
	config.Requested, config.VerifierAddresses, config.ControlDialer = true, seeds, blocked
	role := lab.role(t, subject, config)
	if err := role.Enable(lab.ctx, networkstate.State{Generation: 1, ObservedAt: time.Now()}); err == nil {
		t.Fatal("failed explicit socket policy bypassed")
	}
	if denied.Load() == 0 || role.Status().IPv4.State == reachability.StateVerified ||
		subject.LocalAnnouncement().Capabilities&protocol.CapPublicControlV4 != 0 {
		t.Fatal("failed fresh request fell back to an existing libp2p connection")
	}
}

func TestWindowsRuntimeRejectsFailedBoundChallenge(t *testing.T) {
	lab := newWindowsRuntimeLab(t)
	first, second := lab.node(t, discovery.Config{}), lab.node(t, discovery.Config{})
	var denied atomic.Int32
	blocked, err := controlsocket.DialerForBindings(func(context.Context, netip.Addr) (winroute.Binding, error) {
		denied.Add(1)
		return winroute.Binding{}, errors.New("verifier underlay unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	verifierConfig := lab.config()
	verifierConfig.ControlDialer = blocked
	lab.role(t, first, verifierConfig)
	lab.role(t, second, verifierConfig)
	seeds := []string{runtimeLoopback(t, first), runtimeLoopback(t, second)}
	subject := lab.node(t, discovery.Config{Bootstrap: seeds})
	config := lab.config()
	config.Requested, config.VerifierAddresses = true, seeds
	role := lab.role(t, subject, config)
	if err := role.Enable(lab.ctx, networkstate.State{Generation: 1, ObservedAt: time.Now()}); err == nil {
		t.Fatal("quorum passed without fresh verifier challenges")
	}
	if denied.Load() == 0 || role.Status().IPv4.State == reachability.StateVerified ||
		subject.LocalAnnouncement().Capabilities&protocol.CapPublicControlV4 != 0 {
		t.Fatal("failed challenge policy bypassed through existing peer connection")
	}
}

func TestWindowsRuntimeReplacesLostVerifier(t *testing.T) {
	lab := newWindowsRuntimeLab(t)
	first, second, replacement := lab.node(t, discovery.Config{}), lab.node(t, discovery.Config{}), lab.node(t, discovery.Config{})
	base := lab.config()
	base.ProofTTL, base.ReverifyLead, base.RetryDelay = 12*time.Second, 6*time.Second, 100*time.Millisecond
	firstRole := lab.role(t, first, base)
	lab.role(t, second, base)
	lab.role(t, replacement, base)
	address := func(node *discovery.Node, ip string) string {
		return strings.Replace(runtimeLoopback(t, node), "/ip4/127.0.0.1/", "/ip4/"+ip+"/", 1)
	}
	// Distinct loopback addresses simulate independent hosts; the TCP challenges are real.
	initial := []string{address(first, "127.0.0.2"), address(second, "127.0.0.3")}
	subject := lab.node(t, discovery.Config{Bootstrap: initial})
	config := base
	config.Requested, config.DiscoverVerifiers = true, true
	config.VerifierAddresses = initial
	config.VerifierBootstrap = []string{address(replacement, "127.0.0.4"), initial[1]}
	role := lab.role(t, subject, config)
	if err := role.Enable(lab.ctx, networkstate.State{Generation: 17, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !role.Status().IPv4.Ready(time.Now()) {
		t.Fatal("initial native quorum unavailable")
	}
	if err := firstRole.Close(); err != nil {
		t.Fatal(err)
	}
	waitRuntimeCondition(t, lab.ctx, func() bool {
		snapshot := role.Status().IPv4
		if !snapshot.Ready(time.Now()) {
			return false
		}
		seen := make(map[peer.ID]bool)
		for _, signed := range snapshot.Proof.Attestations {
			value, err := reachability.VerifyAttestation(signed, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			seen[peer.ID(value.VerifierPeerId)] = true
		}
		if !seen[replacement.PeerID()] {
			return false
		}
		if !seen[second.PeerID()] || seen[first.PeerID()] || len(seen) < reachability.DefaultQuorum || snapshot.Proof.Endpoint.NetworkGeneration != 17 {
			t.Fatal("replacement weakened quorum or changed generation", snapshot)
		}
		return true
	})
	if subject.LocalAnnouncement().Capabilities&protocol.CapPublicControlV4 == 0 {
		t.Fatal("new valid quorum not advertised")
	}
	t.Log("automatic verifier replacement: fresh native TCP challenges, two signed identities, unchanged generation")
}
