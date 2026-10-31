// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package connsupervisor

import (
	"fmt"
	"time"

	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

const (
	defaultEventCapacity     = 64
	defaultMaxInflight       = 2
	defaultDisconnectedGrace = 30 * time.Second
	defaultRetryInitial      = 500 * time.Millisecond
	defaultRetryMaximum      = 30 * time.Second
)

// Config bounds one logical connection supervisor.
type Config struct {
	Intent                    Intent
	InitialNetworkGeneration  networkstate.Generation
	Connector                 Connector
	Limiter                   *Limiter
	Clock                     Clock
	EventCapacity             int
	MaxInFlight               int
	DisconnectedGrace         time.Duration
	RetryInitial              time.Duration
	RetryMaximum              time.Duration
	MaxSameGenerationFailures int
}

// DefaultConfig returns conservative reconnect timing and bounds.
func DefaultConfig(intent Intent, generation networkstate.Generation, connector Connector) Config {
	limiter, _ := NewLimiter(defaultMaxInflight)
	return Config{
		Intent: intent, InitialNetworkGeneration: generation, Connector: connector,
		Limiter: limiter, Clock: realClock{}, EventCapacity: defaultEventCapacity,
		MaxInFlight: defaultMaxInflight, DisconnectedGrace: defaultDisconnectedGrace,
		RetryInitial: defaultRetryInitial, RetryMaximum: defaultRetryMaximum,
		MaxSameGenerationFailures: 3,
	}
}

func (c Config) validate() error {
	if c.Intent.LocalPeer == "" || c.Intent.RemotePeer == "" || c.Intent.LocalPeer == c.Intent.RemotePeer ||
		c.Intent.LocalPeer.Validate() != nil || c.Intent.RemotePeer.Validate() != nil {
		return fmt.Errorf("invalid supervisor peer intent")
	}
	if c.Intent.Purpose != icesession.PurposeExit ||
		(c.Intent.Role != icesession.RoleControlling && c.Intent.Role != icesession.RoleControlled) {
		return fmt.Errorf("invalid supervisor purpose or role")
	}
	if c.InitialNetworkGeneration == 0 || c.Connector == nil || c.Limiter == nil || c.Clock == nil {
		return fmt.Errorf("invalid supervisor dependencies")
	}
	if c.EventCapacity <= 0 || c.EventCapacity > 65536 || c.MaxInFlight <= 0 || c.MaxInFlight > 64 {
		return fmt.Errorf("invalid supervisor bounds")
	}
	if c.DisconnectedGrace < 0 || c.RetryInitial <= 0 || c.RetryMaximum < c.RetryInitial {
		return fmt.Errorf("invalid supervisor timing")
	}
	if c.MaxSameGenerationFailures <= 0 || c.MaxSameGenerationFailures > 1024 {
		return fmt.Errorf("invalid same-generation failure bound")
	}
	return nil
}
