// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package networkstate

import (
	"errors"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func fakeWindowsRegistration(handle windows.Handle) windowsNotifyFunc {
	return func(_ uint16, _ uintptr, _ unsafe.Pointer, _ bool, h *windows.Handle) error { *h = handle; return nil }
}

func TestWindowsSubscriptionCoalescesAndClosesDuringCallbacks(t *testing.T) {
	var s *windowsSubscription
	s, err := subscribeWindows(1, []windowsNotifyFunc{fakeWindowsRegistration(1)}, func(windows.Handle) error {
		// Simulate cancellation waiting for a callback; Close must not own its mutex.
		s.notify()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 100; i++ {
		deliverWindowsNetworkChange(s.context, nil, 0)
	}
	if len(s.events) != 1 {
		t.Fatalf("events not coalesced: %d", len(s.events))
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.notify()
			}
		}()
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := windowsSubscriptions.Load(*s.context); ok {
		t.Fatal("registry entry retained after cancel")
	}
	for range s.events {
	}
}

func TestWindowsSubscriptionRegistrationFailureCancelsOwnedHandles(t *testing.T) {
	canceled := 0
	var key uint64
	fail := func(_ uint16, _ uintptr, context unsafe.Pointer, _ bool, _ *windows.Handle) error {
		key = *(*uint64)(context)
		return windows.ERROR_ACCESS_DENIED
	}
	_, err := subscribeWindows(1, []windowsNotifyFunc{fakeWindowsRegistration(7), fail}, func(h windows.Handle) error {
		if h != 7 {
			t.Fatalf("unexpected handle %v", h)
		}
		canceled++
		return nil
	})
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || canceled != 1 {
		t.Fatalf("rollback: %d %v", canceled, err)
	}
	if _, ok := windowsSubscriptions.Load(key); ok {
		t.Fatal("failed subscription retained")
	}
}

func TestWindowsSubscriptionCancelFailureRetainsSafeContext(t *testing.T) {
	calls := 0
	s, err := subscribeWindows(1, []windowsNotifyFunc{fakeWindowsRegistration(1)}, func(windows.Handle) error {
		calls++
		if calls == 1 {
			return windows.ERROR_ACCESS_DENIED
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Close(); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatal(err)
	}
	runtime.GC()
	if _, ok := windowsSubscriptions.Load(*s.context); !ok {
		t.Fatal("native context freed before cancellation")
	}
	deliverWindowsNetworkChange(s.context, nil, 0)
	if len(s.events) != 0 {
		t.Fatal("event published after close intent")
	}
	if err := s.Close(); err != nil || calls != 2 {
		t.Fatalf("cancel retry: %d %v", calls, err)
	}
}

func TestWindowsSubscriptionReconciliationStopsOnClose(t *testing.T) {
	s, err := subscribeWindows(1, []windowsNotifyFunc{fakeWindowsRegistration(1)}, func(windows.Handle) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.startReconcile(time.Millisecond)
	select {
	case <-s.events:
	case <-time.After(time.Second):
		t.Fatal("missing periodic reconciliation")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.reconcileDone:
	default:
		t.Fatal("reconciliation goroutine survived Close")
	}
}

func TestWindowsNativeSubscriptionLifecycle(t *testing.T) {
	previousGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGC)
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	cycle := func() {
		t.Helper()
		s, err := newPlatformSubscription(1)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		cycle()
	}
	countHandles := func() uint32 {
		t.Helper()
		var count uint32
		result, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
		if result == 0 {
			t.Fatal(err)
		}
		return count
	}
	before := countHandles()
	for i := 0; i < 100; i++ {
		cycle()
	}
	after := countHandles()
	t.Logf("100 native subscription cycles: handles %d -> %d", before, after)
	if after > before+4 {
		t.Fatalf("native subscription handles grew: %d -> %d", before, after)
	}
	count := 0
	windowsSubscriptions.Range(func(_, _ any) bool { count++; return true })
	if count != 0 {
		t.Fatalf("native subscription contexts leaked: %d", count)
	}
}
