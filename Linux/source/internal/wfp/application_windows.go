// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import (
	"fmt"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ApplicationID is the opaque native path identity, not a PID, signature or permission to use arbitrary WAN.
type ApplicationID string

func (id ApplicationID) validate() error {
	if len(id) < 4 || len(id) > 65536 || len(id)%2 != 0 || id[len(id)-2:] != "\x00\x00" {
		return fmt.Errorf("invalid WFP application identity encoding")
	}
	for i := 0; i < len(id)-2; i += 2 {
		if id[i:i+2] == "\x00\x00" {
			return fmt.Errorf("embedded terminator in WFP application identity")
		}
	}
	return nil
}

// ApplicationIDForPath delegates canonicalization to Windows; callers must secure the executable path separately.
func ApplicationIDForPath(path string) (ApplicationID, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("WFP application path must be absolute")
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	var b *blob
	err = call("FwpmGetAppIdFromFileName0", uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&b)))
	runtime.KeepAlive(p)
	defer release(b)
	if err != nil {
		return "", err
	}
	if b == nil {
		return "", fmt.Errorf("missing native WFP application identity")
	}
	raw, err := copyBlob(*b)
	if err != nil {
		return "", err
	}
	id := ApplicationID(raw)
	return id, id.validate()
}
