// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import (
	"fmt"
	"time"
)

const (
	defaultObservationLimit = 16
	minimumObservationLimit = 7
	maximumObservationLimit = 64
)

// Config controls pacing and bounded evidence retention.
type Config struct {
	MinimumTransactionInterval time.Duration
	MaxObservations            int
}

// DefaultConfig returns a maximum rate of ten new transactions per second.
func DefaultConfig() Config {
	return Config{MinimumTransactionInterval: 100 * time.Millisecond, MaxObservations: defaultObservationLimit}
}

func (c Config) validate() error {
	if c.MinimumTransactionInterval < 0 {
		return fmt.Errorf("minimum transaction interval cannot be negative")
	}
	if c.MaxObservations < minimumObservationLimit || c.MaxObservations > maximumObservationLimit {
		return fmt.Errorf("maximum observations must be within %d..%d", minimumObservationLimit, maximumObservationLimit)
	}
	return nil
}
