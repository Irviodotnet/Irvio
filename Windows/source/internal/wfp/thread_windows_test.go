// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"runtime"
	"testing"
	"time"
)

var wfpTestStartupThreadID = windows.GetCurrentThreadId()

func TestNativeTransactionPreservesUnrelatedPendingIO(t *testing.T) {
	s := nativeStore(t)
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\IRVIO-wfp-worker-%d-%d`, windows.GetCurrentProcessId(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 1024, 1024, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(pipe)
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	ov := &windows.Overlapped{HEvent: event}
	var pin runtime.Pinner
	pin.Pin(ov)
	defer pin.Unpin()
	var submittedBy uint32
	ready := make(chan struct{})
	var submit func()
	submit = func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if windows.GetCurrentThreadId() == wfpTestStartupThreadID {
			go submit()
			<-ready
			return
		}
		submittedBy = windows.GetCurrentThreadId()
		err = windows.ConnectNamedPipe(pipe, ov)
		close(ready)
	}
	go submit()
	<-ready
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		t.Fatal("connect was not pending", err)
	}
	defer func() {
		_ = windows.CancelIoEx(pipe, ov)
		var transferred uint32
		_ = windows.GetOverlappedResult(pipe, ov, &transferred, true)
	}()
	for i := 0; i < 100; i++ {
		var worker uint32
		err := s.transaction(context.Background(), true, func(windows.Handle) error {
			worker = windows.GetCurrentThreadId()
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if worker == submittedBy {
			t.Logf("worker reused submitting thread %d", worker)
		}
		var transferred uint32
		if err := windows.GetOverlappedResult(pipe, ov, &transferred, false); !errors.Is(err, windows.ERROR_IO_INCOMPLETE) {
			t.Fatalf("worker terminated unrelated I/O: submitted_thread=%d worker=%d cycle=%d result=%v", submittedBy, worker, i, err)
		}
	}
}
