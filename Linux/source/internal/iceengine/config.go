// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceengine

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

const (
	defaultMaxSessions        = 32
	defaultConnectionQueueCap = 64
	maximumSessions           = 65536
)

// Config bounds shared adapter and per-Agent state.
type Config struct {
	Session                 icesession.SessionConfig
	TransportGeneration     uint64
	MaxSessions             int
	ConnectionQueueCapacity int
	RefreshSrflx            func(context.Context) ([]icesession.SrflxObservation, error)
	STUNResponseHandler     func(socketmux.Datagram) bool
	Logger                  *slog.Logger
}

// DefaultConfig returns conservative shared-engine limits.
func DefaultConfig(addresses ...netip.Addr) Config {
	return Config{
		Session: icesession.DefaultSessionConfig(addresses...), TransportGeneration: 1, MaxSessions: defaultMaxSessions,
		ConnectionQueueCapacity: defaultConnectionQueueCap,
	}
}

func (c Config) validate() error {
	if err := c.Session.Validate(); err != nil {
		return fmt.Errorf("session config: %w", err)
	}
	if c.TransportGeneration == 0 {
		return fmt.Errorf("transport generation must be nonzero")
	}
	if c.MaxSessions <= 0 || c.MaxSessions > maximumSessions {
		return fmt.Errorf("max sessions must be within 1..%d", maximumSessions)
	}
	if c.ConnectionQueueCapacity <= 0 || c.ConnectionQueueCapacity > 65536 {
		return fmt.Errorf("connection queue capacity must be within 1..65536")
	}
	return nil
}
