// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package exitnat provides bounded lease-owned translation for the Windows Exit runtime.
package exitnat

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

const (
	ICMP uint8 = 1
	TCP  uint8 = 6
	UDP  uint8 = 17
)

var (
	ErrPacket      = errors.New("invalid Exit packet")
	ErrUnsupported = errors.New("unsupported Exit packet form")
	ErrDenied      = errors.New("Exit packet denied")
	ErrStale       = errors.New("stale Exit ownership")
	ErrCapacity    = errors.New("Exit translation capacity exhausted")
	ErrConfig      = errors.New("invalid Exit translation configuration")
)

type packet struct {
	raw                         []byte
	source, destination         netip.Addr
	sourcePort, destinationPort uint16
	protocol, flags             uint8
	sequence, ack, end          uint32
}

func parse(b []byte) (packet, error) {
	if len(b) < 20 || len(b) > 65535 {
		return packet{}, ErrPacket
	}
	if b[0]>>4 != 4 || b[0]&15 != 5 {
		return packet{}, ErrUnsupported
	}
	if int(binary.BigEndian.Uint16(b[2:4])) != len(b) || checksum(b[:20]) != 0 {
		return packet{}, ErrPacket
	}
	if binary.BigEndian.Uint16(b[6:8])&0xbfff != 0 {
		return packet{}, ErrUnsupported
	}
	if b[8] <= 1 {
		return packet{}, ErrDenied
	}
	p := packet{raw: b, source: netip.AddrFrom4([4]byte(b[12:16])), destination: netip.AddrFrom4([4]byte(b[16:20])), protocol: b[9]}
	u := b[20:]
	switch p.protocol {
	case ICMP:
		if len(u) < 8 || (u[0] != 8 && u[0] != 0) || u[1] != 0 || checksum(u) != 0 {
			return packet{}, ErrPacket
		}
		p.sourcePort, p.destinationPort = binary.BigEndian.Uint16(u[4:6]), binary.BigEndian.Uint16(u[6:8])
		return p, nil
	case UDP:
		if len(u) < 8 || int(binary.BigEndian.Uint16(u[4:6])) != len(u) {
			return packet{}, ErrPacket
		}
		if binary.BigEndian.Uint16(u[6:8]) != 0 && transportChecksum(b) != 0 {
			return packet{}, ErrPacket
		}
	case TCP:
		if len(u) < 20 || int(u[12]>>4)*4 < 20 || int(u[12]>>4)*4 > len(u) || transportChecksum(b) != 0 {
			return packet{}, ErrPacket
		}
		p.flags = u[13]
		p.sequence, p.ack = binary.BigEndian.Uint32(u[4:8]), binary.BigEndian.Uint32(u[8:12])
		p.end = p.sequence + uint32(len(u)-int(u[12]>>4)*4)
		if p.flags&2 != 0 {
			p.end++
		}
		if p.flags&1 != 0 {
			p.end++
		}
		if p.flags&2 != 0 && p.flags&5 != 0 {
			return packet{}, ErrPacket
		}
	default:
		return packet{}, ErrUnsupported
	}
	p.sourcePort, p.destinationPort = binary.BigEndian.Uint16(u[:2]), binary.BigEndian.Uint16(u[2:4])
	if p.sourcePort == 0 || p.destinationPort == 0 {
		return packet{}, ErrPacket
	}
	return p, nil
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
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	return ^uint16(sum)
}

func transportChecksum(b []byte) uint16 {
	var sum uint32
	for i := 12; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	sum += uint32(b[9]) + uint32(len(b)-20)
	u := b[20:]
	for len(u) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(u))
		u = u[2:]
	}
	if len(u) != 0 {
		sum += uint32(u[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	return ^uint16(sum)
}

func rewrite(p packet, address netip.Addr, port uint16, outbound bool) []byte {
	b := append([]byte(nil), p.raw...)
	if outbound {
		copy(b[12:16], address.AsSlice())
		binary.BigEndian.PutUint16(b[20:22], port)
	} else {
		copy(b[16:20], address.AsSlice())
		binary.BigEndian.PutUint16(b[22:24], port)
	}
	b[8]--
	b[10], b[11] = 0, 0
	binary.BigEndian.PutUint16(b[10:12], checksum(b[:20]))
	offset := 36
	if p.protocol == UDP {
		offset = 26
	}
	b[offset], b[offset+1] = 0, 0
	c := transportChecksum(b)
	if c == 0 && p.protocol == UDP {
		c = 65535
	}
	binary.BigEndian.PutUint16(b[offset:offset+2], c)
	return b
}
