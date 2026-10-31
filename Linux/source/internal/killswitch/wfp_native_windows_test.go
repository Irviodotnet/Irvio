// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func nativeProtectionConfig(t *testing.T) WindowsBackendConfig {
	t.Helper()
	c := windowsFixture()
	c.Owner.Provider = windows.GUID{Data1: 0xd6e22314, Data2: 0xe698, Data3: 0x4ed9, Data4: [8]byte{0x90, 0x35, 0x83, 0x15, 0x3a, 0x1a, 0x66, 0x17}}
	c.Owner.Sublayer = windows.GUID{Data1: 0xab6e4d92, Data2: 0x1176, Data3: 0x4edf, Data4: [8]byte{0x9a, 0x5c, 0x76, 0xde, 0x38, 0x2e, 0x1c, 0x53}}
	c.Owner.Marker = "irvio-w05-protection-v1"
	c.Owner.Name = "IRVIO W05 full protection lab"
	c.Network = WindowsNetwork{}
	return c
}

func nativeProbeUser(t *testing.T) (windows.Token, string) {
	t.Helper()
	name, password := os.Getenv("IRVIO_WFP_PROBE_USER"), os.Getenv("IRVIO_WFP_PROBE_PASSWORD")
	if name == "" || password == "" {
		t.Fatal("requires ephemeral non-admin probe identity")
	}
	u, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal("invalid probe name")
	}
	p, err := windows.UTF16PtrFromString(password)
	if err != nil {
		t.Fatal("invalid ephemeral credential")
	}
	d, _ := windows.UTF16PtrFromString(".")
	var token windows.Token
	ok, _, err := windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW").Call(uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(p)), 2, 0, uintptr(unsafe.Pointer(&token)))
	runtime.KeepAlive(u)
	runtime.KeepAlive(p)
	runtime.KeepAlive(d)
	if ok == 0 {
		t.Fatalf("probe logon: %v", err)
	}
	user, err := token.GetTokenUser()
	if err != nil {
		token.Close()
		t.Fatal(err)
	}
	return token, user.User.Sid.String()
}

func slicesWithoutProbeCredential(env []string) []string {
	result := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(strings.ToUpper(e), "IRVIO_WFP_PROBE_PASSWORD=") {
			result = append(result, e)
		}
	}
	return result
}

func TestWindowsNativeProtectionCleanup(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_PROTECTION_CLEANUP") != "1" {
		t.Skip("explicit lab cleanup only")
	}
	c := nativeProtectionConfig(t)
	s, err := wfp.NewStore(c.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := s.Inspect(context.Background())
	if err != nil || after.Installed {
		t.Fatalf("cleanup %+v %v", after, err)
	}
}

func TestWindowsNativeProtectionCrash(t *testing.T) {
	mode := os.Getenv("IRVIO_WINDOWS_PROTECTION_CRASH")
	if mode != "hold" && mode != "adopt" {
		t.Skip("explicit disposable VM process-kill gate")
	}
	c := nativeProtectionConfig(t)
	ctx := context.Background()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c.Network.Application, err = wfp.ApplicationIDForPath(self)
	if err != nil {
		t.Fatal(err)
	}
	path, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range []uint8{6, 17} {
		c.Network.Underlay = append(c.Network.Underlay, WindowsBypass{Interface: path.Binding.LUID, Source: path.Binding.Source, Remote: netip.AddrPortFrom(netip.MustParseAddr("192.168.23.2"), uint16(49494+i)), Protocol: p})
	}
	b, err := NewWindowsBackend(c)
	if err != nil {
		t.Fatal(err)
	}
	before, err := b.Inspect(ctx, c.Policy)
	if err != nil || before.Installed != (mode == "adopt") {
		t.Fatalf("unexpected crash fixture %+v %v", before, err)
	}
	controller, err := New(Config{Backend: b, Policy: c.Policy})
	if err != nil {
		t.Fatal(err)
	}
	gen, err := controller.Protect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Transition(gen, StateNoExit); err != nil {
		t.Fatal(err)
	}
	if mode == "hold" {
		fmt.Println("PROTECTION_HOLD_READY")
		for {
			time.Sleep(time.Hour)
		}
	}
	if !controller.Snapshot().Adopted {
		t.Fatal("crash policy was not adopted")
	}
	if err := controller.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := b.Inspect(ctx, c.Policy)
	if err != nil || after.Installed {
		t.Fatalf("adoption cleanup %+v %v", after, err)
	}
}

