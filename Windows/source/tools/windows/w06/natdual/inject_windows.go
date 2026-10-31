// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Lab-only ABI for the pinned WinDivert 2.2.2 x64 package, never a product dependency.
type divertAddress struct {
	Timestamp  int64
	Flags      uint32
	Reserved   uint32
	IfIndex    uint32
	SubIfIndex uint32
	Padding    [56]byte
}

var labDivertModule struct {
	once sync.Once
	dll  *windows.DLL
	err  error
}

// WinDivert owns per-thread TLS events, so its DLL must stay loaded for the process lifetime.
func loadLabDivert() (*windows.DLL, error) {
	labDivertModule.once.Do(func() {
		labDivertModule.dll, labDivertModule.err = windows.LoadDLL(`C:\IRVIO-Lab\W06-divert\WinDivert.dll`)
	})
	return labDivertModule.dll, labDivertModule.err
}

func (p *probe) injectThrough(index uint32, stage string) map[string]any {
	result := map[string]any{"stage": stage, "responses": 0, "upstream_captures": 0, "ifindex": index}
	if unsafe.Sizeof(divertAddress{}) != 80 {
		result["error"] = "invalid WinDivert ABI"
		return result
	}
	dll, err := loadLabDivert()
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	open, err := dll.FindProc("WinDivertOpen")
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	closeHandle, err := dll.FindProc("WinDivertClose")
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	send, err := dll.FindProc("WinDivertSend")
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	filter := []byte("false\x00")
	handle, _, callErr := open.Call(uintptr(unsafe.Pointer(&filter[0])), 1, 0, 8)
	runtime.KeepAlive(filter)
	if windows.Handle(handle) == windows.InvalidHandle {
		result["error"] = fmt.Sprintf("WinDivertOpen: %v", callErr)
		return result
	}
	defer func() {
		ret, _, err := closeHandle.Call(handle)
		if ret == 0 {
			result["close_error"] = err.Error()
		}
	}()
	var responses, captures int
	var attempts []map[string]any
	for id := 1; id <= 5; id++ {
		token := fmt.Sprintf("irvio-nat-%s-%d-%d", stage, id, time.Now().UnixNano())
		a := map[string]any{"id": id, "token": token}
		lc := net.ListenConfig{Control: p.physical.Control}
		conn, err := lc.ListenPacket(context.Background(), "udp4", net.JoinHostPort(p.physical.Source.String(), "0"))
		if err != nil {
			a["error"] = err.Error()
			attempts = append(attempts, a)
			continue
		}
		port := uint16(conn.LocalAddr().(*net.UDPAddr).Port)
		packet := udpPacket([]byte(token), p.physical.Source.As4(), [4]byte{192, 168, 23, 2}, port, 49495)
		address := divertAddress{Flags: 1, IfIndex: index}
		var sent uint32
		ret, _, sendErr := send.Call(handle, uintptr(unsafe.Pointer(&packet[0])), uintptr(len(packet)), uintptr(unsafe.Pointer(&sent)), uintptr(unsafe.Pointer(&address)))
		runtime.KeepAlive(packet)
		a["send_ok"], a["bytes"], a["local_port"] = ret != 0, sent, port
		if ret == 0 {
			a["error"] = sendErr.Error()
		} else {
			conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
			buf := make([]byte, 256)
			n, from, readErr := conn.ReadFrom(buf)
			if readErr != nil {
				a["error"] = readErr.Error()
			} else if string(buf[:n]) == token && from.String() == oracle {
				responses++
				a["reply"] = true
			} else {
				a["error"] = "oracle reply mismatch"
			}
		}
		conn.Close()
		packets := p.snapshot(token)
		a["packets"] = packets
		if len(packets) > 0 {
			captures++
		}
		attempts = append(attempts, a)
	}
	result["responses"], result["upstream_captures"], result["attempts"] = responses, captures, attempts
	return result
}
