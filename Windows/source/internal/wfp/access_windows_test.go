// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNativeUnprivilegedCannotChangePolicy(t *testing.T) {
	user, password := os.Getenv("IRVIO_WFP_PROBE_USER"), os.Getenv("IRVIO_WFP_PROBE_PASSWORD")
	if user == "" {
		t.Skip("requires temporary non-admin lab identity")
	}
	if password == "" {
		t.Fatal("missing ephemeral credential")
	}
	s := nativeStore(t)
	r := nativeRuleFixture(t)
	if err := s.Replace(context.Background(), []Rule{r}); err != nil {
		t.Fatal(err)
	}
	u, err := windows.UTF16PtrFromString(user)
	if err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(password)
	if err != nil {
		t.Fatal("invalid ephemeral credential")
	}
	d, _ := windows.UTF16PtrFromString(".")
	advapi := windows.NewLazySystemDLL("advapi32.dll")
	var token windows.Token
	ok, _, err := advapi.NewProc("LogonUserW").Call(uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(p)), 2, 0, uintptr(unsafe.Pointer(&token)))
	runtime.KeepAlive(u)
	runtime.KeepAlive(d)
	runtime.KeepAlive(p)
	if ok == 0 {
		t.Fatalf("temporary user logon: %v", err)
	}
	defer token.Close()
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups.AllGroups() {
		if g.Sid.String() == "S-1-5-32-544" {
			t.Fatal("test identity belongs to Administrators")
		}
	}
	intended, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	func() {
		runtime.LockOSThread()
		impersonated := false
		defer func() {
			if impersonated {
				if err := windows.RevertToSelf(); err != nil {
					t.Errorf("revert impersonation: %v", err)
					return
				}
			}
			runtime.UnlockOSThread()
		}()
		ok, _, err := advapi.NewProc("ImpersonateLoggedOnUser").Call(uintptr(token))
		if ok == 0 {
			t.Fatalf("impersonation: %v", err)
		}
		impersonated = true
		effective, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
		if err != nil || !windows.EqualSid(effective.User.Sid, intended.User.Sid) {
			t.Fatal("wrong effective test identity")
		}
		h, err := openEngine()
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Log("unprivileged engine open denied")
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		defer call("FwpmEngineClose0", uintptr(h))
		for name, key := range map[string]windows.GUID{"FwpmFilterDeleteByKey0": r.Key, "FwpmSubLayerDeleteByKey0": s.config.Sublayer, "FwpmProviderDeleteByKey0": s.config.Provider} {
			if err := call(name, uintptr(h), uintptr(unsafe.Pointer(&key))); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Errorf("%s: expected access denied, got %v", name, err)
			}
		}
	}()
	assertNativeRules(t, s, []Rule{r})
}
