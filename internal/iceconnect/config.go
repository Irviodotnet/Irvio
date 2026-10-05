// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import (
	"fmt"
	"time"

	"hydra.local/hydra/internal/icesession"
)

const (
	defaultCandidateLoadTimeout = time.Second
	defaultCandidatePoll        = time.Millisecond
	defaultMaxTransitions       = 16
	defaultMaxRemoteSnapshots   = 32
	maximumTransitions          = 64
	maximumRemoteSnapshots      = 64
)

// Config bounds candidate loading and callback diagnostics.
type Config struct {
	Validation            icesession.ValidationConfig
	CandidateLoadTimeout  time.Duration
	CandidatePollInterval time.Duration
	MaxStateTransitions   int
	MaxRemoteSnapshots    int
}

// DefaultConfig returns conservative one-shot connectivity defaults.
func DefaultConfig() Config {
	return Config{
		Validation:            icesession.DefaultValidationConfig(),
		CandidateLoadTimeout:  defaultCandidateLoadTimeout,
		CandidatePollInterval: defaultCandidatePoll,
		MaxStateTransitions:   defaultMaxTransitions,
		MaxRemoteSnapshots:    defaultMaxRemoteSnapshots,
	}
}

func (c Config) validate() error {
	if err := c.Validation.Validate(); err != nil {
		return fmt.Errorf("invalid description validation: %w", err)
	}
	if c.CandidateLoadTimeout <= 0 || c.CandidateLoadTimeout > 30*time.Second ||
		c.CandidatePollInterval <= 0 || c.CandidatePollInterval > c.CandidateLoadTimeout {
		return fmt.Errorf("invalid candidate loading durations")
	}
	if c.MaxStateTransitions <= 0 || c.MaxStateTransitions > maximumTransitions {
		return fmt.Errorf("state transition limit must be within 1..%d", maximumTransitions)
	}
	if c.MaxRemoteSnapshots < c.Validation.MaxCandidates || c.MaxRemoteSnapshots > maximumRemoteSnapshots {
		return fmt.Errorf("remote snapshot limit must be within %d..%d", c.Validation.MaxCandidates, maximumRemoteSnapshots)
	}
	return nil
}
