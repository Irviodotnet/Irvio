// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build windows

// Package winfile protects service-owned state with native Windows handles and ACLs.
package winfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ErrUnsafe = errors.New("unsafe Windows private state")

// Directory pins a non-reparse directory; callers must close all files before it.
type Directory struct {
	handle windows.Handle
	path   string
	user   string
}

// OpenDirectory creates only the final directory and never repairs an existing ACL.
func OpenDirectory(path string, create bool) (*Directory, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || path == "" {
		return nil, ErrUnsafe
	}
	volume := filepath.VolumeName(absolute)
	if len(volume) != 2 || volume[1] != ':' || len(absolute) <= 3 || strings.Contains(absolute[2:], ":") {
		return nil, ErrUnsafe
	}
	for _, component := range strings.Split(absolute[3:], `\`) {
		if component == "" || strings.TrimRight(component, ". ") != component {
			return nil, ErrUnsafe
		}
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	user := tokenUser.User.Sid.String()
	sd, err := privateDescriptor(user, true)
	if err != nil {
		return nil, err
	}
	disposition := uint32(windows.FILE_OPEN)
	if create {
		disposition = windows.FILE_OPEN_IF
	}
	handle, err := openNative(0, `\??\`+absolute, windows.FILE_GENERIC_READ, disposition,
		windows.FILE_DIRECTORY_FILE, sd)
	if err != nil {
		return nil, &os.PathError{Op: "open private directory", Path: absolute, Err: err}
	}
	if err := validate(handle, user, true); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return &Directory{handle: handle, path: absolute, user: user}, nil
}

func (d *Directory) Close() error {
	if d == nil || d.handle == 0 {
		return nil
	}
	handle := d.handle
	d.handle = 0
	return windows.CloseHandle(handle)
}

// OpenSystemDirectory accepts an existing protected directory owned only by SYSTEM or Administrators.
func OpenSystemDirectory(path string) (*Directory, error) {
	d, err := OpenDirectory(path, false)
	if err != nil {
		return nil, err
	}
	if err := validate(d.handle, "S-1-5-18", true); err != nil {
		return nil, errors.Join(err, d.Close())
	}
	d.user = "S-1-5-18"
	return d, nil
}

// OpenImmutableFile denies concurrent writers and replacement for the lifetime of the returned handle.
func (d *Directory) OpenImmutableFile(name string) (*os.File, error) {
	if d == nil || d.handle == 0 || !validName(name) {
		return nil, ErrUnsafe
	}
	handle, err := openNativeShared(d.handle, name, windows.FILE_GENERIC_READ, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE, nil, windows.FILE_SHARE_READ)
	if err != nil {
		return nil, err
	}
	if err := validate(handle, d.user, false); err != nil {
		return nil, errors.Join(err, windows.CloseHandle(handle))
	}
	return os.NewFile(uintptr(handle), filepath.Join(d.path, name)), nil
}

// OpenFile opens a single relative filename without following links or truncating it.
func (d *Directory) OpenFile(name string, writable, create bool) (*os.File, error) {
	if d == nil || d.handle == 0 || !validName(name) || (create && !writable) {
		return nil, ErrUnsafe
	}
	sd, err := privateDescriptor(d.user, false)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.FILE_GENERIC_READ)
	options := uint32(windows.FILE_NON_DIRECTORY_FILE)
	if writable {
		access |= windows.FILE_GENERIC_WRITE
		options |= windows.FILE_WRITE_THROUGH
	}
	disposition := uint32(windows.FILE_OPEN)
	if create {
		disposition = windows.FILE_OPEN_IF
	}
	handle, err := openNative(d.handle, name, access, disposition, options, sd)
	if err != nil {
		return nil, &os.PathError{Op: "open private file", Path: name, Err: err}
	}
	if err := validate(handle, d.user, false); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), filepath.Join(d.path, name)), nil
}

func Lock(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 0xffffffff, 0xffffffff, &windows.Overlapped{})
}

func Unlock(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 0xffffffff, 0xffffffff, &windows.Overlapped{})
}

func openNative(root windows.Handle, name string, access, disposition, options uint32, sd *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	return openNativeShared(root, name, access, disposition, options, sd, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
}

func openNativeShared(root windows.Handle, name string, access, disposition, options uint32, sd *windows.SECURITY_DESCRIPTOR, share uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := windows.OBJECT_ATTRIBUTES{
		RootDirectory: root, ObjectName: objectName, SecurityDescriptor: sd,
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, access, &oa, &status, nil, windows.FILE_ATTRIBUTE_NORMAL,
		share, disposition,
		options|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if code, ok := err.(windows.NTStatus); ok {
		err = code.Errno()
	}
	return handle, err
}

func privateDescriptor(user string, directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	return windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;%s;FA;;;SY)(A;%s;FA;;;BA)(A;%s;FA;;;%s)", user, inherit, inherit, inherit, user))
}

func validate(handle windows.Handle, user string, directory bool) error {
	return validateACL(handle, user, directory, false)
}

func validateACL(handle windows.Handle, user string, directory, inheritedFile bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		(info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory ||
		(!directory && info.NumberOfLinks != 1) {
		return ErrUnsafe
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	trusted := func(sid *windows.SID) bool {
		if sid == nil || !sid.IsValid() {
			return false
		}
		value := sid.String()
		return value == user || value == "S-1-5-18" || value == "S-1-5-32-544"
	}
	owner, _, err := sd.Owner()
	if err != nil || !trusted(owner) {
		return ErrUnsafe
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 && (!inheritedFile || directory) {
		return ErrUnsafe
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return ErrUnsafe
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !trusted((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return ErrUnsafe
		}
		if control&windows.SE_DACL_PROTECTED == 0 && ace.Header.AceFlags&windows.INHERITED_ACE == 0 {
			return ErrUnsafe
		}
	}
	return nil
}

func validName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.TrimRight(name, ". ") != name {
		return false
	}
	for _, ch := range name {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.' {
			continue
		}
		return false
	}
	return true
}
