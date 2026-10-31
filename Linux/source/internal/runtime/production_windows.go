// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"io"
	"slices"

	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/underlay"
)

func (d *Daemon) startClientRuntime(ctx context.Context, node *discovery.Node, monitor *networkstate.Monitor,
	protection *killswitch.Controller, connector exitselector.Connector, transport TransportLifecycle, core io.Closer, network NetworkReconciler) error {
	raw, err := underlay.NewTCPChallengeProbe(d.platform.dialer)
	if err != nil {
		return err
	}
	var candidate exitselector.CandidateProbe
	if d.m12 == nil {
		candidate, err = exitselector.NewTCPProbe(d.platform.dialer)
		if err != nil {
			return err
		}
	}
	r, oracle, err := newProductionCoordinator(productionCoordinatorConfig{
		Node: node, Monitor: monitor, Protection: windowsDaemonProtection{Controller: protection, daemon: d},
		Connector: connector, Transport: transport, Core: core, Network: network, Targets: d.config.UnderlayTargets,
		RawProbe: raw, CandidateProbe: candidate, M12: d.m12, Public: d.publicRole, Production: d.config.Production,
		Logger: d.config.Logger, OperationTimeout: d.config.OperationTimeout,
	})
	d.oracle = oracle
	if err != nil {
		return err
	}
	d.runtime = r
	return r.Start(ctx)
}

func productionDataPlanePlatform(config ProductionConfig) ClientDataPlanePlatform {
	p := config.Platform
	p.DNSServers = slices.Clone(config.TunnelDNSServers)
	return p
}

// Windows installers own DNS inside the protected BIND lifecycle, including shared Exit adapters.
func wrapProductionTunnelDNS(installer SessionInstaller, network NetworkReconciler, config ProductionConfig) (SessionInstaller, NetworkReconciler, io.Closer, error) {
	c := ClientDataPlaneConfig{Platform: productionDataPlanePlatform(config), Bypass: config.Bypass}
	if installer == nil || network == nil {
		return nil, nil, nil, ErrInvalidConfig
	}
	if err := normalizeClientPlatform(&c); err != nil {
		return nil, nil, nil, err
	}
	return installer, network, nil, nil
}
