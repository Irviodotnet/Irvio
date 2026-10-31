// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"sync"
	"time"
)

// Failed native Close releases its journal lock; retries recover that exact durable journal.
type windowsExitJournalCleanup struct {
	mu           sync.Mutex
	close        func() error
	recover      func(context.Context) error
	closed, done bool
}

func (c *windowsExitJournalCleanup) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return nil
	}
	var err error
	if !c.closed {
		c.closed = true
		err = c.close()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = c.recover(ctx)
	}
	c.done = err == nil
	return err
}

type windowsExitOwnedRoutes struct {
	windowsSessionRoutes
	cleanup windowsExitJournalCleanup
}

func (r *windowsExitOwnedRoutes) Close() error { return r.cleanup.Close() }

type windowsExitOwnedDNS struct {
	windowsSessionDNS
	cleanup windowsExitJournalCleanup
}

func (d *windowsExitOwnedDNS) Close() error { return d.cleanup.Close() }
