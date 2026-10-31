// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func TestWindowsProductServiceLifecycle(t *testing.T) {
	requests, statuses := make(chan svc.ChangeRequest, 4), make(chan svc.Status, 16)
	ready, cleanup := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(cleanup) }) }
	defer finish()
	done := make(chan uint32, 1)
	handler := productWindowsService{run: func(ctx context.Context, notify func()) error {
		<-ready
		notify()
		<-ctx.Done()
		<-cleanup
		return nil
	}}
	go func() { _, code := handler.Execute(nil, requests, statuses); done <- code }()
	check := func(want svc.State) {
		t.Helper()
		select {
		case state := <-statuses:
			if state.State != want {
				t.Fatalf("state=%v want=%v", state, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing service status")
		}
	}
	check(svc.StartPending)
	close(ready)
	check(svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Interrogate}
	check(svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	check(svc.StopPending)
	select {
	case code := <-done:
		t.Fatalf("exited before cleanup: %d", code)
	default:
	}
	finish()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("intentional stop code=%d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service did not finish after cleanup")
	}
}

func TestWindowsProductServiceExitCodes(t *testing.T) {
	for _, failure := range []bool{false, true} {
		err := error(nil)
		if failure {
			err = errors.New("startup failed")
		}
		handler := productWindowsService{run: func(context.Context, func()) error { return err }}
		specific, code := handler.Execute(nil, make(chan svc.ChangeRequest), make(chan svc.Status, 16))
		if failure != specific || (code != 0) != failure {
			t.Fatalf("specific=%v code=%d failure=%v", specific, code, failure)
		}
	}
}

func TestWindowsProductResetLoop(t *testing.T) {
	calls := 0
	err := runWindowsProductLoop(context.Background(), productOptions{}, func(context.Context, productOptions) error {
		calls++
		if calls == 1 {
			return errProfileResetComplete
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	failure := errors.Join(errProfileResetComplete, errors.New("cleanup failed"))
	calls = 0
	err = runWindowsProductLoop(context.Background(), productOptions{}, func(context.Context, productOptions) error { calls++; return failure })
	if err != failure || calls != 1 {
		t.Fatalf("cleanup error swallowed: calls=%d err=%v", calls, err)
	}
}
