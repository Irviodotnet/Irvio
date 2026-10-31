// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// One raw TCP handshake and HTTP request verify packet injection without a TCP proxy.
func (p *probe) injectTCP() map[string]any {
	result := map[string]any{"stage": "forward-tcp", "success": false}
	dll, err := loadLabDivert()
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	api := make(map[string]*windows.Proc)
	for _, name := range []string{"WinDivertOpen", "WinDivertClose", "WinDivertShutdown", "WinDivertSend", "WinDivertRecv"} {
		proc, err := dll.FindProc(name)
		if err != nil {
			result["error"] = err.Error()
			return result
		}
		api[name] = proc
	}
	reserved, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IP(p.physical.Source.AsSlice())})
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	defer reserved.Close()
	port := uint16(reserved.Addr().(*net.TCPAddr).Port)
	result["translated_port"] = port
	defer func() { result["upstream_captures"] = len(p.snapshot(fmt.Sprintf("irvio-tcp-source-port-%d", port))) }()
	open := func(filter string, layer, flags uintptr) (uintptr, error) {
		b := []byte(filter + "\x00")
		h, _, err := api["WinDivertOpen"].Call(uintptr(unsafe.Pointer(&b[0])), layer, 100, flags)
		runtime.KeepAlive(b)
		if windows.Handle(h) == windows.InvalidHandle {
			return 0, err
		}
		return h, nil
	}
	sender, err := open("false", 1, 8)
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	defer api["WinDivertClose"].Call(sender)
	filter := fmt.Sprintf("inbound and ip and tcp and ip.SrcAddr==192.168.23.2 and ip.DstAddr==%s and tcp.SrcPort==49494 and tcp.DstPort==%d", p.physical.Source, port)
	receiver, err := open(filter, 0, 4)
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	packets := make(chan []byte, 32)
	errors := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		// The worker owns native TLS until thread termination; it must not return that thread to Go's pool.
		runtime.LockOSThread()
		defer close(done)
		for {
			b := make([]byte, 65535)
			var size uint32
			var addr divertAddress
			ret, _, err := api["WinDivertRecv"].Call(receiver, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&addr)))
			runtime.KeepAlive(b)
			if ret == 0 {
				errors <- err
				return
			}
			if size > uint32(len(b)) || addr.IfIndex != p.physical.Index {
				errors <- fmt.Errorf("invalid receive interface/length")
				return
			}
			select {
			case packets <- b[:size]:
			default:
			}
		}
	}()
	defer func() { api["WinDivertShutdown"].Call(receiver, 1); api["WinDivertClose"].Call(receiver); <-done }()
	send := func(seq, ack uint32, flags byte, payload []byte) error {
		packet := tcpPacket(p.physical.Source.As4(), [4]byte{192, 168, 23, 2}, port, 49494, seq, ack, flags, payload)
		addr := divertAddress{Flags: 1, IfIndex: p.physical.Index}
		var size uint32
		ret, _, err := api["WinDivertSend"].Call(sender, uintptr(unsafe.Pointer(&packet[0])), uintptr(len(packet)), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&addr)))
		runtime.KeepAlive(packet)
		if ret == 0 || size != uint32(len(packet)) {
			return fmt.Errorf("send: %d bytes, %v", size, err)
		}
		return nil
	}
	seq := uint32(time.Now().UnixNano())
	var peerSeq uint32
	state := "SYN_SENT"
	if err = send(seq, 0, 2, nil); err != nil {
		result["error"] = err.Error()
		return result
	}
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			result["error"] = "TCP deadline in " + state
			return result
		case err := <-errors:
			result["error"] = err.Error()
			return result
		case b := <-packets:
			if len(b) < 40 || b[0] != 0x45 || b[9] != 6 {
				result["error"] = "invalid IPv4/TCP reply"
				return result
			}
			hlen := int(b[32]>>4) * 4
			if hlen < 20 || 20+hlen > len(b) {
				result["error"] = "invalid TCP header"
				return result
			}
			flags := b[33]
			ack := binary.BigEndian.Uint32(b[28:32])
			remoteSeq := binary.BigEndian.Uint32(b[24:28])
			payload := b[20+hlen:]
			if flags&4 != 0 {
				result["error"] = "TCP reset in " + state
				return result
			}
			if state == "SYN_SENT" {
				if flags&0x12 != 0x12 || ack != seq+1 {
					continue
				}
				peerSeq = remoteSeq + 1
				seq++
				if err = send(seq, peerSeq, 0x10, nil); err != nil {
					result["error"] = err.Error()
					return result
				}
				request := []byte("GET /irvio-lab HTTP/1.0\r\nHost: irvio-lab\r\nConnection: close\r\n\r\n")
				if err = send(seq, peerSeq, 0x18, request); err != nil {
					result["error"] = err.Error()
					return result
				}
				seq += uint32(len(request))
				state = "REQUEST_SENT"
				result["handshake"] = true
				continue
			}
			if remoteSeq != peerSeq || len(payload) == 0 {
				continue
			}
			if !strings.HasPrefix(string(payload), "HTTP/") {
				result["error"] = "unexpected HTTP reply"
				return result
			}
			returned := append([]byte(nil), b...)
			copy(returned[16:20], []byte{10, 253, 240, 2})
			binary.BigEndian.PutUint16(returned[22:24], 49000)
			fixTCPChecksum(returned)
			result["sequence_preserved"] = binary.BigEndian.Uint32(returned[24:28]) == remoteSeq
			result["inner_destination"] = net.IP(returned[16:20]).String()
			result["inner_port"] = binary.BigEndian.Uint16(returned[22:24])
			result["http_status"] = strings.SplitN(string(payload), "\r\n", 2)[0]
			peerSeq += uint32(len(payload))
			if flags&1 != 0 {
				peerSeq++
			}
			if err = send(seq, peerSeq, 0x14, nil); err != nil {
				result["error"] = err.Error()
				return result
			}
			result["success"] = true
			return result
		}
	}
}

func tcpPacket(source, destination [4]byte, sp, dp uint16, seq, ack uint32, flags byte, payload []byte) []byte {
	b := make([]byte, 40+len(payload))
	b[0], b[8], b[9] = 0x45, 64, 6
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	copy(b[12:16], source[:])
	copy(b[16:20], destination[:])
	binary.BigEndian.PutUint16(b[20:22], sp)
	binary.BigEndian.PutUint16(b[22:24], dp)
	binary.BigEndian.PutUint32(b[24:28], seq)
	binary.BigEndian.PutUint32(b[28:32], ack)
	b[32], b[33] = 5<<4, flags
	binary.BigEndian.PutUint16(b[34:36], 65535)
	copy(b[40:], payload)
	fixTCPChecksum(b)
	return b
}

func checksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) > 0 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func fixTCPChecksum(b []byte) {
	b[10], b[11], b[36], b[37] = 0, 0, 0, 0
	binary.BigEndian.PutUint16(b[10:12], checksum(b[:20]))
	pseudo := make([]byte, 12+len(b)-20)
	copy(pseudo[:8], b[12:20])
	pseudo[9] = 6
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(b)-20))
	copy(pseudo[12:], b[20:])
	binary.BigEndian.PutUint16(b[36:38], checksum(pseudo))
}
