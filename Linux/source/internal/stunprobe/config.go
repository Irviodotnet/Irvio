// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"fmt"
	"time"
)

const (
	defaultMaxInFlight  = 64
	defaultMaxRetired   = 256
	maximumRegistrySize = 65536
)

// RegistryConfig bounds active and recently retired STUN transactions.
type RegistryConfig struct {
	MaxInFlight  int
	MaxRetired   int
	RetiredGrace time.Duration
}

// DefaultRegistryConfig returns conservative registry bounds.
func DefaultRegistryConfig() RegistryConfig {
	return RegistryConfig{
		MaxInFlight:  defaultMaxInFlight,
		MaxRetired:   defaultMaxRetired,
		RetiredGrace: 5 * time.Second,
	}
}

func (c RegistryConfig) validate() error {
	if c.MaxInFlight <= 0 || c.MaxInFlight > maximumRegistrySize {
		return fmt.Errorf("maximum in-flight transactions must be within 1..%d", maximumRegistrySize)
	}
	if c.MaxRetired <= 0 || c.MaxRetired > maximumRegistrySize {
		return fmt.Errorf("maximum retired transactions must be within 1..%d", maximumRegistrySize)
	}
	if c.RetiredGrace <= 0 {
		return fmt.Errorf("retired transaction grace must be positive")
	}
	return nil
}

// Config controls Binding request retransmission.
type Config struct {
	InitialRTO  time.Duration
	MaxRTO      time.Duration
	MaxAttempts int
}

// DefaultConfig returns a bounded Basic Binding retry policy.
func DefaultConfig() Config {
	return Config{
		InitialRTO:  500 * time.Millisecond,
		MaxRTO:      2 * time.Second,
		MaxAttempts: 4,
	}
}

func (c Config) validate() error {
	if c.InitialRTO <= 0 {
		return fmt.Errorf("initial RTO must be positive")
	}
	if c.MaxRTO < c.InitialRTO {
		return fmt.Errorf("maximum RTO must be at least the initial RTO")
	}
	if c.MaxAttempts <= 0 || c.MaxAttempts > 32 {
		return fmt.Errorf("maximum attempts must be within 1..32")
	}
	return nil
}
