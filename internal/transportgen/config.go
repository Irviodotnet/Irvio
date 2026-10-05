// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package transportgen

import (
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"hydra.local/hydra/internal/iceadapter"
	"hydra.local/hydra/internal/icesession"
	"hydra.local/hydra/internal/socketmux"
	"hydra.local/hydra/internal/stunprobe"
)

// Config controls one atomic transport-generation build.
type Config struct {
	Planner BindPlanner
	Port    int
	// SocketMark applies before STUN/ICE emission; zero leaves sockets unmarked.
	SocketMark  uint32
	STUNServers map[socketmux.AddressFamily]netip.AddrPort
	// STUNServerPools replaces legacy single endpoints per family and retains all seeds for refresh.
	STUNServerPools         map[socketmux.AddressFamily][]netip.AddrPort
	STUN                    stunprobe.Config
	STUNRegistry            stunprobe.RegistryConfig
	ICERegistry             iceadapter.RegistryConfig
	MaxSessions             int
	ConnectionQueueCapacity int
	GatherTimeout           time.Duration
	Timing                  icesession.AgentTiming
	Validation              icesession.ValidationConfig
	ControlQueueCapacity    int
	DataQueueCapacity       int
	Logger                  *slog.Logger
}

// DefaultConfig returns conservative transport, STUN, and ICE limits.
func DefaultConfig(planner BindPlanner) Config {
	mux := socketmux.DefaultConfig()
	engineMax := iceadapter.DefaultRegistryConfig().MaxUfrags
	return Config{
		Planner: planner, STUN: stunprobe.DefaultConfig(), STUNRegistry: stunprobe.DefaultRegistryConfig(),
		ICERegistry: iceadapter.DefaultRegistryConfig(), MaxSessions: engineMax,
		ConnectionQueueCapacity: 64, GatherTimeout: 5 * time.Second,
		Validation:           icesession.DefaultValidationConfig(),
		ControlQueueCapacity: mux.ControlQueueCapacity, DataQueueCapacity: mux.DataQueueCapacity,
	}
}

func (c Config) validate() error {
	if c.Planner == nil {
		return fmt.Errorf("transport generation planner is nil")
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("invalid preferred port %d", c.Port)
	}
	if c.MaxSessions <= 0 || c.MaxSessions > c.ICERegistry.MaxUfrags {
		return fmt.Errorf("max sessions must fit the ICE ufrag registry")
	}
	if c.ConnectionQueueCapacity <= 0 || c.ControlQueueCapacity <= 0 || c.DataQueueCapacity <= 0 || c.GatherTimeout <= 0 {
		return fmt.Errorf("transport queue capacities and gather timeout must be positive")
	}
	for family, servers := range c.STUNServerPools {
		if len(servers) > c.STUNRegistry.MaxInFlight {
			return fmt.Errorf("STUN %s pool of %d exceeds transaction registry capacity %d", family, len(servers), c.STUNRegistry.MaxInFlight)
		}
	}
	if err := c.Validation.Validate(); err != nil {
		return err
	}
	return nil
}
