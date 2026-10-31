// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	"github.com/Irviodotnet/Irvio/internal/underlay"
)

type productionWindowsInstaller struct{}

func (*productionWindowsInstaller) Install(context.Context, InstallRequest) (io.Closer, error) {
	return nil, errors.New("unexpected install")
}
func (*productionWindowsInstaller) ReconcileNetwork(context.Context, networkstate.State) error {
	return nil
}

func TestWindowsProductionDNSUsesOwnedInstaller(t *testing.T) {
	i := &productionWindowsInstaller{}
	c := ProductionConfig{TunnelDNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		Platform: ClientDataPlanePlatform{Protection: &killswitch.WindowsNetworkUpdater{}, JournalDirectory: t.TempDir()}}
	got, reconcile, dns, err := wrapProductionTunnelDNS(i, i, c)
	if err != nil || got != i || reconcile != i || dns != nil {
		t.Fatal("Windows introduced a second DNS owner", err)
	}
	p := productionDataPlanePlatform(c)
	if !slices.Equal(p.DNSServers, c.TunnelDNSServers) || p.Protection != c.Platform.Protection || p.JournalDirectory != c.Platform.JournalDirectory {
		t.Fatal("production platform lost native dependencies")
	}
	p.DNSServers[0] = netip.MustParseAddr("8.8.8.8")
	if c.TunnelDNSServers[0] != netip.MustParseAddr("1.1.1.1") {
		t.Fatal("DNS config shares mutable slice")
	}
	for _, mutate := range []func(*ProductionConfig){
		func(c *ProductionConfig) { c.Platform.Protection = nil },
		func(c *ProductionConfig) { c.Platform.JournalDirectory = "relative" },
		func(c *ProductionConfig) { c.TunnelDNSServers = nil },
		func(c *ProductionConfig) { c.Bypass = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")} },
	} {
		bad := c
		mutate(&bad)
		if _, _, _, err := wrapProductionTunnelDNS(i, i, bad); err == nil {
			t.Fatal("incomplete native configuration accepted")
		}
	}
}

func TestWindowsClientNetworkChangeRevokesAndFences(t *testing.T) {
	x := &windowsRouteTrace{fail: "revoke"}
	p := &ClientDataPlane{current: &installedClientDataPlane{generation: 1}}
	state := networkstate.State{Generation: 2}
	if err := p.reconcileWindowsNetwork(context.Background(), state, x); !errors.Is(err, errWindowsRouteInjected) || p.networkFloor != 2 {
		t.Fatal("failed revoke lost generation fence", err)
	}
	x.fail = ""
	if err := p.reconcileWindowsNetwork(context.Background(), state, x); err != nil || len(x.events) != 2 {
		t.Fatal("same generation did not retry failed revoke", err)
	}
	if err := p.reconcileWindowsNetwork(context.Background(), networkstate.State{Generation: 1}, x); !errors.Is(err, ErrGenerationStale) {
		t.Fatal("old event accepted", err)
	}
	p.current.generation = 2
	if err := p.reconcileWindowsNetwork(context.Background(), state, x); err != nil || len(x.events) != 2 {
		t.Fatal("current connection revoked by duplicate event", err)
	}
	p.closed = true
	if err := p.reconcileWindowsNetwork(context.Background(), state, x); !errors.Is(err, ErrClosed) {
		t.Fatal("closed plane reconciled", err)
	}
}

func TestWindowsClientRejectsPromotionBeforeTransportMigration(t *testing.T) {
	g := newRuntimeGeneration(t)
	p := &ClientDataPlane{networkFloor: g.ID() + 1, config: ClientDataPlaneConfig{Transports: &exitPlaneTransport{current: g}}}
	request := InstallRequest{Transport: g}
	request.SelectedEndpoint.NetworkGeneration = g.ID()
	s := &stagedClientDataPlane{owner: p, request: request}
	if err := s.Promote(context.Background()); !errors.Is(err, ErrGenerationStale) {
		t.Fatal("staged predecessor could promote after network change", err)
	}
}

func TestWindowsProductionBrokeredClientComposition(t *testing.T) {
	lab := newWindowsRuntimeLab(t)
	monitor, err := networkstate.NewMonitor(networkstate.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	raw, err := underlay.NewTCPChallengeProbe(lab.factory)
	if err != nil {
		t.Fatal(err)
	}
	private, public, _ := nativeDPIdentity(t)
	node := lab.node(t, discovery.Config{AWGPublicKey: public[:], AWGKeyEpoch: 1})
	role := lab.role(t, node, lab.config())
	m12, err := newM12Runtime(lab.ctx, node, role, M12Config{Enabled: true, Policy: rendezvous.DefaultConfig()}, lab.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer m12.Close(context.Background())
	c := ProductionConfig{LeaseTTL: time.Minute, TunnelDNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		Platform: ClientDataPlanePlatform{Protection: &killswitch.WindowsNetworkUpdater{}, JournalDirectory: t.TempDir()}}
	owner, err := newTransportOwner(c, lab.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	bindings := productionBindingResolver(node, awgbinding.NewRegistry())
	for _, mode := range []string{"owned-global", "shared-global", "owned-private"} {
		t.Run(mode, func(t *testing.T) {
			var shared SessionInstaller
			if mode == "shared-global" {
				shared = &productionWindowsInstaller{}
			}
			config := c
			if mode == "owned-private" {
				config.PrivateTarget = newRuntimeHost(t).ID()
				config.PrivateLocator[0] = 1
			}
			client, err := newProductionBrokeredClient(node, node.IdentityPrivateKey(), private, 1, owner, bindings, config, time.Second, shared, m12)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := client.core().Close(); err != nil {
					t.Error(err)
				}
			}()
			connector, ok := client.connector.(*BrokeredClientConnector)
			if !ok || connector.config.PrivateTarget != config.PrivateTarget || client.dns != nil {
				t.Fatal("production target or native DNS ownership lost")
			}
			if connector.config.Logger != lab.logger {
				t.Fatal("production connector diagnostics bypass the daemon logger")
			}
			if shared != nil {
				if client.plane != nil || connector.config.Installer != shared || client.network != shared.(NetworkReconciler) {
					t.Fatal("shared Exit installer replaced")
				}
			} else {
				plane, ok := client.plane.(*ClientDataPlane)
				if !ok || connector.config.Installer != plane || client.network != plane || plane.config.Platform.Protection != c.Platform.Protection || !slices.Equal(plane.config.Platform.DNSServers, c.TunnelDNSServers) {
					t.Fatal("native production platform not propagated")
				}
			}
			coordinatorConfig := productionCoordinatorConfig{
				Node: node, Monitor: monitor, Protection: &killswitch.Controller{},
				Connector: client.connector, Transport: owner, Core: client.core(), Network: client.network,
				RawProbe: raw, M12: m12, Public: role, Production: &config, Logger: lab.logger, OperationTimeout: time.Second,
			}
			r, oracle, err := newProductionCoordinator(coordinatorConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer r.cancel()
			defer oracle.Close()
			if r.config.Transport != owner || r.config.Protection != coordinatorConfig.Protection || r.config.Core != coordinatorConfig.Core || r.config.Network != monitor {
				t.Fatal("coordinator replaced production resource owners")
			}
			target := exitselector.Target{Kind: exitselector.TargetPublicCountry, Country: "NL"}
			if mode == "owned-private" {
				target = exitselector.Target{Kind: exitselector.TargetPrivatePeer, PeerID: config.PrivateTarget}
			}
			s, err := r.config.NewTargetSelector(target, exitselector.UnderlayState{NetworkGeneration: monitor.Current().Generation})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.Current().Target != target {
				t.Fatal("coordinator lost exact private/public target", s.Current().Target)
			}
			coordinatorConfig.RawProbe = nil
			if _, _, err := newProductionCoordinator(coordinatorConfig); !errors.Is(err, ErrInvalidConfig) {
				t.Fatal("missing OS probe policy accepted", err)
			}
		})
	}
}
