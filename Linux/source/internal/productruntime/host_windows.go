// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

type WindowsPaths struct {
	Config, Profile, GeoIP, Runtime string
}

// WindowsDefaultPaths uses the system folder API, never a caller's ProgramData environment override.
func WindowsDefaultPaths() (WindowsPaths, error) {
	root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return WindowsPaths{}, fmt.Errorf("resolve Windows product directory: %w", err)
	}
	root = filepath.Join(root, "IRVIO")
	return WindowsPaths{Config: filepath.Join(root, "config.json"), Profile: filepath.Join(root, "profile"),
		GeoIP: filepath.Join(root, "geo", "country.mmdb"), Runtime: filepath.Join(root, "runtime")}, nil
}

func LoadDefaultConfig() (FileConfig, error) {
	paths, err := WindowsDefaultPaths()
	if err != nil {
		return FileConfig{}, err
	}
	return loadWindowsSystemConfig(paths.Config)
}

// Optional service configuration must be owned by SYSTEM/Administrators, not the invoking GUI user.
func loadWindowsSystemConfig(path string) (config FileConfig, resultErr error) {
	if !filepath.IsAbs(path) || filepath.Base(path) != "config.json" {
		return FileConfig{}, errors.New("invalid Windows product config path")
	}
	directory, err := winfile.OpenSystemDirectory(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return FileConfig{Version: 1}, nil
	}
	if err != nil {
		return FileConfig{}, fmt.Errorf("open protected product config directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	file, err := directory.OpenImmutableFile("config.json")
	if errors.Is(err, os.ErrNotExist) {
		return FileConfig{Version: 1}, nil
	}
	if err != nil {
		return FileConfig{}, fmt.Errorf("open protected product config: %w", err)
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, 4097))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return FileConfig{}, err
	}
	if len(contents) > 4096 {
		return FileConfig{}, errors.New("product config exceeds 4096 bytes")
	}
	return parseFileConfig(contents)
}

// ResolveBootstrapUnderlay returns a native path, not proof that the bootstrap is reachable.
func ResolveBootstrapUnderlay(pool []netip.AddrPort, excludedTunnelLUIDs []uint64) (winroute.Path, error) {
	return resolveWindowsBootstrapUnderlay(pool, func(target netip.Addr) (winroute.Path, error) {
		return winroute.ResolveUnderlay(target, 0, excludedTunnelLUIDs)
	})
}

func resolveWindowsBootstrapUnderlay(pool []netip.AddrPort, resolve func(netip.Addr) (winroute.Path, error)) (winroute.Path, error) {
	var failures error
	for _, endpoint := range pool {
		if !endpoint.IsValid() || !endpoint.Addr().Is4() || endpoint.Port() == 0 {
			failures = errors.Join(failures, errors.New("invalid IPv4 bootstrap STUN endpoint"))
			continue
		}
		path, err := resolve(endpoint.Addr())
		if err == nil {
			return path, nil
		}
		failures = errors.Join(failures, err)
	}
	return winroute.Path{}, fmt.Errorf("no usable bootstrap underlay route: %w", errors.Join(errors.New("no eligible kernel path"), failures))
}

func windowsIPv4Routes() ([]windows.MibIpForwardRow2, error) {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_INET, &table); err != nil {
		return nil, fmt.Errorf("read native IPv4 route table: %w", err)
	}
	if table == nil {
		return nil, errors.New("Windows returned no IPv4 route table")
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	return append([]windows.MibIpForwardRow2(nil), table.Rows()...), nil
}

func windowsRoutePrefix(row windows.MibIpForwardRow2) (netip.Prefix, bool) {
	if row.InterfaceLuid == 0 || row.InterfaceIndex == 0 || row.ValidLifetime == 0 ||
		row.DestinationPrefix.Prefix.Family != windows.AF_INET || row.DestinationPrefix.PrefixLength > 32 {
		return netip.Prefix{}, false
	}
	ip := (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.DestinationPrefix.Prefix)).Addr
	prefix := netip.PrefixFrom(netip.AddrFrom4(ip), int(row.DestinationPrefix.PrefixLength)).Masked()
	return prefix, prefix.Bits() > 0
}

// Pool selection conservatively includes other VPNs and owned residue until lifecycle cleanup finishes.
func hostIPv4Prefixes() ([]netip.Prefix, error) {
	routes, err := windowsIPv4Routes()
	if err != nil {
		return nil, err
	}
	var prefixes []netip.Prefix
	for _, row := range routes {
		if prefix, ok := windowsRoutePrefix(row); ok {
			prefixes = append(prefixes, prefix)
		}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("read host interfaces for Exit pool: %w", err)
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("read host addresses for Exit pool: %w", err)
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil {
				return nil, fmt.Errorf("invalid host address for Exit pool: %w", err)
			}
			if prefix.Addr().Is4() {
				prefixes = append(prefixes, prefix.Masked())
			}
		}
	}
	return prefixes, nil
}

// ConnectedBypassPrefixes excludes defaults, gateway routes, loopback and caller-owned tunnel interfaces.
func ConnectedBypassPrefixes(excludedTunnelLUIDs []uint64) ([]netip.Prefix, error) {
	rows, err := windowsIPv4Routes()
	if err != nil {
		return nil, err
	}
	return windowsConnectedPrefixes(rows, excludedTunnelLUIDs), nil
}

func windowsConnectedPrefixes(rows []windows.MibIpForwardRow2, excluded []uint64) []netip.Prefix {
	var result []netip.Prefix
	seen := make(map[netip.Prefix]bool)
	for _, row := range rows {
		prefix, ok := windowsRoutePrefix(row)
		if !ok || row.Loopback != 0 || prefix.Addr().IsLoopback() || prefix.Addr().IsMulticast() ||
			row.NextHop.Family != windows.AF_INET || (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.NextHop)).Addr != [4]byte{} {
			continue
		}
		owned := false
		for _, luid := range excluded {
			owned = owned || luid == row.InterfaceLuid
		}
		if !owned && !seen[prefix] {
			seen[prefix] = true
			result = append(result, prefix)
		}
	}
	return result
}
