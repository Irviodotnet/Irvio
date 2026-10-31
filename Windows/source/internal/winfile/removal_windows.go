// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Identity identifies a file on a local volume without relying on its pathname.
type Identity struct {
	Volume uint64
	File   [16]byte
}

func (d *Directory) Identity() (Identity, error) {
	var id Identity
	if d == nil || d.handle == 0 {
		return id, ErrUnsafe
	}
	err := windows.GetFileInformationByHandleEx(d.handle, windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id)))
	return id, err
}

// Names enumerates the pinned directory by a duplicated handle with a strict entry bound.
func (d *Directory) Names(limit int) ([]string, error) {
	if d == nil || d.handle == 0 || limit < 1 || limit > 4096 {
		return nil, ErrUnsafe
	}
	var handle windows.Handle
	process := windows.CurrentProcess()
	if err := windows.DuplicateHandle(process, d.handle, process, &handle, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), d.path)
	names, readErr := file.Readdirnames(limit + 1)
	closeErr := file.Close()
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if len(names) > limit {
		return nil, errors.Join(fmt.Errorf("private directory exceeds %d entries", limit), closeErr)
	}
	return names, errors.Join(readErr, closeErr)
}

// Removal pins one validated regular file exclusively until removal or Close.
type Removal struct {
	file   *os.File
	user   string
	parent *Directory
}

func (d *Directory) OpenRemoval(name string) (*Removal, error) {
	return d.openRemoval(name, false)
}

// OpenInheritedRemoval accepts only trusted inherited file ACEs beneath this still-pinned protected directory.
func (d *Directory) OpenInheritedRemoval(name string) (*Removal, error) {
	return d.openRemoval(name, true)
}

func (d *Directory) openRemoval(name string, inherited bool) (*Removal, error) {
	if d == nil || d.handle == 0 || !validName(name) {
		return nil, ErrUnsafe
	}
	if inherited {
		if err := validate(d.handle, d.user, true); err != nil {
			return nil, err
		}
	}
	handle, err := openNativeShared(d.handle, name, windows.FILE_GENERIC_READ|windows.DELETE,
		windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, nil, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open removal", Path: name, Err: err}
	}
	if err := validateRemoval(handle, d.user, inherited); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	r := &Removal{file: os.NewFile(uintptr(handle), name), user: d.user}
	if inherited {
		r.parent = d
	}
	return r, nil
}

func validateRemoval(handle windows.Handle, user string, inherited bool) error {
	if err := validateACL(handle, user, false, inherited); err != nil {
		return err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_READONLY != 0 {
		return ErrUnsafe
	}
	return nil
}

func (r *Removal) Read(limit int64) ([]byte, error) {
	if r == nil || r.file == nil || limit < 0 || limit > 1<<20 {
		return nil, ErrUnsafe
	}
	return io.ReadAll(io.NewSectionReader(r.file, 0, limit))
}

func (r *Removal) Remove() error {
	if r == nil || r.file == nil {
		return ErrUnsafe
	}
	handle := windows.Handle(r.file.Fd())
	if r.parent != nil {
		if r.parent.handle == 0 {
			return ErrUnsafe
		}
		if err := validate(r.parent.handle, r.user, true); err != nil {
			return err
		}
	}
	if err := validateRemoval(handle, r.user, r.parent != nil); err != nil {
		return err
	}
	var status windows.IO_STATUS_BLOCK
	remove := byte(1)
	if err := windows.NtSetInformationFile(handle, &status, &remove, 1, windows.FileDispositionInformation); err != nil {
		if code, ok := err.(windows.NTStatus); ok {
			return code.Errno()
		}
		return err
	}
	return r.Close()
}

func (r *Removal) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	file := r.file
	r.file = nil
	return file.Close()
}
