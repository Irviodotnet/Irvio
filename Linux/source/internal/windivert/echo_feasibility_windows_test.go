// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windivert

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

// This primitive gate does not exercise lease admission or the encrypted backend.
func echoLabFilter(b winroute.Binding, remote netip.Addr, nonce [16]byte) string {
	return (&echoCapture{remote: remote, identifier: 30481, sequence: 1, nonce: nonce}).filter(b)
}

func echoLabPacket(b winroute.Binding, remote netip.Addr, nonce [16]byte) []byte {
	p := make([]byte, 60)
	p[0], p[8], p[9], p[20] = 0x45, 64, 1, 8
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	binary.BigEndian.PutUint16(p[6:8], 0x4000)
	copy(p[12:16], b.Source.AsSlice())
	copy(p[16:20], remote.AsSlice())
	binary.BigEndian.PutUint16(p[24:26], 30481)
	binary.BigEndian.PutUint16(p[26:28], 1)
	copy(p[28:44], nonce[:])
	copy(p[44:], []byte("irvio-echo-probe"))
	binary.BigEndian.PutUint16(p[10:12], icmpTestChecksum(p[:20]))
	binary.BigEndian.PutUint16(p[22:24], icmpTestChecksum(p[20:]))
	return p
}

func echoLabUser(t *testing.T) windows.Token {
	t.Helper()
	user, password := os.Getenv("IRVIO_ECHO_PROBE_USER"), os.Getenv("IRVIO_ECHO_PROBE_PASSWORD")
	if user == "" || password == "" {
		t.Fatal("disposable standard-user credentials missing")
	}
	u, err := windows.UTF16PtrFromString(user)
	if err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(password)
	if err != nil {
		t.Fatal("invalid temporary password")
	}
	d, _ := windows.UTF16PtrFromString(".")
	var token windows.Token
	ok, _, err := windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW").Call(uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(p)), 2, 0, uintptr(unsafe.Pointer(&token)))
	runtime.KeepAlive(u)
	runtime.KeepAlive(p)
	runtime.KeepAlive(d)
	if ok == 0 {
		t.Fatal("temporary user logon", err)
	}
	t.Cleanup(func() {
		if err := token.Close(); err != nil {
			t.Error(err)
		}
	})
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups.AllGroups() {
		if group.Sid.String() == "S-1-5-32-544" {
			t.Fatal("probe must not be an administrator")
		}
	}
	identity, err := token.GetTokenUser()
	if err != nil || identity.User.Sid.String() == "S-1-5-18" {
		t.Fatal("invalid standard-user token", err)
	}
	return token
}

func echoLabOwner() wfp.Config {
	return wfp.Config{Provider: windows.GUID{Data1: 0xe91617c1, Data2: 0x6333, Data3: 0x4bc1, Data4: [8]byte{0x90, 0x42, 0x32, 0x9a, 0, 0, 0, 1}}, Sublayer: windows.GUID{Data1: 0xe91617c2, Data2: 0x6333, Data3: 0x4bc1, Data4: [8]byte{0x90, 0x42, 0x32, 0x9a, 0, 0, 0, 1}}, Name: "IRVIO echo injection lab", Marker: "irvio-echo-injection-v1", ServiceName: "IRVIO-W06-EchoOwner", Weight: 49302}
}

func TestNativeEchoInjectionCleanup(t *testing.T) {
	if os.Getenv("IRVIO_ECHO_INJECTION_LAB") != "1" {
		t.Skip("disposable fixture only")
	}
	store, err := wfp.NewStore(echoLabOwner())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state, err := store.Inspect(context.Background()); err != nil || state.Installed {
		t.Fatal("echo WFP residue", err)
	}
}

