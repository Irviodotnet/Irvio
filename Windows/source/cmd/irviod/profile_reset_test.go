// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/productprofile"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
)

func TestProfileResetActivatedDaemonExitsWithoutExec(t *testing.T) {
	called := false
	err := completeProfileReset(slog.New(slog.NewTextHandler(io.Discard, nil)), true, func() error {
		called = true
		return errors.New("exec lost systemd fd")
	})
	if err != nil || called {
		t.Fatalf("activated reset must exit cleanly, not exec: called=%t err=%v", called, err)
	}
}

func TestProfileResetManualDaemonRetainsExecAndReportsFailure(t *testing.T) {
	failure := errors.New("exec failed")
	called := false
	err := completeProfileReset(slog.New(slog.NewTextHandler(io.Discard, nil)), false, func() error {
		called = true
		return failure
	})
	if !called || !errors.Is(err, failure) {
		t.Fatalf("manual reset did not retain exec: called=%t err=%v", called, err)
	}
}

type fakeProfileRuntime struct {
	status          irvioruntime.Status
	disconnectCalls int
	disconnectErr   error
}

func (runtime *fakeProfileRuntime) Status() irvioruntime.Status {
	return runtime.status
}

func (runtime *fakeProfileRuntime) Disconnect(context.Context) error {
	runtime.disconnectCalls++
	return runtime.disconnectErr
}

func TestProfileResetDisconnectsBeforeCommit(t *testing.T) {
	runtime := &fakeProfileRuntime{status: irvioruntime.Status{
		State:   irvioruntime.StateConnected,
		Country: exitselector.CountryIntent("DE"),
	}}
	controller := newProfileResetController(runtime)
	if err := controller.PrepareDelete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.disconnectCalls != 1 {
		t.Fatalf("disconnect calls = %d", runtime.disconnectCalls)
	}
	select {
	case <-controller.Committed():
		t.Fatal("reset committed before response delivery")
	default:
	}
	controller.CommitDelete()
	select {
	case <-controller.Committed():
	default:
		t.Fatal("reset was not committed")
	}
}

func TestProfileResetIdleAndDuplicatePreparation(t *testing.T) {
	runtime := &fakeProfileRuntime{status: irvioruntime.Status{State: irvioruntime.StateIdle}}
	controller := newProfileResetController(runtime)
	if err := controller.PrepareDelete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.disconnectCalls != 0 {
		t.Fatalf("disconnect calls = %d", runtime.disconnectCalls)
	}
	if err := controller.PrepareDelete(context.Background()); !errors.Is(err, irvioruntime.ErrInvalidState) {
		t.Fatalf("duplicate prepare error = %v", err)
	}
	controller.CancelDelete()
	if err := controller.PrepareDelete(context.Background()); err != nil {
		t.Fatalf("prepare after cancel: %v", err)
	}
}

func TestProfileResetValidationFailureDoesNotDisconnectOrCommit(t *testing.T) {
	runtime := &fakeProfileRuntime{status: irvioruntime.Status{State: irvioruntime.StateConnected}}
	controller := newProfileResetController(runtime)
	controller.validate = func() error { return productprofile.ErrProfileNotOwned }
	if err := controller.PrepareDelete(context.Background()); !errors.Is(err, productprofile.ErrProfileNotOwned) {
		t.Fatalf("validation error = %v", err)
	}
	controller.CommitDelete()
	select {
	case <-controller.Committed():
		t.Fatal("invalid deletion committed")
	default:
	}
	if runtime.disconnectCalls != 0 {
		t.Fatal("failed validation disconnected the session")
	}
	controller.validate = func() error { return nil }
	if err := controller.PrepareDelete(context.Background()); err != nil {
		t.Fatalf("retry after fixing contents: %v", err)
	}
}

func TestProfileResetCancelsPreparationAfterDisconnectFailure(t *testing.T) {
	runtime := &fakeProfileRuntime{
		status:        irvioruntime.Status{State: irvioruntime.StateConnected},
		disconnectErr: errors.New("disconnect failed"),
	}
	controller := newProfileResetController(runtime)
	if err := controller.PrepareDelete(context.Background()); err == nil {
		t.Fatal("expected disconnect error")
	}
	runtime.disconnectErr = nil
	if err := controller.PrepareDelete(context.Background()); err != nil {
		t.Fatalf("prepare after failure: %v", err)
	}
}

func TestProfileResetAcceptsAlreadyInactiveProtection(t *testing.T) {
	runtime := &fakeProfileRuntime{
		status:        irvioruntime.Status{State: irvioruntime.StateNoExit},
		disconnectErr: killswitch.ErrNotProtected,
	}
	controller := newProfileResetController(runtime)
	if err := controller.PrepareDelete(context.Background()); err != nil {
		t.Fatalf("prepare from no-exit state: %v", err)
	}
	if runtime.disconnectCalls != 1 {
		t.Fatalf("disconnect calls = %d", runtime.disconnectCalls)
	}
	controller.CommitDelete()
	select {
	case <-controller.Committed():
	default:
		t.Fatal("reset was not committed")
	}
}
