// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package connsupervisor

import (
	"context"
	"fmt"
)

// Limiter bounds reconnect work shared by multiple supervisors.
type Limiter struct{ tokens chan struct{} }

// NewLimiter creates a process-local reconnect semaphore.
func NewLimiter(maxConcurrent int) (*Limiter, error) {
	if maxConcurrent <= 0 || maxConcurrent > 65536 {
		return nil, fmt.Errorf("reconnect concurrency must be within 1..65536")
	}
	return &Limiter{tokens: make(chan struct{}, maxConcurrent)}, nil
}

func (l *Limiter) acquire(ctx context.Context) error {
	select {
	case l.tokens <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Limiter) release() { <-l.tokens }
