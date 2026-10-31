// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"fmt"
	"net/netip"
)

const ExitPoolFilename = "exit-pool-v1.json"
const ListenPortFilename = "listen-port-v1.json"
const exitPoolChoices = 254 * 256

type exitPoolRecord struct {
	Version int    `json:"version"`
	IPv4    string `json:"ipv4"`
}

type listenPortRecord struct {
	Version int `json:"version"`
	TCPPort int `json:"tcp_port"`
}

// Randomize the persisted pool starting point per profile and recheck conflicts on startup.
func selectExitPoolFrom(occupied []netip.Prefix, start uint32) (netip.Prefix, error) {
	for offset := uint32(0); offset < exitPoolChoices; offset++ {
		index := (start + offset) % exitPoolChoices
		pool := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(index/256 + 1), byte(index % 256), 0}), 24)
		if !overlapsAny(pool, occupied) {
			return pool, nil
		}
	}
	return netip.Prefix{}, fmt.Errorf("no non-conflicting private Exit IPv4 /24 pool")
}

func allowedExitPool(pool netip.Prefix) bool {
	if !pool.IsValid() || !pool.Addr().Is4() || pool.Bits() != 24 || pool != pool.Masked() {
		return false
	}
	first := pool.Addr().As4()
	return first[0] == 10 && first[1] >= 1 && first[1] <= 254
}

func overlapsAny(candidate netip.Prefix, occupied []netip.Prefix) bool {
	for _, value := range occupied {
		if value.IsValid() && value.Addr().Is4() &&
			(candidate.Contains(value.Addr()) || value.Contains(candidate.Addr())) {
			return true
		}
	}
	return false
}

func exitTUNAddress(pool netip.Prefix) netip.Prefix {
	ip := pool.Addr().As4()
	ip[3] = 1
	return netip.PrefixFrom(netip.AddrFrom4(ip), pool.Bits())
}
