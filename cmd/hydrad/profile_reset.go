// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"hydra.local/hydra/internal/killswitch"
	irvioruntime "hydra.local/hydra/internal/runtime"
)

// Exit socket-activated daemons cleanly for reactivation; only manually launched daemons may exec.
func completeProfileReset(logger *slog.Logger, socketActivated bool, reexec func() error) error {
	if socketActivated {
		logger.Info("PROFILE_DELETE_COMPLETE", "action", "socket-reactivation")
		return nil
	}
	logger.Info("PROFILE_DELETE_COMPLETE", "action", "restart")
	return reexec()
}

var (
	errProfileResetRequested = errors.New("Irvio profile reset requested")
	errProfileResetComplete  = errors.New("Irvio profile reset complete")
)

type profileRuntime interface {
	Status() irvioruntime.Status
	Disconnect(context.Context) error
}

type profileResetController struct {
	runtime profileRuntime
	ready   chan struct{}

	mu        sync.Mutex
	prepared  bool
	committed bool
}

func newProfileResetController(runtime profileRuntime) *profileResetController {
	return &profileResetController{runtime: runtime, ready: make(chan struct{}, 1)}
}

func (controller *profileResetController) PrepareDelete(ctx context.Context) error {
	if ctx == nil {
		return irvioruntime.ErrInvalidConfig
	}
	controller.mu.Lock()
	if controller.prepared || controller.committed {
		controller.mu.Unlock()
		return irvioruntime.ErrInvalidState
	}
	controller.prepared = true
	controller.mu.Unlock()

	status := controller.runtime.Status()
	if status.State == irvioruntime.StateIdle || status.State == irvioruntime.StateStopped ||
		status.State == irvioruntime.StateClosed {
		return nil
	}
	if err := controller.runtime.Disconnect(ctx); err != nil &&
		!errors.Is(err, irvioruntime.ErrNotConnected) && !errors.Is(err, killswitch.ErrNotProtected) {
		controller.CancelDelete()
		return err
	}
	return nil
}

func (controller *profileResetController) CommitDelete() {
	controller.mu.Lock()
	if !controller.prepared || controller.committed {
		controller.mu.Unlock()
		return
	}
	controller.prepared = false
	controller.committed = true
	controller.mu.Unlock()
	select {
	case controller.ready <- struct{}{}:
	default:
	}
}

func (controller *profileResetController) CancelDelete() {
	controller.mu.Lock()
	if !controller.committed {
		controller.prepared = false
	}
	controller.mu.Unlock()
}

func (controller *profileResetController) Committed() <-chan struct{} {
	return controller.ready
}
