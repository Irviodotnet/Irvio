// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Five fixed, synthetic UDP flows test packet translation without adding a production NAT engine.
func (p *probe) injectNAT() map[string]any {
	result := map[string]any{"stage": "forward-nat", "responses": 0, "upstream_captures": 0}
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
	recv, err := dll.FindProc("WinDivertRecv")
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	shutdown, err := dll.FindProc("WinDivertShutdown")
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	falseFilter := []byte("false\x00")
	sender, _, callErr := open.Call(uintptr(unsafe.Pointer(&falseFilter[0])), 1, 0, 8)
	runtime.KeepAlive(falseFilter)
	if windows.Handle(sender) == windows.InvalidHandle {
		result["error"] = callErr.Error()
		return result
	}
	defer closeHandle.Call(sender)
	type flow struct {
		socket net.PacketConn
		port   uint16
		inner  [4]byte
		token  string
	}
	flows := make(map[uint16]flow)
	defer func() {
		for _, f := range flows {
			f.socket.Close()
		}
	}()
	var filters []string
	for id := byte(2); id <= 6; id++ {
		lc := net.ListenConfig{Control: p.physical.Control}
		conn, err := lc.ListenPacket(context.Background(), "udp4", net.JoinHostPort(p.physical.Source.String(), "0"))
		if err != nil {
			result["error"] = err.Error()
			return result
		}
		port := uint16(conn.LocalAddr().(*net.UDPAddr).Port)
		flows[port] = flow{conn, port, [4]byte{10, 253, 240, id}, fmt.Sprintf("irvio-nat-packet-%d-%d", id, time.Now().UnixNano())}
		filters = append(filters, fmt.Sprintf("udp.DstPort==%d", port))
	}
	filter := []byte(fmt.Sprintf("inbound and ip and udp and ip.DstAddr==%s and ip.SrcAddr==192.168.23.2 and udp.SrcPort==49495 and (%s)\x00", p.physical.Source, strings.Join(filters, " or ")))
	receiver, _, callErr := open.Call(uintptr(unsafe.Pointer(&filter[0])), 0, 100, 4)
	runtime.KeepAlive(filter)
	if windows.Handle(receiver) == windows.InvalidHandle {
		result["error"] = callErr.Error()
		return result
	}
	type incoming struct {
		packet []byte
		iface  uint32
		err    error
	}
	packets := make(chan incoming, 8)
	done := make(chan struct{})
	go func() {
		// A terminal native-I/O worker releases the DLL's thread-local event when its OS thread exits.
		runtime.LockOSThread()
		defer close(done)
		for {
			buf := make([]byte, 65535)
			var length uint32
			var addr divertAddress
			ret, _, err := recv.Call(receiver, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&addr)))
			runtime.KeepAlive(buf)
			if ret == 0 {
				select {
				case packets <- incoming{err: err}:
				default:
				}
				return
			}
			if length > uint32(len(buf)) {
				select {
				case packets <- incoming{err: fmt.Errorf("invalid receive length")}:
				default:
				}
				return
			}
			select {
			case packets <- incoming{packet: buf[:length], iface: addr.IfIndex}:
			default:
			}
		}
	}()
	defer func() { shutdown.Call(receiver, 1); closeHandle.Call(receiver); <-done }()
	for _, f := range flows {
		original := udpPacket([]byte(f.token), f.inner, [4]byte{192, 168, 23, 2}, 49000, 49495)
		translated := append([]byte(nil), original...)
		copy(translated[12:16], p.physical.Source.AsSlice())
		binary.BigEndian.PutUint16(translated[20:22], f.port)
		addr := divertAddress{Flags: 1, IfIndex: p.physical.Index}
		var length uint32
		ret, _, err := send.Call(sender, uintptr(unsafe.Pointer(&translated[0])), uintptr(len(translated)), uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&addr)))
		runtime.KeepAlive(translated)
		if ret == 0 || length != uint32(len(translated)) {
			result["error"] = fmt.Sprintf("send: %d bytes, %v", length, err)
			return result
		}
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	seen := make(map[uint16]bool)
	var records []map[string]any
	for len(seen) < 5 {
		select {
		case <-deadline.C:
			result["error"] = "reply deadline"
			goto finished
		case received := <-packets:
			if received.err != nil {
				result["error"] = received.err.Error()
				goto finished
			}
			b := received.packet
			if len(b) < 28 || b[0] != 0x45 || b[9] != 17 {
				result["error"] = "unexpected IPv4/UDP reply"
				goto finished
			}
			port := binary.BigEndian.Uint16(b[22:24])
			f, ok := flows[port]
			if !ok || seen[port] || received.iface != p.physical.Index || string(b[28:]) != f.token || net.IP(b[12:16]).String() != "192.168.23.2" || binary.BigEndian.Uint16(b[20:22]) != 49495 {
				result["error"] = "wrong flow, source or physical interface"
				goto finished
			}
			returned := udpPacket(b[28:], [4]byte{192, 168, 23, 2}, f.inner, 49495, 49000)
			seen[port] = true
			records = append(records, map[string]any{"inner_destination": net.IP(returned[16:20]).String(), "inner_port": binary.BigEndian.Uint16(returned[22:24]), "translated_port": port, "receive_ifindex": received.iface, "payload_ok": string(returned[28:]) == f.token})
		}
	}
finished:
	captures := 0
	for _, f := range flows {
		if len(p.snapshot(f.token)) > 0 {
			captures++
		}
	}
	result["responses"], result["upstream_captures"], result["flows"] = len(seen), captures, records
	return result
}
