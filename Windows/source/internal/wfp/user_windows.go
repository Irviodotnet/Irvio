// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import (
	"bytes"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// UserMatch exposes only the account restrictions needed by privileged control and DHCP maintenance.
type UserMatch uint8

const (
	AnyUser UserMatch = iota
	SystemOrAdministrator
	DHCPService
)

var userDescriptors = sync.OnceValues(func() (map[UserMatch][]byte, error) {
	sid, _, _, err := windows.LookupSID("", `NT SERVICE\Dhcp`)
	if err != nil {
		return nil, fmt.Errorf("resolve DHCP service identity: %w", err)
	}
	result := make(map[UserMatch][]byte)
	for kind, sddl := range map[UserMatch]string{
		SystemOrAdministrator: "D:(A;;0x1;;;SY)(A;;0x1;;;BA)",
		DHCPService:           "D:(A;;0x1;;;" + sid.String() + ")",
	} {
		sd, err := windows.SecurityDescriptorFromString(sddl)
		if err != nil {
			return nil, err
		}
		result[kind] = bytes.Clone(unsafe.Slice((*byte)(unsafe.Pointer(sd)), int(sd.Length())))
	}
	return result, nil
})

func (u UserMatch) descriptor() ([]byte, error) {
	if u != SystemOrAdministrator && u != DHCPService {
		return nil, fmt.Errorf("unsupported WFP user restriction")
	}
	descriptors, err := userDescriptors()
	if err != nil {
		return nil, err
	}
	return descriptors[u], nil
}

func readUserDescriptor(b []byte) (UserMatch, error) {
	descriptors, err := userDescriptors()
	if err != nil {
		return AnyUser, err
	}
	for kind, descriptor := range descriptors {
		if bytes.Equal(b, descriptor) {
			return kind, nil
		}
	}
	return AnyUser, fmt.Errorf("unsupported WFP user security descriptor")
}
