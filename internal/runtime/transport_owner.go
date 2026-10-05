// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
)

// TransportLifecycle may retain a predecessor until the replacement session commits.
type TransportLifecycle interface {
	Activate(context.Context, networkstate.State) error
	Current() *transportgen.Generation
	Commit(networkstate.Generation) error
	Close() error
}

// TransportOwner retains at most one predecessor for controlled handover.
type TransportOwner struct {
	builder          *transportgen.Builder
	requireFixedPort bool

	mu        sync.RWMutex
	current   *transportgen.Generation
	previous  *transportgen.Generation
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func NewTransportOwner(builder *transportgen.Builder) (*TransportOwner, error) {
	if builder == nil {
		return nil, ErrInvalidConfig
	}
	return &TransportOwner{builder: builder}, nil
}

func (o *TransportOwner) Activate(ctx context.Context, state networkstate.State) error {
	if o == nil || ctx == nil || state.Generation == 0 {
		return ErrInvalidConfig
	}
	o.mu.RLock()
	if o.closed {
		o.mu.RUnlock()
		return ErrClosed
	}
	current := o.current
	o.mu.RUnlock()
	if current != nil && current.ID() >= state.Generation {
		if current.ID() == state.Generation && current.Lifecycle() == transportgen.LifecycleReady {
			return nil
		}
		return ErrGenerationStale
	}
	generation, err := o.builder.Build(ctx, state)
	if err != nil && current != nil && errors.Is(err, syscall.EADDRINUSE) {
		if o.requireFixedPort {
			// Server firewall ports cannot follow a make-before-break ephemeral fallback.
			o.mu.Lock()
			if o.closed || o.current != current {
				o.mu.Unlock()
				return ErrGenerationStale
			}
			previous := o.previous
			o.current, o.previous = nil, nil
			o.mu.Unlock()
			current.MarkStale()
			if previous != nil {
				previous.MarkStale()
			}
			if closeErr := errors.Join(closeTransportGeneration(current), closeTransportGeneration(previous)); closeErr != nil {
				return fmt.Errorf("release fixed server UDP port: %w", closeErr)
			}
			generation, err = o.builder.Build(ctx, state)
		} else {
			generation, err = o.builder.BuildOnAnyPort(ctx, state)
		}
	}
	if err != nil {
		return fmt.Errorf("build transport generation %d: %w", state.Generation, err)
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		_ = generation.Close()
		return ErrClosed
	}
	if o.current != nil && o.current.ID() >= generation.ID() {
		o.mu.Unlock()
		_ = generation.Close()
		return ErrGenerationStale
	}
	obsolete := o.previous
	o.previous = o.current
	o.current = generation
	o.mu.Unlock()
	if obsolete != nil {
		obsolete.MarkStale()
		_ = obsolete.Close()
	}
	return nil
}

func (o *TransportOwner) Current() *transportgen.Generation {
	if o == nil {
		return nil
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.current
}

// Commit retires the predecessor only if id is still the current generation.
func (o *TransportOwner) Commit(id networkstate.Generation) error {
	if o == nil || id == 0 {
		return ErrInvalidConfig
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return ErrClosed
	}
	if o.current == nil || o.current.ID() != id {
		o.mu.Unlock()
		return ErrGenerationStale
	}
	previous := o.previous
	o.previous = nil
	o.mu.Unlock()
	if previous != nil {
		previous.MarkStale()
		return previous.Close()
	}
	return nil
}

func (o *TransportOwner) Close() error {
	if o == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		o.mu.Lock()
		o.closed = true
		current, previous := o.current, o.previous
		o.current, o.previous = nil, nil
		o.mu.Unlock()
		if current != nil {
			current.MarkStale()
		}
		if previous != nil {
			previous.MarkStale()
		}
		o.closeErr = errors.Join(closeTransportGeneration(current), closeTransportGeneration(previous))
	})
	return o.closeErr
}

func closeTransportGeneration(generation *transportgen.Generation) error {
	if generation == nil {
		return nil
	}
	return generation.Close()
}

var _ TransportLifecycle = (*TransportOwner)(nil)
