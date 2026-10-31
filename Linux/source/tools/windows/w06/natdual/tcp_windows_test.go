// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unsafe"
)

func TestLabDivertAddressLayout(t *testing.T) {
	var address divertAddress
	if unsafe.Sizeof(address) != 80 || unsafe.Offsetof(address.Flags) != 8 || unsafe.Offsetof(address.IfIndex) != 16 {
		t.Fatal("WinDivert ABI differs")
	}
}

func TestLabTCPChecksumAndTranslationPreservePayloadAndSequence(t *testing.T) {
	for _, payload := range [][]byte{nil, []byte("odd"), []byte("even")} {
		packet := tcpPacket([4]byte{192, 168, 23, 2}, [4]byte{10, 0, 2, 15}, 49494, 55100, 12345, 98765, 0x18, payload)
		checkTCP := func(b []byte) {
			t.Helper()
			if checksum(b[:20]) != 0 {
				t.Fatal("invalid IP checksum")
			}
			pseudo := make([]byte, 12+len(b)-20)
			copy(pseudo[:8], b[12:20])
			pseudo[9] = 6
			binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(b)-20))
			copy(pseudo[12:], b[20:])
			if checksum(pseudo) != 0 {
				t.Fatal("invalid TCP checksum")
			}
		}
		checkTCP(packet)
		copy(packet[16:20], []byte{10, 253, 240, 2})
		binary.BigEndian.PutUint16(packet[22:24], 49000)
		fixTCPChecksum(packet)
		checkTCP(packet)
		if binary.BigEndian.Uint32(packet[24:28]) != 12345 || binary.BigEndian.Uint32(packet[28:32]) != 98765 || !bytes.Equal(packet[40:], payload) {
			t.Fatal("translation changed TCP sequence or payload")
		}
	}
}