func TestWindowsWANProbeHelper(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_WAN_HELPER") != "1" {
		t.Skip("isolated subprocess helper")
	}
	if want := os.Getenv("IRVIO_WINDOWS_WAN_SID"); want != "" {
		token := windows.GetCurrentProcessToken()
		user, err := token.GetTokenUser()
		if err != nil || user.User.Sid.String() != want {
			t.Fatal("unexpected process identity")
		}
		groups, err := token.GetTokenGroups()
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range groups.AllGroups() {
			if g.Sid.String() == "S-1-5-32-544" {
				t.Fatal("probe identity is an administrator")
			}
		}
		fmt.Println("WAN_NONADMIN_VERIFIED")
	}
	fmt.Println("WAN_HELPER_READY")
	results := map[string]bool{}
	for _, proto := range []string{"tcp", "udp"} {
		port := "49494"
		if proto == "udp" {
			port = "49495"
		}
		conn, err := net.DialTimeout(proto+"4", "192.168.23.2:"+port, 1500*time.Millisecond)
		ok := false
		if err == nil {
			_ = conn.SetDeadline(time.Now().Add(1500 * time.Millisecond))
			msg := fmt.Sprintf("irvio-nat-wfp-backend-%s-%d\n", os.Getenv("IRVIO_WINDOWS_WAN_STAGE"), os.Getpid())
			if _, err = io.WriteString(conn, msg); err == nil {
				buf := make([]byte, len(msg))
				_, err = io.ReadFull(conn, buf)
				ok = err == nil && string(buf) == msg
			}
			conn.Close()
		}
		results[proto] = ok
	}
	b, _ := json.Marshal(results)
	fmt.Println("WAN_RESULT " + string(b))
}

