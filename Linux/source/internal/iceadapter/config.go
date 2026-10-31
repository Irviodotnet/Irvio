// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

const (
	defaultMaxUfrags       = 32
	defaultMaxTransactions = 256
	defaultMaxRetired      = 512
	defaultQueueCapacity   = 64
	defaultTransactionTTL  = 30 * time.Second
	defaultRetiredTTL      = 5 * time.Second
	maxListenAddressInputs = 16
	maximumRegistrySize    = 65536
	maximumQueueCapacity   = 65536
	maxUfragLength         = 256
	minUfragLength         = 4
)

// RegistryConfig bounds ICE routing state.
type RegistryConfig struct {
	MaxUfrags       int
	MaxTransactions int
	MaxRetired      int
	TransactionTTL  time.Duration
	RetiredTTL      time.Duration
}

// DefaultRegistryConfig returns conservative routing limits.
func DefaultRegistryConfig() RegistryConfig {
	return RegistryConfig{
		MaxUfrags:       defaultMaxUfrags,
		MaxTransactions: defaultMaxTransactions,
		MaxRetired:      defaultMaxRetired,
		TransactionTTL:  defaultTransactionTTL,
		RetiredTTL:      defaultRetiredTTL,
	}
}

func (c RegistryConfig) validate() error {
	if c.MaxUfrags <= 0 || c.MaxTransactions <= 0 || c.MaxRetired <= 0 {
		return fmt.Errorf("registry limits must be positive")
	}
	if c.MaxUfrags > maximumRegistrySize || c.MaxTransactions > maximumRegistrySize || c.MaxRetired > maximumRegistrySize {
		return fmt.Errorf("registry limits must not exceed %d", maximumRegistrySize)
	}
	if c.TransactionTTL <= 0 || c.RetiredTTL <= 0 {
		return fmt.Errorf("registry lifetimes must be positive")
	}
	return nil
}

// Config permits one concrete listen address per family because M5a lacks destination metadata.
type Config struct {
	ListenAddresses         []netip.Addr
	ConnectionQueueCapacity int
	STUNResponseHandler     func(socketmux.Datagram) bool
	Logger                  *slog.Logger
}

// DefaultConfig returns bounded defaults for the supplied explicit addresses.
func DefaultConfig(addresses ...netip.Addr) Config {
	return Config{
		ListenAddresses:         append([]netip.Addr(nil), addresses...),
		ConnectionQueueCapacity: defaultQueueCapacity,
	}
}

func (c Config) validate() error {
	if c.ConnectionQueueCapacity <= 0 || c.ConnectionQueueCapacity > maximumQueueCapacity {
		return fmt.Errorf("logical connection queue capacity must be within 1..%d", maximumQueueCapacity)
	}
	if len(c.ListenAddresses) == 0 || len(c.ListenAddresses) > maxListenAddressInputs {
		return fmt.Errorf("listen address input count must be within 1..%d", maxListenAddressInputs)
	}
	return nil
}
