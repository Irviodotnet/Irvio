// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"fmt"
	"log/slog"
	"net/netip"
)

const (
	// MaxUDPPayloadSize bounds payloads to the ordinary IPv4 UDP maximum.
	MaxUDPPayloadSize = 65507
	defaultQueueSize  = 64
	portBindAttempts  = 8
)

// FamilyPolicy controls whether an address family is disabled, optional, or required.
type FamilyPolicy uint8

const (
	FamilyDisabled FamilyPolicy = iota
	FamilyOptional
	FamilyRequired
)

// Config defines socket binding, queue bounds, and packet classification.
type Config struct {
	IPv4Policy FamilyPolicy
	IPv6Policy FamilyPolicy
	IPv4Bind   netip.Addr
	IPv6Bind   netip.Addr
	Port       int

	ControlQueueCapacity int
	DataQueueCapacity    int
	MaxDatagramSize      int
	Classifier           Classifier
	Logger               *slog.Logger
}

// DefaultConfig requires IPv4 and permits graceful IPv6 unavailability.
func DefaultConfig() Config {
	return Config{
		IPv4Policy:           FamilyRequired,
		IPv6Policy:           FamilyOptional,
		IPv4Bind:             netip.IPv4Unspecified(),
		IPv6Bind:             netip.IPv6Unspecified(),
		ControlQueueCapacity: defaultQueueSize,
		DataQueueCapacity:    defaultQueueSize,
		MaxDatagramSize:      MaxUDPPayloadSize,
	}
}

func (c Config) validate() error {
	if !validPolicy(c.IPv4Policy) || !validPolicy(c.IPv6Policy) {
		return fmt.Errorf("invalid address family policy")
	}
	if c.IPv4Policy == FamilyDisabled && c.IPv6Policy == FamilyDisabled {
		return fmt.Errorf("at least one address family must be enabled")
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("UDP port %d is invalid", c.Port)
	}
	if c.ControlQueueCapacity <= 0 {
		return fmt.Errorf("control queue capacity must be positive")
	}
	if c.DataQueueCapacity <= 0 {
		return fmt.Errorf("data queue capacity must be positive")
	}
	if c.MaxDatagramSize <= 0 || c.MaxDatagramSize > MaxUDPPayloadSize {
		return fmt.Errorf("maximum datagram size must be within 1..%d", MaxUDPPayloadSize)
	}
	if c.IPv4Policy != FamilyDisabled {
		if !c.IPv4Bind.IsValid() || !c.IPv4Bind.Is4() || c.IPv4Bind.Is4In6() {
			return fmt.Errorf("IPv4 bind address must be a native IPv4 address")
		}
	}
	if c.IPv6Policy != FamilyDisabled {
		if !c.IPv6Bind.IsValid() || !c.IPv6Bind.Is6() || c.IPv6Bind.Is4In6() {
			return fmt.Errorf("IPv6 bind address must be a native IPv6 address")
		}
	}
	return nil
}

func validPolicy(policy FamilyPolicy) bool {
	return policy == FamilyDisabled || policy == FamilyOptional || policy == FamilyRequired
}