func TestWindowsNativeProtectedController(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_PROTECTION_TEST") != "1" {
		t.Skip("requires disposable elevated VM, Guest Agent and independent mini-PC oracle")
	}
	c := nativeProtectionConfig(t)
	ctx := context.Background()
	s, err := wfp.NewStore(c.Owner)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := s.Inspect(ctx)
	if err != nil || prior.Installed {
		t.Fatalf("existing native protection fixture: %+v %v", prior, err)
	}
	t.Cleanup(func() {
		if err := s.Remove(ctx); err != nil {
			t.Error(err)
		}
	})
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(filepath.Dir(self), "foreign-protected.test.exe")
	selfID, err := wfp.ApplicationIDForPath(self)
	if err != nil {
		t.Fatal(err)
	}
	foreignID, err := wfp.ApplicationIDForPath(foreign)
	if err != nil || foreignID == selfID {
		t.Fatal("distinct executable fixture required")
	}
	userToken, userSID := nativeProbeUser(t)
	defer userToken.Close()
	var probeToken syscall.Token
	probe := func(exe, stage string, want bool) {
		t.Helper()
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		started := time.Now()
		command := exec.CommandContext(cctx, exe, "-test.run=^TestWindowsWANProbeHelper$")
		command.Env = append(os.Environ(), "IRVIO_WINDOWS_WAN_HELPER=1", "IRVIO_WINDOWS_WAN_STAGE="+stage)
		command.Env = slicesWithoutProbeCredential(command.Env)
		if probeToken != 0 {
			command.SysProcAttr = &syscall.SysProcAttr{Token: probeToken}
			command.Env = append(command.Env, "IRVIO_WINDOWS_WAN_SID="+userSID)
		}
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("WAN subprocess: %v context=%v elapsed=%s ready=%v output=%s", err, cctx.Err(), time.Since(started), strings.Contains(string(out), "WAN_HELPER_READY"), out)
		}
		if probeToken != 0 && !strings.Contains(string(out), "WAN_NONADMIN_VERIFIED") {
			t.Fatal("non-admin identity proof missing")
		}
		var result map[string]bool
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "WAN_RESULT ") {
				if err = json.Unmarshal([]byte(strings.TrimPrefix(line, "WAN_RESULT ")), &result); err != nil {
					t.Fatal(err)
				}
			}
		}
		if len(result) != 2 || result["tcp"] != want || result["udp"] != want {
			t.Fatalf("%s: %v expected %v", stage, result, want)
		}
		t.Logf("%s TCP/UDP=%v subprocess_elapsed=%s", stage, want, time.Since(started))
	}
	probe(self, "baseline-self", true)
	probe(foreign, "baseline-foreign", true)
	probeToken = syscall.Token(userToken)
	probe(self, "baseline-same-app-nonadmin", true)
	probeToken = 0
	path, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Network.Application = selfID
	for i, p := range []uint8{6, 17} {
		c.Network.Underlay = append(c.Network.Underlay, WindowsBypass{Interface: path.Binding.LUID, Source: path.Binding.Source, Remote: netip.AddrPortFrom(netip.MustParseAddr("192.168.23.2"), uint16(49494+i)), Protocol: p})
	}
	b, err := NewWindowsBackend(c)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := New(Config{Backend: b, Policy: c.Policy})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := controller.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	gen, err := controller.Protect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	probe(self, "protected-control", true)
	probe(foreign, "protected-ordinary", false)
	probeToken = syscall.Token(userToken)
	probe(self, "protected-same-app-nonadmin", false)
	probeToken = 0
	updater, err := b.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	listener.Close()
	if err != nil {
		t.Fatal("loopback blocked: ", err)
	}
	conn.Close()
	var owner *tunnel.Windows
	if err := controller.Activate(ctx, gen, func(context.Context) (io.Closer, error) {
		var e error
		owner, e = tunnel.NewWindows(tunnel.Config{Name: "IRVIO-W05-TUN", Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.244.1/24"), netip.MustParsePrefix("fd77:244::1/64")}})
		if e != nil {
			return nil, e
		}
		n := c.Network
		n.TunnelLUID = owner.LUID()
		if e = updater.Update(ctx, n); e != nil {
			owner.Close()
			return nil, e
		}
		return closeFunction(func() error {
			snap, e := s.Inspect(ctx)
			if e != nil || !snap.Active {
				return fmt.Errorf("protection absent before TUN close: %v", e)
			}
			return owner.Close()
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	borrowed, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var mu sync.Mutex
	seen := map[string]bool{}
	go func() {
		defer wg.Done()
		buffer := make([]byte, 65535)
		sizes := []int{0}
		for {
			_, e := borrowed.Read([][]byte{buffer}, sizes, 0)
			if e != nil {
				return
			}
			packet := buffer[:sizes[0]]
			start := 20
			if len(packet) > 0 && packet[0]>>4 == 6 {
				start = 40
			}
			if len(packet) < start+8 {
				continue
			}
			payload := string(packet[start+8:])
			if !strings.HasPrefix(payload, "irvio-protected-tun-") {
				continue
			}
			mu.Lock()
			seen[payload] = true
			mu.Unlock()
			if reply := protectionUDPReply(packet); reply != nil {
				_, _ = borrowed.Write([][]byte{reply}, 0)
			}
		}
	}()
	t.Cleanup(func() { borrowed.Close(); wg.Wait() })
	for _, pair := range [][2]string{{"10.253.244.1", "10.253.244.2"}, {"fd77:244::1", "fd77:244::2"}} {
		for _, port := range []int{49497, 11211} {
			proto := "udp4"
			if netip.MustParseAddr(pair[0]).Is6() {
				proto = "udp6"
			}
			socket, e := net.ListenUDP(proto, &net.UDPAddr{IP: net.ParseIP(pair[0])})
			if e != nil {
				t.Fatal(e)
			}
			socket.SetDeadline(time.Now().Add(1200 * time.Millisecond))
			payload := fmt.Sprintf("irvio-protected-tun-%s-%d", proto, port)
			_, e = socket.WriteToUDP([]byte(payload), &net.UDPAddr{IP: net.ParseIP(pair[1]), Port: port})
			buf := make([]byte, 256)
			n, _, readErr := socket.ReadFromUDP(buf)
			socket.Close()
			mu.Lock()
			observed := seen[payload]
			mu.Unlock()
			if port == 49497 {
				if e != nil || readErr != nil || string(buf[:n]) != payload || !observed {
					t.Fatalf("tunnel %s failed: %v %v observed=%v", proto, e, readErr, observed)
				}
			} else if observed || readErr == nil {
				t.Fatalf("Safety Policy leaked %s", proto)
			}
			t.Logf("tunnel %s port=%d observed=%v", proto, port, observed)
		}
	}
	borrowed.Close()
	wg.Wait()
	mu.Lock()
	for payload := range seen {
		if strings.HasSuffix(payload, "-11211") {
			t.Error("late Safety Policy packet observed on TUN")
		}
	}
	mu.Unlock()
	if err := controller.Transition(gen, StateNoExit); err != nil {
		t.Fatal(err)
	}
	probe(foreign, "no-exit-ordinary", false)
	if err := updater.Update(ctx, WindowsNetwork{}); err != nil {
		t.Fatal(err)
	}
	probe(self, "revoked-control", false)
	if err := controller.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	probe(foreign, "disconnected-ordinary", true)
	last, err := s.Inspect(ctx)
	if err != nil || last.Installed {
		t.Fatalf("final residue %+v %v", last, err)
	}
}

func protectionUDPReply(packet []byte) []byte {
	p := append([]byte(nil), packet...)
	if len(p) < 28 {
		return nil
	}
	start := 20
	var pseudo []byte
	if p[0] == 0x45 && p[9] == 17 {
		a := [4]byte{}
		copy(a[:], p[12:16])
		copy(p[12:16], p[16:20])
		copy(p[16:20], a[:])
		p[10], p[11] = 0, 0
		binary.BigEndian.PutUint16(p[10:12], protectionChecksum(p[:20]))
		pseudo = append(pseudo, p[12:20]...)
		pseudo = append(pseudo, 0, 17, byte((len(p)-20)>>8), byte(len(p)-20))
	} else if p[0]>>4 == 6 && len(p) >= 48 && p[6] == 17 {
		start = 40
		a := [16]byte{}
		copy(a[:], p[8:24])
		copy(p[8:24], p[24:40])
		copy(p[24:40], a[:])
		pseudo = append(pseudo, p[8:40]...)
		pseudo = append(pseudo, 0, 0, byte((len(p)-40)>>8), byte(len(p)-40), 0, 0, 0, 17)
	} else {
		return nil
	}
	u := p[start:]
	u[0], u[1], u[2], u[3] = u[2], u[3], u[0], u[1]
	u[6], u[7] = 0, 0
	sum := protectionChecksum(append(pseudo, u...))
	if sum == 0 {
		sum = 0xffff
	}
	binary.BigEndian.PutUint16(u[6:8], sum)
	return p
}

func protectionChecksum(p []byte) uint16 {
	var sum uint32
	for len(p) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(p))
		p = p[2:]
	}
	if len(p) > 0 {
		sum += uint32(p[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
