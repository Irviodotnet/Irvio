// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wintest

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"golang.org/x/sys/windows"
)

var startupThread = windows.GetCurrentThreadId()

// PreservePendingIO verifies that native workers never retire a Go thread owning unrelated I/O.
func PreservePendingIO(operation func() error) error {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\IRVIO-thread-test-%d-%d`, windows.GetCurrentProcessId(), time.Now().UnixNano()))
	if err != nil {
		return err
	}
	pipe, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 1024, 1024, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(pipe)
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	ov := &windows.Overlapped{HEvent: event}
	var pin runtime.Pinner
	pin.Pin(ov)
	defer pin.Unpin()
	ready := make(chan struct{})
	var submittedBy uint32
	var submit func()
	submit = func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if windows.GetCurrentThreadId() == startupThread {
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
		return fmt.Errorf("expected pending pipe operation: %w", err)
	}
	defer func() {
		_ = windows.CancelIoEx(pipe, ov)
		var transferred uint32
		_ = windows.GetOverlappedResult(pipe, ov, &transferred, true)
	}()
	for cycle := 0; cycle < 100; cycle++ {
		if err := operation(); err != nil {
			return err
		}
		var transferred uint32
		if err := windows.GetOverlappedResult(pipe, ov, &transferred, false); !errors.Is(err, windows.ERROR_IO_INCOMPLETE) {
			return fmt.Errorf("worker damaged unrelated I/O: thread=%d cycle=%d: %w", submittedBy, cycle, err)
		}
	}
	return nil
}
