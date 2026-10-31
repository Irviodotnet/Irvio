// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

// NewInbound captures one exclusively reserved physical TCP/UDP endpoint; it does not bypass ProtectedMode.
func NewInbound(owner windows.GUID, luid uint64, endpoint netip.AddrPort, protocol uint32) (*Control, error) {
	if runtime.GOARCH != "amd64" || owner == (windows.GUID{}) || luid == 0 || !validInboundEndpoint(endpoint, protocol) {
		return nil, fmt.Errorf("inbound firewall requires amd64, owner, physical interface and exact TCP/UDP endpoint")
	}
	row := windows.MibIfRow2{InterfaceLuid: luid}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, err
	}
	c := &Control{owner: owner, name: "IRVIO.Inbound." + owner.String(), luid: luid, guid: row.InterfaceGuid,
		application: exe, iface: windows.UTF16ToString(row.Alias[:]), endpoint: endpoint, physical: true, protocol: protocol,
		description: "IRVIO physical inbound v1; interface=" + row.InterfaceGuid.String()}
	if err := c.validateTunnel(); err != nil {
		return nil, err
	}
	return c, nil
}

func validInboundEndpoint(endpoint netip.AddrPort, protocol uint32) bool {
	a := endpoint.Addr()
	return (protocol == 6 || protocol == 17) && endpoint.Port() != 0 && a.IsGlobalUnicast() && !a.Is4In6() && a.Zone() == ""
}

// OpenOwnedInbound journals an exact physical exception before installation; each socket needs its own directory.
func OpenOwnedInbound(directory string, luid uint64, endpoint netip.AddrPort, protocol uint32) (*OwnedControl, error) {
	return openOwnedRule(directory, func(id windows.GUID) (*Control, error) {
		return NewInbound(id, luid, endpoint, protocol)
	})
}
