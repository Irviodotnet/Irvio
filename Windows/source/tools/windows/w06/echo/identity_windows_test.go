// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package echo

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func assertStandardToken(t *testing.T, token windows.Token) {
	t.Helper()
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups.AllGroups() {
		if group.Sid.String() == "S-1-5-32-544" {
			t.Fatal("probe token contains Administrators")
		}
	}
	u, err := token.GetTokenUser()
	if err != nil || u.User.Sid.String() == "S-1-5-18" {
		t.Fatal("not a standard user token", err)
	}
	t.Logf("standard process identity=%s", u.User.Sid.String())
}

func TestNativeEchoStandardUserChild(t *testing.T) {
	if os.Getenv("IRVIO_ECHO_STANDARD_CHILD") != "1" {
		t.Skip("launched only by the identity isolation lab")
	}
	assertStandardToken(t, windows.GetCurrentProcessToken())
	_, err := echoRequest(netip.MustParseAddr("10.0.2.15"), netip.MustParseAddr("10.0.2.2"), []byte("irvio-standard-user-echo"))
	if err != nil {
		t.Fatalf("ECHO_BLOCKED: %v", err)
	}
	t.Log("ECHO_SUCCEEDED")
}

func TestNativeEchoUserIdentityCannotIsolate(t *testing.T) {
	if os.Getenv("IRVIO_ECHO_IDENTITY_LAB") != "1" {
		t.Skip("requires isolated SYSTEM QEMU VM and disposable standard user")
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || u.User.Sid.String() != "S-1-5-18" {
		t.Fatal("parent must be SYSTEM", err)
	}
	user, password := os.Getenv("IRVIO_ECHO_PROBE_USER"), os.Getenv("IRVIO_ECHO_PROBE_PASSWORD")
	if user == "" || password == "" {
		t.Fatal("missing temporary standard-user credentials")
	}
	up, err := windows.UTF16PtrFromString(user)
	if err != nil {
		t.Fatal(err)
	}
	pp, err := windows.UTF16PtrFromString(password)
	if err != nil {
		t.Fatal("invalid temporary credential")
	}
	domain, _ := windows.UTF16PtrFromString(".")
	var token windows.Token
	ok, _, err := windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW").Call(uintptr(unsafe.Pointer(up)), uintptr(unsafe.Pointer(domain)), uintptr(unsafe.Pointer(pp)), 2, 0, uintptr(unsafe.Pointer(&token)))
	runtime.KeepAlive(up)
	runtime.KeepAlive(pp)
	runtime.KeepAlive(domain)
	if ok == 0 {
		t.Fatal("temporary user logon", err)
	}
	defer token.Close()
	assertStandardToken(t, token)
	target := netip.MustParseAddr("10.0.2.2")
	path, err := winroute.ResolveUnderlay(target, 0, nil)
	if err != nil || path.Binding.Source.String() != "10.0.2.15" {
		t.Fatal("requires isolated QEMU path", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "-test.v", "-test.run=^TestNativeEchoStandardUserChild$", "-test.timeout=5s")
		cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "IRVIO_ECHO_STANDARD_CHILD=1"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(token)}
		out, err := cmd.CombinedOutput()
		t.Logf("standard-user child:\n%s", out)
		if err == nil {
			return true, nil
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if !bytes.Contains(out, []byte("ECHO_BLOCKED:")) {
			return false, fmt.Errorf("child failed outside echo assertion: %w", err)
		}
		return false, nil
	}
	systemEcho := func() error {
		_, err := echoRequest(path.Binding.Source, target, []byte("irvio-system-echo"))
		return err
	}
	if err := systemEcho(); err != nil {
		t.Fatal("SYSTEM preflight", err)
	}
	if allowed, err := child(); err != nil || !allowed {
		t.Fatal("standard-user preflight", allowed, err)
	}
	store, err := wfp.NewStore(wfp.Config{Provider: windows.GUID{Data1: 0xe91617b1, Data2: 0x6333, Data3: 0x4bc1, Data4: [8]byte{0x90, 0x42, 0x32, 0x9a, 0, 0, 0, 1}}, Sublayer: windows.GUID{Data1: 0xe91617b2, Data2: 0x6333, Data3: 0x4bc1, Data4: [8]byte{0x90, 0x42, 0x32, 0x9a, 0, 0, 0, 1}}, Name: "IRVIO echo identity lab", Marker: "irvio-echo-identity-v1", ServiceName: "IRVIO-W06-EchoOwner", Weight: 49301})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Inspect(context.Background())
	if err != nil || prior.Installed {
		t.Fatal("existing lab owner", err)
	}
	t.Cleanup(func() {
		if err := store.Remove(context.Background()); err != nil {
			t.Error(err)
		}
		if state, err := store.Inspect(context.Background()); err != nil || state.Installed {
			t.Error("lab WFP residue", err)
		}
	})
	apply := func(permit bool) {
		var rules []wfp.Rule
		for i, layer := range []windows.GUID{wfp.ConnectV4, wfp.TransportV4} {
			rules = append(rules, wfp.Rule{Key: windows.GUID{Data1: uint32(101 + i*2)}, Layer: layer, Action: wfp.Block, Remote: netip.PrefixFrom(target, 32), Protocol: 1})
			if permit {
				r := wfp.Rule{Key: windows.GUID{Data1: uint32(102 + i*2)}, Layer: layer, Action: wfp.Permit, Priority: 100, Remote: netip.PrefixFrom(target, 32), LocalAddress: path.Binding.Source, LocalInterface: path.Binding.LUID, Protocol: 1, ICMP: true, ICMPType: 8}
				if layer == wfp.ConnectV4 {
					r.User = wfp.SystemOrAdministrator
				}
				rules = append(rules, r)
			}
		}
		if err := store.Replace(context.Background(), rules); err != nil {
			t.Fatal(err)
		}
	}
	apply(false)
	if err := systemEcho(); err == nil {
		t.Fatal("SYSTEM echo bypassed deny-only control")
	}
	if allowed, err := child(); err != nil || allowed {
		t.Fatal("standard echo bypassed deny-only control", allowed, err)
	}
	apply(true)
	if err := systemEcho(); err != nil {
		t.Fatal("SYSTEM-only candidate failed", err)
	}
	if allowed, err := child(); err != nil || !allowed {
		t.Fatal("native API behavior changed; reassess identity feasibility", allowed, err)
	}
	apply(false)
	if allowed, err := child(); err != nil || allowed {
		t.Fatal("withdrawn grant still permits standard echo", allowed, err)
	}
	t.Log("CANDIDATE REJECTED: privileged-only ICMP permit also authorizes standard-user echo; no production integration")
}
