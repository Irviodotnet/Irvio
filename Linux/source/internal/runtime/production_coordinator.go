// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	"github.com/Irviodotnet/Irvio/internal/underlay"
)

type productionCoordinatorConfig struct {
	Node             *discovery.Node
	Monitor          *networkstate.Monitor
	Protection       Protection
	Connector        exitselector.Connector
	Transport        TransportLifecycle
	Core             io.Closer
	Network          NetworkReconciler
	Targets          underlay.TargetProvider
	RawProbe         underlay.Prober
	CandidateProbe   exitselector.CandidateProbe
	M12              *m12Runtime
	Public           *publicRole
	Production       *ProductionConfig
	Logger           *slog.Logger
	OperationTimeout time.Duration
}

// Both platforms share selection and recovery; callers supply constrained OS socket policies.
func newProductionCoordinator(c productionCoordinatorConfig) (*Runtime, *underlay.Oracle, error) {
	if c.Node == nil || c.Monitor == nil || c.Protection == nil || c.Connector == nil || c.RawProbe == nil || c.M12 == nil && c.CandidateProbe == nil {
		return nil, nil, ErrInvalidConfig
	}
	if c.Production != nil && c.Production.PrivateTarget != "" && c.M12 == nil {
		return nil, nil, ErrInvalidConfig
	}
	targets := combinedUnderlayTargets{
		configured: c.Targets,
		discovered: &DiscoveryUnderlayTargets{Node: c.Node, Now: time.Now},
	}
	peerProbe, err := underlay.NewLibp2pChallengeProbe(c.Node.Host())
	if err != nil {
		return nil, nil, err
	}
	probe := productionUnderlayProbe{raw: c.RawProbe, libp2p: peerProbe}
	oracleConfig := underlay.DefaultConfig(targets, probe)
	oracleConfig.FailureThreshold = 1
	oracle, err := underlay.New(oracleConfig)
	if err != nil {
		return nil, nil, err
	}
	var provider exitselector.CandidateProvider = &DiscoveryCandidateProvider{Node: c.Node, Now: time.Now}
	candidateProbe := c.CandidateProbe
	if c.M12 != nil {
		provider = c.M12.CandidateProvider()
		candidateProbe = c.M12.Probe()
		if c.Production != nil && c.Production.PrivateTarget != "" {
			private, err := c.M12.privateCandidates(c.Production.PrivateTarget, c.Production.PrivateLocator)
			if err != nil {
				return nil, oracle, err
			}
			provider = private
			candidateProbe = rendezvous.MetadataProbe{Provider: private, Now: time.Now, Logger: c.Logger}
		}
	}
	newSelector := func(target exitselector.Target, initial exitselector.UnderlayState) (Selector, error) {
		config := exitselector.DefaultConfig(
			c.Node.PeerID(), target.Country, provider, candidateProbe, c.Connector,
			func() bool { return c.Protection.Snapshot().State.Protected() }, initial,
		)
		config.Target = target
		return exitselector.New(config)
	}
	coordinator, err := New(Config{
		Core: c.Core, Network: c.Monitor, Underlay: oracle, Transport: c.Transport,
		Protection: c.Protection, NewTargetSelector: newSelector, Now: time.Now,
		OperationTimeout: c.OperationTimeout, EventCapacity: 128,
		OnNetworkChange: func(ctx context.Context, state networkstate.State) error {
			var publicEpoch uint64
			var publicRequested bool
			if c.Public != nil {
				var err error
				publicEpoch, publicRequested, err = c.Public.invalidateNetwork(state)
				if err != nil {
					return fmt.Errorf("invalidate public reachability: %w", err)
				}
			}
			if c.Network != nil {
				if err := c.Network.ReconcileNetwork(ctx, state); err != nil {
					return fmt.Errorf("reconcile Irvio underlay routes: %w", err)
				}
			}
			if publicRequested {
				c.Public.reverifyNetwork(state, publicEpoch)
			}
			return nil
		},
	})
	return coordinator, oracle, err
}
