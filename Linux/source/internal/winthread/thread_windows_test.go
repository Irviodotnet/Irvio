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
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

var startupThreadID = windows.GetCurrentThreadId()

func TestNativeWorkersNeverUseStartupThread(t *testing.T) {
	for i := 0; i < 100; i++ {
		id, err := Call(func() (uint32, error) { return windows.GetCurrentThreadId(), nil })
		if err != nil || id == 0 || id == startupThreadID {
			t.Fatalf("worker=%d startup=%d error=%v", id, startupThreadID, err)
		}
	}
	want := errors.New("native fixture error")
	value, err := Call(func() (int, error) { return 42, want })
	if value != 42 || !errors.Is(err, want) {
		t.Fatal(value, err)
	}
}

func TestNativeWorkerPreservesUnrelatedPendingIO(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\IRVIO-worker-%d-%d`, windows.GetCurrentProcessId(), time.Now().UnixNano()))
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
		if windows.GetCurrentThreadId() == startupThreadID {
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
		worker, err := Call(func() (uint32, error) { return windows.GetCurrentThreadId(), nil })
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
