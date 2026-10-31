// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesession

import (
	"context"
	"errors"
	"testing"
)

func TestControlPhaseTransitions(t *testing.T) {
	manager := NewPhaseManager()
	stun, err := manager.Acquire(context.Background(), ControlPhaseSTUN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(context.Background(), ControlPhaseICE); !errors.Is(err, ErrControlBusy) {
		t.Fatalf("concurrent ICE acquire error = %v", err)
	}
	stun.Release()
	stun.Release()
	iceLease, err := manager.Acquire(context.Background(), ControlPhaseICE)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(context.Background(), ControlPhaseSTUN); !errors.Is(err, ErrControlBusy) {
		t.Fatalf("concurrent STUN acquire error = %v", err)
	}
	iceLease.Release()
	if manager.Phase() != ControlPhaseIdle {
		t.Fatal("phase did not return to idle")
	}
	again, err := manager.Acquire(context.Background(), ControlPhaseSTUN)
	if err != nil {
		t.Fatal(err)
	}
	again.Release()
}

func TestControlPhaseCancellationAndClose(t *testing.T) {
	manager := NewPhaseManager()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Acquire(ctx, ControlPhaseICE); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(context.Background(), ControlPhaseICE); !errors.Is(err, ErrClosed) {
		t.Fatalf("acquire after close error = %v", err)
	}
}
