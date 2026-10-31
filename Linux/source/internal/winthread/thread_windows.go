// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winthread

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/windows"
)

var nativeCalls sync.Map
var nativeCallID atomic.Uintptr
var createNativeThread = windows.NewLazySystemDLL("kernel32.dll").NewProc("CreateThread")
var nativeThreadEntry = windows.NewCallback(func(id uintptr) uintptr {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	call, ok := nativeCalls.Load(id)
	if !ok {
		return 1
	}
	call.(func())()
	return 0
})

// Call waits for actual OS-thread termination, including vendor DLL_THREAD_DETACH cleanup.
func Call[T any](fn func() (T, error)) (T, error) {
	var value T
	var callErr error
	done := make(chan struct{})
	id := nativeCallID.Add(1)
	if id == 0 {
		return value, errors.New("native worker token exhausted")
	}
	if _, loaded := nativeCalls.LoadOrStore(id, func() {
		defer close(done)
		value, callErr = fn()
	}); loaded {
		return value, errors.New("native worker token collision")
	}
	defer nativeCalls.Delete(id)
	// A fresh OS thread must not terminate a reused Go M that owns unrelated pending I/O.
	thread, _, err := createNativeThread.Call(0, 0, nativeThreadEntry, id, 0, 0)
	if thread == 0 {
		return value, fmt.Errorf("create vendor worker thread: %w", err)
	}
	status, waitErr := windows.WaitForSingleObject(windows.Handle(thread), windows.INFINITE)
	<-done
	if waitErr != nil || status != windows.WAIT_OBJECT_0 {
		callErr = errors.Join(callErr, fmt.Errorf("wait for vendor thread termination: status=%d: %w", status, waitErr))
	}
	return value, errors.Join(callErr, windows.CloseHandle(windows.Handle(thread)))
}
