// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package icmpv4 validates IPv4 errors quoting the Exit's supported TCP/UDP packets.
package icmpv4

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

var ErrPacket = errors.New("invalid or unsupported ICMPv4 error")

type Error struct {
	Router, Recipient                   netip.Addr
	OriginalSource, OriginalDestination netip.Addr
	SourcePort, DestinationPort         uint16
	Protocol                            uint8
	raw                                 []byte
	quotedLength                        int
}

// ParseError accepts only unfragmented, option-free errors with a valid TCP/UDP quotation.
func ParseError(b []byte) (Error, error) {
	if len(b) < 56 || len(b) > 65535 || b[0] != 0x45 || b[9] != 1 ||
		int(binary.BigEndian.Uint16(b[2:4])) != len(b) || binary.BigEndian.Uint16(b[6:8])&0xbfff != 0 ||
		b[8] <= 1 || checksum(b[:20]) != 0 || checksum(b[20:]) != 0 {
		return Error{}, ErrPacket
	}
	if !(b[20] == 3 && b[21] <= 15 || b[20] == 11 && b[21] <= 1 || b[20] == 12 && b[21] <= 2) {
		return Error{}, ErrPacket
	}
	n := len(b) - 28
	if words := int(b[25]); words != 0 {
		if words*4 < 128 || words*4 > n {
			return Error{}, ErrPacket
		}
		n = words * 4
	}
	q := b[28:]
	total := int(binary.BigEndian.Uint16(q[2:4]))
	if q[0] != 0x45 || total < 28 || binary.BigEndian.Uint16(q[6:8])&0xbfff != 0 || checksum(q[:20]) != 0 {
		return Error{}, ErrPacket
	}
	n = min(n, total)
	switch q[9] {
	case 6:
		if total < 40 || n == 37 || n >= 33 && (q[32]>>4 < 5 || int(q[32]>>4)*4 > total-20) {
			return Error{}, ErrPacket
		}
	case 17:
		if int(binary.BigEndian.Uint16(q[24:26])) != total-20 {
			return Error{}, ErrPacket
		}
	default:
		return Error{}, ErrPacket
	}
	p := Error{Router: netip.AddrFrom4([4]byte(b[12:16])), Recipient: netip.AddrFrom4([4]byte(b[16:20])),
		OriginalSource: netip.AddrFrom4([4]byte(q[12:16])), OriginalDestination: netip.AddrFrom4([4]byte(q[16:20])),
		SourcePort: binary.BigEndian.Uint16(q[20:22]), DestinationPort: binary.BigEndian.Uint16(q[22:24]),
		Protocol: q[9], raw: b, quotedLength: n}
	if !p.Router.IsGlobalUnicast() || p.Router.IsLoopback() || !p.Recipient.IsGlobalUnicast() ||
		p.SourcePort == 0 || p.DestinationPort == 0 {
		return Error{}, ErrPacket
	}
	return p, nil
}

// Translate restores the quoted source and outer destination without changing MTU or extension data.
func (p Error) Translate(address netip.Addr, port uint16) ([]byte, error) {
	if p.raw == nil || !address.Is4() || !address.IsGlobalUnicast() || port == 0 {
		return nil, ErrPacket
	}
	b := append([]byte(nil), p.raw...)
	q := b[28:]
	offset := 36
	if p.Protocol == 17 {
		offset = 26
	}
	if p.quotedLength >= offset+2 {
		old := binary.BigEndian.Uint16(q[offset : offset+2])
		if p.Protocol != 17 || old != 0 {
			value := replaceChecksum(old, q[12:16], address.AsSlice())
			var ports [4]byte
			binary.BigEndian.PutUint16(ports[:2], p.SourcePort)
			binary.BigEndian.PutUint16(ports[2:], port)
			value = replaceChecksum(value, ports[:2], ports[2:])
			if p.Protocol == 17 && value == 0 {
				value = 65535
			}
			binary.BigEndian.PutUint16(q[offset:offset+2], value)
		}
	}
	copy(q[12:16], address.AsSlice())
	binary.BigEndian.PutUint16(q[20:22], port)
	q[10], q[11] = 0, 0
	binary.BigEndian.PutUint16(q[10:12], checksum(q[:20]))
	copy(b[16:20], address.AsSlice())
	b[8]--
	b[10], b[11] = 0, 0
	binary.BigEndian.PutUint16(b[10:12], checksum(b[:20]))
	b[22], b[23] = 0, 0
	binary.BigEndian.PutUint16(b[22:24], checksum(b[20:]))
	return b, nil
}

func checksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) != 0 {
		sum += uint32(b[0]) << 8
	}
	return fold(sum)
}

func fold(sum uint32) uint16 {
	for sum>>16 != 0 {
		sum = sum&65535 + sum>>16
	}
	return ^uint16(sum)
}

func replaceChecksum(old uint16, before, after []byte) uint16 {
	sum := uint32(^old)
	for i := 0; i < len(before); i += 2 {
		sum += uint32(^binary.BigEndian.Uint16(before[i:i+2])) + uint32(binary.BigEndian.Uint16(after[i:i+2]))
	}
	return fold(sum)
}