func TestNativeEchoForwardInjectionProtected(t *testing.T) {
	if os.Getenv("IRVIO_ECHO_INJECTION_LAB") != "1" {
		t.Skip("requires isolated SYSTEM QEMU fixture")
	}
	identity, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || identity.User.Sid.String() != "S-1-5-18" {
		t.Fatal("SYSTEM required", err)
	}
	m := ioModule(t)
	remote := netip.MustParseAddr("10.0.2.2")
	path, err := winroute.ResolveUnderlay(remote, 0, nil)
	if err != nil || path.Binding.Source.String() != "10.0.2.15" {
		t.Fatal("wrong disposable QEMU path", err)
	}
	token := echoLabUser(t)
	probe := os.Getenv("IRVIO_ECHO_PROBE_BINARY")
	if probe == "" {
		t.Fatal("native ordinary-user echo probe missing")
	}
	ordinary := func(want bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, probe, "-test.v", "-test.run=^TestNativeEchoStandardUserChild$", "-test.timeout=4s")
		cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "IRVIO_ECHO_STANDARD_CHILD=1"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(token)}
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil || (err == nil) != want || (!want && !bytes.Contains(out, []byte("ECHO_BLOCKED:"))) {
			t.Fatalf("ordinary echo want=%v err=%v output=%s", want, err, out)
		}
		t.Logf("ordinary-user echo allowed=%v", want)
	}
	ordinary(true)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	capture, err := m.OpenEchoReplyCapture(path.Binding, remote, 30481, 1, nonce)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := capture.Close(); err != nil {
			t.Error(err)
		}
	})
	sender, err := m.OpenForwardSender(path.Binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sender.Close(); err != nil {
			t.Error(err)
		}
	})
	packet := echoLabPacket(path.Binding, remote, nonce)
	injected := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := sender.SendIPv4(ctx, packet); err != nil {
			t.Fatal("send injected echo", err)
		}
		r, err := capture.ReceiveIPv4(ctx)
		if err != nil {
			t.Fatal("receive injected echo", err)
		}
		if len(r) != len(packet) || r[0] != 0x45 || r[9] != 1 || r[20] != 0 || r[21] != 0 ||
			!bytes.Equal(r[24:], packet[24:]) || !bytes.Equal(r[12:16], remote.AsSlice()) || !bytes.Equal(r[16:20], path.Binding.Source.AsSlice()) ||
			icmpTestChecksum(r[:20]) != 0 || icmpTestChecksum(r[20:]) != 0 {
			t.Fatal("invalid echo response or physical path")
		}
	}
	injected()
	ordinary(true)
	store, err := wfp.NewStore(echoLabOwner())
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Inspect(context.Background())
	if err != nil || prior.Installed {
		t.Fatal("existing lab WFP owner", err)
	}
	t.Cleanup(func() {
		if err := store.Remove(context.Background()); err != nil {
			t.Error(err)
		}
		if state, err := store.Inspect(context.Background()); err != nil || state.Installed {
			t.Error("lab WFP residue", err)
		}
	})
	var rules []wfp.Rule
	for i, layer := range []windows.GUID{wfp.ConnectV4, wfp.TransportV4} {
		rules = append(rules, wfp.Rule{Key: windows.GUID{Data1: uint32(201 + i)}, Layer: layer, Action: wfp.Block, Remote: netip.PrefixFrom(remote, 32), Protocol: 1})
	}
	if err := store.Replace(context.Background(), rules); err != nil {
		t.Fatal(err)
	}
	ordinary(false)
	for i := 0; i < 10; i++ {
		injected()
	}
	ordinary(false)
	if err := store.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	ordinary(true)
	policy := killswitch.Policy{TableName: "echo_lab", TUNName: "echo_lab", BypassMark: 12345}
	backend, err := killswitch.NewWindowsBackend(killswitch.WindowsBackendConfig{Policy: policy, Owner: echoLabOwner()})
	if err != nil {
		t.Fatal(err)
	}
	installation, _, err := backend.Ensure(context.Background(), policy)
	if err != nil {
		t.Fatal("production protection install", err)
	}
	ordinary(false)
	for i := 0; i < 10; i++ {
		injected()
	}
	ordinary(false)
	if err := installation.Remove(context.Background()); err != nil {
		t.Fatal("production protection remove", err)
	}
	ordinary(true)
	t.Log("forwarded echo passes with target deny and full production NO_EXIT protection, without ICMP permit; ordinary-user echo stays denied and cleanup restores it")
}

func TestNativeEchoNonceFilter(t *testing.T) {
	m := ioModule(t)
	eval, err := m.dll.FindProc("WinDivertHelperEvalFilter")
	if err != nil {
		t.Fatal(err)
	}
	binding := winroute.Binding{Index: 9, Source: netip.MustParseAddr("203.0.113.9")}
	remote := netip.MustParseAddr("198.51.100.7")
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	filter := append([]byte(echoLabFilter(binding, remote, nonce)), 0)
	for _, variant := range []string{"match", "nonce", "sequence", "id", "source", "recipient", "type", "code", "short", "interface", "outbound", "protocol", "fragment", "more-fragments", "options"} {
		t.Run(variant, func(t *testing.T) {
			p := echoLabPacket(binding, remote, nonce)
			copy(p[12:16], remote.AsSlice())
			copy(p[16:20], binding.Source.AsSlice())
			p[20] = 0
			a := nativeAddress{IfIndex: binding.Index}
			switch variant {
			case "nonce":
				p[35] ^= 1
			case "sequence":
				p[27] ^= 1
			case "id":
				p[25] ^= 1
			case "source":
				p[15] ^= 1
			case "recipient":
				p[19] ^= 1
			case "type":
				p[20] = 8
			case "code":
				p[21] = 1
			case "short":
				p = p[:43]
				binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
			case "interface":
				a.IfIndex++
			case "outbound":
				a.Flags = 1 << 17
			case "protocol":
				p[9] = 17
			case "fragment":
				p[7] = 1
			case "more-fragments":
				p[6] |= 0x20
			case "options":
				p[0] = 0x46
			}
			ok, _, err := eval.Call(uintptr(unsafe.Pointer(&filter[0])), uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unsafe.Pointer(&a)))
			runtime.KeepAlive(filter)
			runtime.KeepAlive(p)
			runtime.KeepAlive(a)
			if err != syscall.Errno(0) || (ok != 0) != (variant == "match") {
				t.Fatal("filter mismatch", ok, err)
			}
		})
	}
}
