// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package wintest supplies bounded native diagnostics for Windows lab tests, never production policy.
package wintest

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// HandleTypes counts local handle types without collecting names or accessing another process.
func HandleTypes() map[string]int {
	get := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	query := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryObject")
	counts := map[string]int{}
	var flags uint32
	var buffer [4096]byte
	for h := uintptr(4); h < 0x10000; h += 4 {
		ok, _, _ := get.Call(h, uintptr(unsafe.Pointer(&flags)))
		if ok == 0 {
			continue
		}
		code, _, _ := query.Call(h, 2, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		if code != 0 {
			counts["unqueried"]++
			continue
		}
		name := (*windows.NTUnicodeString)(unsafe.Pointer(&buffer[0]))
		counts[windows.UTF16ToString(unsafe.Slice(name.Buffer, int(name.Length)/2))]++
	}
	return counts
}

// ResourceGrowth caps Go's known six-handle-per-M footprint and all remaining handle growth.
func ResourceGrowth(before, after map[string]int) error {
	threads := max(0, after["Thread"]-before["Thread"])
	if threads > 2 {
		return fmt.Errorf("thread growth exceeds bounded runtime allowance: %d", threads)
	}
	if before["unqueried"] != 0 || after["unqueried"] != 0 {
		return fmt.Errorf("incomplete handle type observation")
	}
	perThread := map[string]int{"Thread": 1, "Event": 2, "IRTimer": 2, "WaitCompletionPacket": 1}
	residual := 0
	for kind, n := range after {
		delta := n - before[kind] - perThread[kind]*threads
		if delta > 0 {
			residual += delta
		}
	}
	if residual > 4 {
		return fmt.Errorf("unexplained handle growth: %d (threads delta %d)", residual, threads)
	}
	return nil
}
