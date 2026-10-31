// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfile

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestUnprivilegedServiceACL(t *testing.T) {
	user := os.Getenv("IRVIO_ACL_PROBE_USER")
	if user == "" {
		return
	}
	password := os.Getenv("IRVIO_ACL_PROBE_PASSWORD")
	path := os.Getenv("IRVIO_ACL_PROBE_PATH")
	if password == "" || path == "" {
		t.Fatal("ACL probe inputs missing")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("ACL fixture must exist before impersonation")
	}
	user16, err := windows.UTF16PtrFromString(user)
	if err != nil {
		t.Fatal(err)
	}
	password16, err := windows.UTF16PtrFromString(password)
	if err != nil {
		t.Fatal("invalid probe credential")
	}
	domain16, _ := windows.UTF16PtrFromString(".")
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	dll := windows.NewLazySystemDLL("advapi32.dll")
	var token windows.Token
	result, _, err := dll.NewProc("LogonUserW").Call(uintptr(unsafe.Pointer(user16)), uintptr(unsafe.Pointer(domain16)),
		uintptr(unsafe.Pointer(password16)), 2, 0, uintptr(unsafe.Pointer(&token)))
	if result == 0 {
		t.Fatalf("probe logon failed: %v", err)
	}
	defer token.Close()
	intended, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups.AllGroups() {
		if group.Sid.String() == "S-1-5-32-544" {
			t.Fatal("probe token contains Administrators membership")
		}
	}
	runtime.LockOSThread()
	impersonated := false
	defer func() {
		if impersonated {
			if err := windows.RevertToSelf(); err != nil {
				t.Errorf("revert probe impersonation: %v", err)
				return
			}
		}
		runtime.UnlockOSThread()
	}()
	result, _, err = dll.NewProc("ImpersonateLoggedOnUser").Call(uintptr(token))
	if result == 0 {
		t.Fatalf("probe impersonation failed: %v", err)
	}
	impersonated = true
	effective, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil || !windows.EqualSid(effective.User.Sid, intended.User.Sid) {
		t.Fatalf("probe identity not adopted: %v", err)
	}
	for _, access := range []uint32{windows.GENERIC_READ, windows.GENERIC_WRITE, windows.DELETE} {
		handle, err := windows.CreateFile(path16, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			windows.CloseHandle(handle)
		}
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Errorf("unprivileged access %x: expected access denied, got %v", access, err)
		}
	}
	if !t.Failed() {
		t.Log("real non-admin token: read denied, write denied, delete denied")
	}
}
