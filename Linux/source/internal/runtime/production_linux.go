// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"fmt"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/underlay"
	"io"
)

func productionDataPlanePlatform(config ProductionConfig) ClientDataPlanePlatform {
	return config.Platform
}

func wrapProductionTunnelDNS(installer SessionInstaller, network NetworkReconciler, config ProductionConfig) (SessionInstaller, NetworkReconciler, io.Closer, error) {
	if len(config.TunnelDNSServers) == 0 {
		return installer, network, nil, nil
	}
	tunName := config.TUNName
	if tunName == "" {
		tunName = tunnel.DefaultName
	}
	manager, err := newTunnelDNSManager(tunName, config.TunnelDNSServers, nil, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	wrapper, err := newTunnelDNSInstaller(installer, network, manager)
	if err != nil {
		_ = manager.Close()
		return nil, nil, nil, err
	}
	return wrapper, wrapper, manager, nil
}

func (d *Daemon) startClientRuntime(
	ctx context.Context, node *discovery.Node, monitor *networkstate.Monitor,
	protection *killswitch.Controller, connector exitselector.Connector,
	transport TransportLifecycle, core io.Closer,
	network NetworkReconciler,
) error {
	if node.ControlMark() != linuxroute.IrvioBypassMark {
		return fmt.Errorf("underlay host is not Irvio-marked: %w", ErrInvalidConfig)
	}
	rawProbe, err := underlay.NewMarkedChallengeProbe(linuxroute.IrvioBypassMark)
	if err != nil {
		return err
	}
	var candidateProbe exitselector.CandidateProbe
	if d.m12 == nil {
		candidateProbe, err = exitselector.NewMarkedTCPProbe(linuxroute.IrvioBypassMark)
		if err != nil {
			return err
		}
	}
	coordinator, oracle, err := newProductionCoordinator(productionCoordinatorConfig{
		Node: node, Monitor: monitor, Protection: protection, Connector: connector,
		Transport: transport, Core: core, Network: network, Targets: d.config.UnderlayTargets,
		RawProbe: rawProbe, CandidateProbe: candidateProbe, M12: d.m12, Public: d.publicRole,
		Production: d.config.Production, Logger: d.config.Logger, OperationTimeout: d.config.OperationTimeout,
	})
	d.oracle = oracle
	if err != nil {
		return err
	}
	d.runtime = coordinator
	return coordinator.Start(ctx)
}
