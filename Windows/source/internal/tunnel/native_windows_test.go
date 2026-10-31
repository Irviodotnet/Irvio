// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnel

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"golang.org/x/sys/windows"
)

func nativeTUNEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("IRVIO_WINDOWS_TUN_TEST") != "1" {
		t.Skip("requires an isolated elevated Windows lab and signed wintun.dll beside the test executable")
	}
}

func nativeOwner(t *testing.T, suffix int) *Windows {
	t.Helper()
	w, err := NewWindows(Config{Name: fmt.Sprintf("IRVIO-T%05d-%d", os.Getpid()%100000, suffix), Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.242.1/24"), netip.MustParsePrefix("fd77:242::1/64")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	return w
}

func assertNativeOwner(t *testing.T, w *Windows) {
	t.Helper()
	row := windows.MibIfRow2{InterfaceLuid: w.LUID()}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		t.Fatal(err)
	}
	if row.InterfaceIndex != uint32(w.IfIndex()) || row.OperStatus != windows.IfOperStatusUp {
		t.Fatalf("adapter changed/down: %+v", row)
	}
	for _, family := range []uint16{windows.AF_INET, windows.AF_INET6} {
		ip := tunIPInterfaceRow{InterfaceLuid: w.LUID(), Family: family}
		if err := tunCallRow(tunGetIPInterface, unsafe.Pointer(&ip)); err != nil {
			t.Fatal(err)
		}
		if ip.NlMtu != DefaultMTU {
			t.Fatalf("MTU changed: %d", ip.NlMtu)
		}
		if ip.DadTransmits != 0 {
			t.Fatalf("owned TUN still performs DAD: %d", ip.DadTransmits)
		}
	}
	for _, p := range w.Addresses() {
		address := tunAddressRow(w.LUID(), p)
		if err := windows.GetUnicastIpAddressEntry(&address); err != nil {
			t.Fatal(err)
		}
		if address.DadState != windows.IpDadStatePreferred {
			t.Fatal("source not ready")
		}
	}
}

func TestWindowsNativePacketsAndGenerationLifetime(t *testing.T) {
	nativeTUNEnabled(t)
	w := nativeOwner(t, 1)
	if duplicate, err := NewWindows(Config{Name: w.Name(), Addresses: w.Addresses()}); err == nil {
		duplicate.Close()
		t.Fatal("existing adapter name was accepted")
	}
	for generation := 0; generation < 3; generation++ {
		b, err := w.Borrow()
		if err != nil {
			t.Fatal(err)
		}
		assertNativeOwner(t, w)
		for _, pair := range [][2]string{{"10.253.242.1", "10.253.242.2"}, {"fd77:242::1", "fd77:242::2"}} {
			nativeRoundTrip(t, b, pair[0], pair[1], generation)
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		assertNativeOwner(t, w)
	}
	prefix := netip.MustParsePrefix("10.253.243.3/32")
	if err := w.AddAddresses([]netip.Prefix{prefix}); err != nil {
		t.Fatal(err)
	}
	row := tunAddressRow(w.LUID(), prefix)
	if err := windows.GetUnicastIpAddressEntry(&row); err != nil {
		t.Fatal(err)
	}
	if err := w.RemoveAddresses([]netip.Prefix{prefix}); err != nil {
		t.Fatal(err)
	}
	if err := windows.GetUnicastIpAddressEntry(&row); err == nil {
		t.Fatal("dynamic address remained")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	requireNativeGone(t, w)
}

func TestWindowsNativeDualRoleSourceSelection(t *testing.T) {
	nativeTUNEnabled(t)
	w := nativeOwner(t, 9)
	control := w.Addresses()[0]
	upstream := netip.MustParsePrefix("10.253.243.3/32")
	destination := netip.MustParseAddr("10.253.242.2")
	if err := w.AddAddresses([]netip.Prefix{upstream}); err != nil {
		t.Fatal(err)
	}
	before, err := winroute.ResolveTunnel(destination, w.LUID())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("unconstrained source before Exit exclusion: %s", before.Source)
	if err := w.SetAutomaticSource(control, false); err != nil {
		t.Fatal(err)
	}
	row := tunAddressRow(w.LUID(), control)
	if err := windows.GetUnicastIpAddressEntry(&row); err != nil || row.SkipAsSource != 1 {
		t.Fatal("Exit control still eligible for automatic source selection", row.SkipAsSource, err)
	}
	selected, err := winroute.ResolveTunnel(destination, w.LUID())
	if err != nil || selected.Source != upstream.Addr() {
		t.Fatal("own traffic did not select upstream lease", selected, err)
	}
	b, err := w.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	nativeRoundTrip(t, b, control.Addr().String(), destination.String(), 0)
	if err := w.RemoveAddresses([]netip.Prefix{upstream}); err != nil {
		t.Fatal(err)
	}
	if selected, err := winroute.ResolveTunnel(destination, w.LUID()); err == nil {
		t.Fatal("automatic source fell back to Exit control after upstream removal", selected)
	}
	if err := w.SetAutomaticSource(control, true); err != nil {
		t.Fatal(err)
	}
	selected, err = winroute.ResolveTunnel(destination, w.LUID())
	if err != nil || selected.Source != control.Addr() {
		t.Fatal("source restoration failed", selected, err)
	}
	t.Log("upstream source selected; explicit Exit control traffic works; no control-source fallback after upstream removal")
}

func nativeRoundTrip(t *testing.T, b awgtun.Device, source, dest string, generation int) {
	t.Helper()
	network := "udp4"
	if netip.MustParseAddr(source).Is6() {
		network = "udp6"
	}
	socket, err := net.ListenUDP(network, &net.UDPAddr{IP: net.ParseIP(source)})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	socket.SetDeadline(time.Now().Add(5 * time.Second))
	payload := []byte(fmt.Sprintf("irvio-native-tun-%s-generation-%d", network, generation))
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 65535)
		sizes := []int{0}
		for {
			_, err := b.Read([][]byte{buf}, sizes, 0)
			if err != nil {
				done <- err
				return
			}
			packet := buf[:sizes[0]]
			if !bytes.HasSuffix(packet, payload) {
				continue
			}
			reply := nativeUDPReply(packet)
			if reply == nil {
				done <- fmt.Errorf("invalid kernel UDP packet")
				return
			}
			_, err = b.Write([][]byte{reply}, 0)
			done <- err
			return
		}
	}()
	if _, err = socket.WriteToUDP(payload, &net.UDPAddr{IP: net.ParseIP(dest), Port: 49497}); err != nil {
		b.Close()
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, from, err := socket.ReadFromUDP(buf)
	if err != nil {
		b.Close()
		t.Fatal("native response", err)
	}
	if !bytes.Equal(buf[:n], payload) || !from.IP.Equal(net.ParseIP(dest)) {
		t.Fatal("native response mismatch")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		b.Close()
		t.Fatal("TUN packet path stuck")
	}
	t.Logf("native %s generation=%d kernel-to-Wintun-to-kernel PASS", network, generation)
}

func nativeUDPReply(packet []byte) []byte {
	p := append([]byte(nil), packet...)
	var start int
	var pseudo []byte
	switch p[0] >> 4 {
	case 4:
		if len(p) < 28 || p[0] != 0x45 || p[9] != 17 {
			return nil
		}
		start = 20
		source := append([]byte(nil), p[12:16]...)
		copy(p[12:16], p[16:20])
		copy(p[16:20], source)
		p[10], p[11] = 0, 0
		binary.BigEndian.PutUint16(p[10:12], nativeChecksum(p[:20]))
		pseudo = append(pseudo, p[12:20]...)
		pseudo = append(pseudo, 0, 17, byte((len(p)-20)>>8), byte(len(p)-20))
	case 6:
		if len(p) < 48 || p[6] != 17 {
			return nil
		}
		start = 40
		source := append([]byte(nil), p[8:24]...)
		copy(p[8:24], p[24:40])
		copy(p[24:40], source)
		pseudo = append(pseudo, p[8:40]...)
		pseudo = append(pseudo, 0, 0, byte((len(p)-40)>>8), byte(len(p)-40), 0, 0, 0, 17)
	default:
		return nil
	}
	u := p[start:]
	u[0], u[1], u[2], u[3] = u[2], u[3], u[0], u[1]
	u[6], u[7] = 0, 0
	checksum := nativeChecksum(append(pseudo, u...))
	if checksum == 0 {
		checksum = 0xffff
	}
	binary.BigEndian.PutUint16(u[6:8], checksum)
	return p
}

func nativeChecksum(p []byte) uint16 {
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

func requireNativeGone(t *testing.T, w *Windows) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		row := windows.MibIfRow2{InterfaceLuid: w.LUID()}
		if windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row) != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("owned adapter remained after Close")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func tunHandleCount(t *testing.T) uint32 {
	t.Helper()
	var count uint32
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	r, _, err := proc.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
	if r == 0 {
		t.Fatal(err)
	}
	return count
}

func tunThreadCount(t *testing.T) int {
	t.Helper()
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	count := 0
	for err := windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID == uint32(os.Getpid()) {
			count++
		}
	}
	return count
}

func TestWindowsNativeBorrow100Cycles(t *testing.T) {
	nativeTUNEnabled(t)
	w := nativeOwner(t, 2)
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	cycle := func() {
		b, err := w.Borrow()
		if err != nil {
			t.Fatal(err)
		}
		done := blockedRead(b)
		time.Sleep(2 * time.Millisecond)
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		requireStopped(t, done)
	}
	for range 20 {
		cycle()
	}
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := tunHandleCount(t)
	for range 100 {
		cycle()
	}
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	final := tunHandleCount(t)
	t.Logf("100 native borrower lifetimes: handles %d -> %d", baseline, final)
	if final > baseline+4 {
		t.Fatal("native borrower handle growth")
	}
	assertNativeOwner(t, w)
	b, err := w.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	done := blockedRead(b)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	requireStopped(t, done)
	requireNativeGone(t, w)
}

func TestWindowsNativeOwnerCleanup(t *testing.T) {
	nativeTUNEnabled(t)
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	cycle := func() {
		w := nativeOwner(t, 3)
		assertNativeOwner(t, w)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		requireNativeGone(t, w)
		t.Logf("adapter cycle handles=%d threads=%d goroutines=%d", tunHandleCount(t), tunThreadCount(t), runtime.NumGoroutine())
	}
	for range 3 {
		cycle()
	}
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := tunHandleCount(t)
	for range 10 {
		if os.Getenv("IRVIO_TUN_HANDLE_DIAGNOSTIC") == "1" {
			t.Logf("handle types: %v", tunHandleTypes())
		}
		cycle()
	}
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	final := tunHandleCount(t)
	t.Logf("10 native adapter create/configure/close cycles: handles %d -> %d", baseline, final)
	if final > baseline+4 {
		t.Fatal("native adapter handle growth")
	}
}
