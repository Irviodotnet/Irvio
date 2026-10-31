// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WriteAtomic installs a flushed private file by handle-relative rename on the same volume.
func (d *Directory) WriteAtomic(name string, payload []byte, replace bool) (resultErr error) {
	if d == nil || d.handle == 0 || !validName(name) {
		return ErrUnsafe
	}
	if replace {
		existing, err := d.OpenFile(name, false, false)
		if err == nil {
			existing.Close()
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	temporary := ".irvio-write-" + hex.EncodeToString(random[:])
	sd, err := privateDescriptor(d.user, false)
	if err != nil {
		return err
	}
	handle, err := openNative(d.handle, temporary, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE,
		windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_WRITE_THROUGH, sd)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), temporary)
	renamed := false
	defer func() {
		if !renamed {
			var status windows.IO_STATUS_BLOCK
			remove := byte(1)
			if err := windows.NtSetInformationFile(handle, &status, &remove, 1, windows.FileDispositionInformation); err != nil {
				resultErr = fmt.Errorf("atomic write failed (%v); temporary cleanup: %w", resultErr, err)
			}
		}
		if err := file.Close(); err != nil {
			resultErr = fmt.Errorf("atomic write failed (%v); close: %w", resultErr, err)
		}
	}()
	if err := validate(handle, d.user, false); err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	encoded, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	type renameInformation struct {
		Flags          uint32
		RootDirectory  windows.Handle
		FileNameLength uint32
		FileName       [1]uint16
	}
	var layout renameInformation
	length := max(int(unsafe.Offsetof(layout.FileName))+2*(len(encoded)-1), int(unsafe.Sizeof(layout)))
	buffer := make([]byte, length)
	info := (*renameInformation)(unsafe.Pointer(&buffer[0]))
	info.RootDirectory = d.handle
	info.FileNameLength = uint32(2 * (len(encoded) - 1))
	if replace {
		info.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS
	}
	copy(unsafe.Slice(&info.FileName[0], len(encoded)-1), encoded[:len(encoded)-1])
	var status windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(handle, &status, &buffer[0], uint32(length), windows.FileRenameInformation)
	if code, ok := err.(windows.NTStatus); ok {
		err = code.Errno()
	}
	if err != nil {
		return err
	}
	renamed = true
	return file.Sync()
}
