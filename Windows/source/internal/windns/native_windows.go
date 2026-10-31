// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package windns owns journaled per-interface DNS overrides; WFP and routing remain separate prerequisites.
package windns

import (
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"strings"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winthread"
	"golang.org/x/sys/windows"
)

type nativeSettings struct {
	Version                                                                 uint32
	Flags                                                                   uint64
	Domain, NameServer, SearchList                                          *uint16
	RegistrationEnabled, RegisterAdapterName, EnableLLMNR, QueryAdapterName uint32
	ProfileNameServer                                                       *uint16
}

type nativeSettings3 struct {
	nativeSettings
	DisableUnconstrainedQueries uint32
	SupplementalSearchList      *uint16
	ServerPropertyCount         uint32
	ServerProperties            unsafe.Pointer
	ProfilePropertyCount        uint32
	ProfileProperties           unsafe.Pointer
}

// Settings captures configured values, not a DHCP-learned address falsely converted into static DNS.
type Settings struct {
	Servers                                                                 string
	Domain                                                                  string
	SearchList                                                              string
	ProfileServers                                                          string
	SupplementalSearchList                                                  string
	EncryptedServerProperties                                               bool
	RegistrationEnabled, RegisterAdapterName, EnableLLMNR, QueryAdapterName uint32
}

type Snapshot struct {
	Interface  windows.GUID
	IPv4, IPv6 Settings
}

type backend interface {
	Read(windows.GUID) (Snapshot, error)
	SetServers(windows.GUID, bool, string) error
}

type nativeBackend struct{}

var ErrInterfaceGone = errors.New("DNS interface no longer exists")
var iphelper = windows.NewLazySystemDLL("iphlpapi.dll")
var nativeSlot = make(chan struct{}, 1)

// A dedicated native thread releases RPC resources without retiring an unrelated Go I/O owner.
func nativeDNS(fn func() error) error {
	nativeSlot <- struct{}{}
	defer func() { <-nativeSlot }()
	_, err := winthread.Call(func() (struct{}, error) { return struct{}{}, fn() })
	return err
}

//go:uintptrescapes
func nativeCall(name string, args ...uintptr) error {
	proc := iphelper.NewProc(name)
	if err := proc.Find(); err != nil {
		return err
	}
	r, _, _ := proc.Call(args...)
	if r != 0 {
		return fmt.Errorf("%s: %w", name, windows.Errno(r))
	}
	return nil
}

func interfaceGUID(luid uint64) (windows.GUID, error) {
	var id windows.GUID
	if luid == 0 {
		return id, fmt.Errorf("DNS interface LUID is required")
	}
	err := nativeCall("ConvertInterfaceLuidToGuid", uintptr(unsafe.Pointer(&luid)), uintptr(unsafe.Pointer(&id)))
	return id, err
}

func Read(luid uint64) (Snapshot, error) {
	id, err := interfaceGUID(luid)
	if err != nil {
		return Snapshot{}, err
	}
	return (nativeBackend{}).Read(id)
}

func (nativeBackend) Read(id windows.GUID) (Snapshot, error) {
	var result Snapshot
	err := nativeDNS(func() error {
		var err error
		result, err = readNative(id)
		return err
	})
	return result, err
}

func readNative(id windows.GUID) (Snapshot, error) {
	if unsafe.Sizeof(uintptr(0)) != 8 || id == (windows.GUID{}) {
		return Snapshot{}, fmt.Errorf("DNS requires a 64-bit Windows interface identity")
	}
	var luid uint64
	if err := nativeCall("ConvertInterfaceGuidToLuid", uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&luid))); err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_NOT_FOUND) {
			return Snapshot{}, ErrInterfaceGone
		}
		return Snapshot{}, err
	}
	s := Snapshot{Interface: id}
	for _, v6 := range []bool{false, true} {
		n := nativeSettings3{nativeSettings: nativeSettings{Version: 3}}
		// Windows uses this selector for IPv6 reads; the native SDK experiment checks both families independently.
		if v6 {
			n.Flags = 1
		}
		if err := nativeCall("GetInterfaceDnsSettings", uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&n))); err != nil {
			return Snapshot{}, err
		}
		value, err := copySettings(&n.nativeSettings, v6)
		if err == nil {
			value.SupplementalSearchList, err = nativeString(n.SupplementalSearchList)
		}
		value.EncryptedServerProperties = n.ServerPropertyCount != 0 || n.ProfilePropertyCount != 0
		iphelper.NewProc("FreeInterfaceDnsSettings").Call(uintptr(unsafe.Pointer(&n)))
		runtime.KeepAlive(n)
		if err != nil {
			return Snapshot{}, err
		}
		if v6 {
			s.IPv6 = value
		} else {
			s.IPv4 = value
		}
	}
	return s, nil
}

func copySettings(n *nativeSettings, v6 bool) (Settings, error) {
	s := Settings{RegistrationEnabled: n.RegistrationEnabled, RegisterAdapterName: n.RegisterAdapterName, EnableLLMNR: n.EnableLLMNR, QueryAdapterName: n.QueryAdapterName}
	for _, field := range []struct {
		p   *uint16
		dst *string
	}{{n.NameServer, &s.Servers}, {n.Domain, &s.Domain}, {n.SearchList, &s.SearchList}, {n.ProfileNameServer, &s.ProfileServers}} {
		text, err := nativeString(field.p)
		if err != nil {
			return Settings{}, err
		}
		*field.dst = text
	}
	var err error
	s.Servers, err = normalizeServers(s.Servers, v6)
	if err != nil {
		return Settings{}, err
	}
	s.ProfileServers, err = normalizeServers(s.ProfileServers, v6)
	return s, err
}

func nativeString(p *uint16) (string, error) {
	if p == nil {
		return "", nil
	}
	for n := 0; n < 16384; n++ {
		if *(*uint16)(unsafe.Add(unsafe.Pointer(p), 2*n)) == 0 {
			return windows.UTF16ToString(unsafe.Slice(p, n)), nil
		}
	}
	return "", fmt.Errorf("unbounded native DNS string")
}

func normalizeServers(value string, v6 bool) (string, error) {
	if len(value) > 16384 {
		return "", fmt.Errorf("DNS server list too large")
	}
	items := strings.FieldsFunc(value, func(c rune) bool { return c == ',' || c == ' ' || c == '\t' || c == '\r' || c == '\n' })
	if len(items) > 64 {
		return "", fmt.Errorf("too many configured DNS servers")
	}
	for i, item := range items {
		a, err := netip.ParseAddr(item)
		if err != nil || a.Is4In6() || a.Is4() == v6 || a.IsUnspecified() || a.IsMulticast() {
			return "", fmt.Errorf("invalid DNS server address family")
		}
		items[i] = a.String()
	}
	return strings.Join(items, ","), nil
}

func (nativeBackend) SetServers(id windows.GUID, v6 bool, value string) error {
	return nativeDNS(func() error { return setServersNative(id, v6, value) })
}

func setServersNative(id windows.GUID, v6 bool, value string) error {
	if unsafe.Sizeof(uintptr(0)) != 8 || id == (windows.GUID{}) {
		return fmt.Errorf("invalid DNS interface")
	}
	value, err := normalizeServers(value, v6)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return err
	}
	n := nativeSettings{Version: 1, Flags: 2, NameServer: p}
	if v6 {
		n.Flags |= 1
	}
	err = nativeCall("SetInterfaceDnsSettings", uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&n)))
	runtime.KeepAlive(n)
	runtime.KeepAlive(p)
	return err
}
