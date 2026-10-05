// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	defaultProbeConcurrency   = 5
	defaultProbeTimeout       = 750 * time.Millisecond
	defaultLatencyTolerance   = 5 * time.Millisecond
	defaultRecoveryBudget     = 2 * time.Second
	defaultFastConnectTimeout = 1500 * time.Millisecond
	defaultRefreshMinimum     = 45 * time.Second
	defaultRefreshMaximum     = 75 * time.Second
	defaultProviderRetry      = 5 * time.Second
	defaultLocalRetry         = 30 * time.Second
	defaultAvoidDuration      = 60 * time.Second
	defaultEventCapacity      = 128
	defaultSampleCapacity     = 256
	defaultSuppressionLimit   = 256
)

type Config struct {
	LocalPeer            peer.ID
	Country              CountryIntent
	Provider             CandidateProvider
	Probe                CandidateProbe
	Connector            Connector
	Protected            ProtectedModeCheck
	InitialUnderlay      UnderlayState
	Clock                Clock
	Random               Random
	MinimumBandwidthMbps uint32
	ProbeConcurrency     int
	ProbeTimeout         time.Duration
	LatencyTolerance     time.Duration
	ActiveRecoveryBudget time.Duration
	FastConnectTimeout   time.Duration
	RefreshMinimum       time.Duration
	RefreshMaximum       time.Duration
	ProviderRetry        time.Duration
	LocalRetry           time.Duration
	AvoidDuration        time.Duration
	EventCapacity        int
	SampleCapacity       int
	SuppressionLimit     int
}

func DefaultConfig(local peer.ID, country CountryIntent, provider CandidateProvider, probe CandidateProbe, connector Connector, protected ProtectedModeCheck, underlay UnderlayState) Config {
	return Config{
		LocalPeer: local, Country: country, Provider: provider, Probe: probe,
		Connector: connector, Protected: protected, InitialUnderlay: underlay,
		Clock: realClock{}, Random: NewCryptoRandom(), ProbeConcurrency: defaultProbeConcurrency,
		ProbeTimeout: defaultProbeTimeout, LatencyTolerance: defaultLatencyTolerance,
		ActiveRecoveryBudget: defaultRecoveryBudget, FastConnectTimeout: defaultFastConnectTimeout,
		RefreshMinimum: defaultRefreshMinimum, RefreshMaximum: defaultRefreshMaximum,
		ProviderRetry: defaultProviderRetry, LocalRetry: defaultLocalRetry, AvoidDuration: defaultAvoidDuration,
		EventCapacity: defaultEventCapacity, SampleCapacity: defaultSampleCapacity,
		SuppressionLimit: defaultSuppressionLimit,
	}
}

func (c *Config) validate() error {
	if c == nil || c.LocalPeer == "" || c.LocalPeer.Validate() != nil || c.Provider == nil || c.Probe == nil || c.Connector == nil || c.Protected == nil || c.Clock == nil || c.Random == nil {
		return ErrInvalidConfig
	}
	normalized, err := normalizeCountry(c.Country)
	if err != nil {
		return err
	}
	c.Country = normalized
	if c.LocalRetry == 0 {
		c.LocalRetry = defaultLocalRetry
	}
	if c.InitialUnderlay.NetworkGeneration == 0 {
		return fmt.Errorf("%w: initial NetworkGeneration is zero", ErrInvalidConfig)
	}
	if c.ProbeConcurrency < 1 || c.ProbeConcurrency > 5 || c.ProbeTimeout <= 0 ||
		c.LatencyTolerance <= 0 || c.ActiveRecoveryBudget <= 0 || c.ActiveRecoveryBudget > 2*time.Second ||
		c.FastConnectTimeout <= 0 || c.FastConnectTimeout >= c.ActiveRecoveryBudget ||
		c.RefreshMinimum < time.Second || c.RefreshMaximum < c.RefreshMinimum ||
		c.ProviderRetry <= 0 || c.LocalRetry <= 0 || c.LocalRetry > 10*time.Minute || c.AvoidDuration <= 0 || c.EventCapacity < 1 || c.EventCapacity > 65536 ||
		c.SampleCapacity < 1 || c.SampleCapacity > 4096 || c.SuppressionLimit < 1 || c.SuppressionLimit > 4096 {
		return ErrInvalidConfig
	}
	return nil
}
