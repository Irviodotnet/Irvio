// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package productruntime

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/vishvananda/netlink"
)

const ExitPoolFilename = "exit-pool-v1.json"

type exitPoolRecord struct {
	Version int    `json:"version"`
	IPv4    string `json:"ipv4"`
}

// ResolveExitPool persists a free private /24; later conflicts fail rather than silently renumbering leases.
func ResolveExitPool(dataDir string) (netip.Prefix, netip.Prefix, error) {
	occupied, err := hostIPv4Prefixes()
	if err != nil {
		return netip.Prefix{}, netip.Prefix{}, err
	}
	path := filepath.Join(dataDir, ExitPoolFilename)
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return netip.Prefix{}, netip.Prefix{}, fmt.Errorf("persisted Exit pool record is not a private regular file")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return netip.Prefix{}, netip.Prefix{}, fmt.Errorf("inspect persisted Exit pool: %w", statErr)
	}
	contents, err := os.ReadFile(path)
	if err == nil {
		var record exitPoolRecord
		if len(contents) > 1024 || json.Unmarshal(contents, &record) != nil || record.Version != 1 {
			return netip.Prefix{}, netip.Prefix{}, fmt.Errorf("persisted Exit pool record is invalid")
		}
		pool, parseErr := netip.ParsePrefix(record.IPv4)
		if parseErr != nil || !allowedExitPool(pool) {
			return netip.Prefix{}, netip.Prefix{}, fmt.Errorf("persisted Exit pool is invalid")
		}
		if overlapsAny(pool, occupied) {
			return netip.Prefix{}, netip.Prefix{}, fmt.Errorf("persisted Exit pool %s now conflicts with a host route or address", pool)
		}
		return pool, exitTUNAddress(pool), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return netip.Prefix{}, netip.Prefix{}, fmt.Errorf("read persisted Exit pool: %w", err)
	}
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return netip.Prefix{}, netip.Prefix{}, fmt.Errorf("choose Exit pool: %w", err)
	}
	pool, err := selectExitPoolFrom(occupied, binary.BigEndian.Uint32(random[:]))
	if err != nil {
		return netip.Prefix{}, netip.Prefix{}, err
	}
	value, err := json.Marshal(exitPoolRecord{Version: 1, IPv4: pool.String()})
	if err != nil {
		return netip.Prefix{}, netip.Prefix{}, err
	}
	if err := writeExitPool(path, value); err != nil {
		return netip.Prefix{}, netip.Prefix{}, err
	}
	return pool, exitTUNAddress(pool), nil
}

func hostIPv4Prefixes() ([]netip.Prefix, error) {
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{}, 0)
	if err != nil {
		return nil, fmt.Errorf("list IPv4 routes for Exit pool selection: %w", err)
	}
	result := make([]netip.Prefix, 0, len(routes)+8)
	for _, route := range routes {
		if route.Dst == nil || route.Dst.IP == nil {
			continue
		}
		if ownIrvioLink(route.LinkIndex) {
			continue
		}
		prefix, ok := prefixFromIPNet(route.Dst)
		if ok && prefix.Bits() > 0 {
			result = append(result, prefix)
		}
	}
	addresses, err := netlink.AddrList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil, fmt.Errorf("list IPv4 addresses for Exit pool selection: %w", err)
	}
	for _, address := range addresses {
		if ownIrvioLink(address.LinkIndex) {
			continue
		}
		if prefix, ok := prefixFromIPNet(address.IPNet); ok {
			result = append(result, prefix)
		}
	}
	return result, nil
}

func ownIrvioLink(index int) bool {
	if index <= 0 {
		return false
	}
	link, err := netlink.LinkByIndex(index)
	return err == nil && link.Attrs() != nil && link.Attrs().Name == "hydra0"
}

func prefixFromIPNet(value *net.IPNet) (netip.Prefix, bool) {
	if value == nil {
		return netip.Prefix{}, false
	}
	ip, ok := netip.AddrFromSlice(value.IP)
	ones, bits := value.Mask.Size()
	if !ok || !ip.Is4() || bits != 32 || ones < 0 {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(ip.Unmap(), ones).Masked(), true
}

const exitPoolChoices = 254 * 256

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

func writeExitPool(path string, value []byte) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".exit-pool-v1-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(value); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
