// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnel

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// This bounded lab diagnostic samples local handle types, never object names or foreign processes.
func tunHandleTypes() map[string]int {
	getInfo := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	query := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryObject")
	counts := make(map[string]int)
	var flags uint32
	var buffer [4096]byte
	for handle := uintptr(4); handle < 0x10000; handle += 4 {
		ok, _, _ := getInfo.Call(handle, uintptr(unsafe.Pointer(&flags)))
		if ok == 0 {
			continue
		}
		status, _, _ := query.Call(handle, 2, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		if status != 0 {
			counts["unqueried"]++
			continue
		}
		name := (*windows.NTUnicodeString)(unsafe.Pointer(&buffer[0]))
		counts[windows.UTF16ToString(unsafe.Slice(name.Buffer, int(name.Length)/2))]++
	}
	return counts
}
