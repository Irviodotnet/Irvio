// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// Controller owns user intent independently of ICE, AWG, and tunnel state.
type Controller struct {
	config Config

	opMu         sync.Mutex
	mu           sync.Mutex
	buildCond    *sync.Cond
	activeBuilds int
	state        State
	generation   uint64
	lifecycle    context.Context
	cancel       context.CancelFunc
	installation Installation
	resource     io.Closer
	kernel       KernelSnapshot
	adopted      bool
	updatedAt    time.Time
}

func New(config Config) (*Controller, error) {
	policy, err := normalizePolicy(config.Policy)
	if config.Backend == nil || err != nil {
		return nil, ErrInvalidConfig
	}
	config.Policy = policy
	if config.Now == nil {
		config.Now = time.Now
	}
	controller := &Controller{config: config, state: StateVPNOff, updatedAt: config.Now()}
	controller.buildCond = sync.NewCond(&controller.mu)
	return controller, nil
}

// Protect verifies kernel protection before committing intent and adopts valid crash residue.
func (c *Controller) Protect(ctx context.Context) (uint64, error) {
	if ctx == nil {
		return 0, ErrInvalidConfig
	}
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.mu.Lock()
	if c.state == StateClosed {
		c.mu.Unlock()
		return 0, ErrClosed
	}
	if c.state != StateVPNOff {
		c.mu.Unlock()
		return 0, ErrAlreadyProtected
	}
	c.generation++
	generation := c.generation
	c.state = StateProtecting
	c.updatedAt = c.config.Now()
	c.mu.Unlock()

	installation, result, err := c.config.Backend.Ensure(ctx, c.config.Policy)
	if err != nil {
		c.mu.Lock()
		if c.generation == generation && c.state == StateProtecting {
			c.state = StateVPNOff
			c.updatedAt = c.config.Now()
		}
		c.mu.Unlock()
		return 0, fmt.Errorf("install Irvio protection: %w", err)
	}
	if installation == nil || result < EnsureInstalled || result > EnsureReconciled {
		c.mu.Lock()
		if c.generation == generation && c.state == StateProtecting {
			c.state = StateVPNOff
			c.updatedAt = c.config.Now()
		}
		c.mu.Unlock()
		return 0, fmt.Errorf("install Irvio protection: %w", ErrConflict)
	}
	kernel, err := installation.Snapshot(ctx)
	if err != nil || !kernel.Installed || !kernel.Complete || kernel.Version != PolicyVersion {
		if err == nil {
			err = ErrConflict
		}
		if result == EnsureInstalled {
			_ = installation.Remove(context.Background())
		}
		c.mu.Lock()
		if c.generation == generation && c.state == StateProtecting {
			c.state = StateVPNOff
			c.updatedAt = c.config.Now()
		}
		c.mu.Unlock()
		return 0, fmt.Errorf("verify Irvio protection: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation || c.state != StateProtecting {
		if result == EnsureInstalled {
			_ = installation.Remove(context.Background())
		}
		return 0, ErrStaleGeneration
	}
	c.installation = installation
	c.kernel = kernel
	c.adopted = result != EnsureInstalled
	c.lifecycle, c.cancel = context.WithCancel(context.Background())
	c.state = StateProtectedConnecting
	c.updatedAt = c.config.Now()
	return generation, nil
}

// Transition records tunnel lifecycle while keeping protection independent.
func (c *Controller) Transition(generation uint64, state State) error {
	switch state {
	case StateProtectedConnecting, StateConnected, StateSamePeerRecovery, StateNetworkMigration, StateNoExit:
	default:
		return ErrInvalidConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == StateClosed {
		return ErrClosed
	}
	if generation != c.generation {
		return ErrStaleGeneration
	}
	if !c.state.Protected() || c.state == StateUnprotecting {
		return ErrNotProtected
	}
	c.state = state
	c.updatedAt = c.config.Now()
	return nil
}

// Reconcile verifies or atomically repairs the owned persistent policy.
func (c *Controller) Reconcile(ctx context.Context, generation uint64) error {
	c.mu.Lock()
	if generation != c.generation {
		c.mu.Unlock()
		return ErrStaleGeneration
	}
	installation := c.installation
	protected := c.state.Protected()
	c.mu.Unlock()
	if !protected || installation == nil {
		return ErrNotProtected
	}
	if err := installation.Reconcile(ctx); err != nil {
		return fmt.Errorf("reconcile Irvio protection: %w", err)
	}
	kernel, err := installation.Snapshot(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if generation == c.generation && c.state.Protected() {
		c.kernel = kernel
		c.updatedAt = c.config.Now()
	}
	c.mu.Unlock()
	return nil
}

// Activate builds resources only after protection commits; stale results are closed.
func (c *Controller) Activate(ctx context.Context, generation uint64, build ResourceBuilder) error {
	if ctx == nil || build == nil {
		return ErrInvalidConfig
	}
	c.mu.Lock()
	if c.state == StateClosed {
		c.mu.Unlock()
		return ErrClosed
	}
	if generation != c.generation {
		c.mu.Unlock()
		return ErrStaleGeneration
	}
	if !c.state.Protected() || c.state == StateUnprotecting {
		c.mu.Unlock()
		return ErrNotProtected
	}
	lifecycle := c.lifecycle
	c.activeBuilds++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.activeBuilds--
		c.buildCond.Broadcast()
		c.mu.Unlock()
	}()
	buildContext, cancel := context.WithCancel(ctx)
	stopLifecycleCancel := context.AfterFunc(lifecycle, cancel)
	defer func() {
		stopLifecycleCancel()
		cancel()
	}()

	resource, err := build(buildContext)
	if err != nil {
		_ = closeResource(resource)
		c.mu.Lock()
		if c.generation == generation && c.state.Protected() && c.state != StateUnprotecting {
			c.state = StateNoExit
			c.updatedAt = c.config.Now()
		}
		c.mu.Unlock()
		return fmt.Errorf("establish Irvio tunnel while protected: %w", err)
	}
	if resource == nil {
		return ErrInvalidConfig
	}
	c.mu.Lock()
	if c.generation != generation || !c.state.Protected() || c.state == StateUnprotecting {
		c.mu.Unlock()
		_ = resource.Close()
		return ErrStaleGeneration
	}
	old := c.resource
	c.resource = resource
	c.state = StateConnected
	c.updatedAt = c.config.Now()
	c.mu.Unlock()
	return closeResource(old)
}

// Disconnect removes protection only after successful teardown; errors remain fail-closed.
func (c *Controller) Disconnect(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	c.opMu.Lock()
	defer c.opMu.Unlock()
	return c.disconnect(ctx)
}

func (c *Controller) disconnect(ctx context.Context) error {
	c.mu.Lock()
	if c.state == StateClosed {
		c.mu.Unlock()
		return ErrClosed
	}
	if !c.state.Protected() {
		c.mu.Unlock()
		return ErrNotProtected
	}
	c.generation++
	generation := c.generation
	c.state = StateUnprotecting
	if c.cancel != nil {
		c.cancel()
	}
	c.updatedAt = c.config.Now()
	stopWait := context.AfterFunc(ctx, func() {
		c.mu.Lock()
		c.buildCond.Broadcast()
		c.mu.Unlock()
	})
	for c.activeBuilds > 0 && ctx.Err() == nil {
		c.buildCond.Wait()
	}
	stopWait()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("wait for protected tunnel setup: %w", err)
	}
	installation := c.installation
	resource := c.resource
	c.mu.Unlock()

	if err := closeResource(resource); err != nil {
		c.mu.Lock()
		if c.generation == generation && c.state == StateUnprotecting {
			c.state = StateNoExit
			c.updatedAt = c.config.Now()
		}
		c.mu.Unlock()
		return fmt.Errorf("tear down Irvio tunnel while protected: %w", err)
	}
	if err := installation.Remove(ctx); err != nil {
		return fmt.Errorf("remove Irvio protection: %w", err)
	}
	c.mu.Lock()
	if c.generation != generation || c.state != StateUnprotecting {
		c.mu.Unlock()
		return ErrStaleGeneration
	}
	c.installation = nil
	c.resource = nil
	c.kernel = KernelSnapshot{}
	c.adopted = false
	c.lifecycle = nil
	c.cancel = nil
	c.state = StateVPNOff
	c.updatedAt = c.config.Now()
	c.mu.Unlock()
	return nil
}

// Close becomes terminal only after successful teardown; failures remain protected and retryable.
func (c *Controller) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	if state == StateClosed {
		return nil
	}
	if state != StateVPNOff {
		if err := c.disconnect(ctx); err != nil {
			return err
		}
	}
	c.mu.Lock()
	if c.state != StateVPNOff {
		c.mu.Unlock()
		return ErrNotProtected
	}
	c.state = StateClosed
	c.updatedAt = c.config.Now()
	c.mu.Unlock()
	return nil
}

func closeResource(resource io.Closer) error {
	if resource == nil {
		return nil
	}
	return resource.Close()
}

// IsCurrent fences asynchronous routing, reconnect, and migration callbacks.
func (c *Controller) IsCurrent(generation uint64) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return generation == c.generation && c.state.Protected() && c.state != StateUnprotecting
}

// TrafficReader retains final statistics after Disconnect without retaining kernel resources.
func (c *Controller) TrafficReader() func() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if reader, ok := c.installation.(interface{ TrafficBytes() (uint64, error) }); ok {
		return reader.TrafficBytes
	}
	return nil
}

func (c *Controller) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{State: StateClosed}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	result := Snapshot{State: c.state, Generation: c.generation, Kernel: c.kernel, Adopted: c.adopted, UpdatedAt: c.updatedAt}
	result.Kernel.Counters = cloneCounters(c.kernel.Counters)
	return result
}

func cloneCounters(input map[string]Counter) map[string]Counter {
	result := make(map[string]Counter, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
