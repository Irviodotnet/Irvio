// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windns

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sys/windows"
)

func dnsProtectionOwner() wfp.Config {
	return wfp.Config{Provider: windows.GUID{Data1: 0xad438dad, Data2: 0x231d, Data3: 0x482d, Data4: [8]byte{0xb8, 0xaf, 0xf0, 0x49, 0x3c, 0x95, 0x42, 0x53}}, Sublayer: windows.GUID{Data1: 0x53527385, Data2: 0x96ab, Data3: 0x48a1, Data4: [8]byte{0x87, 0xaa, 0xd1, 0x37, 0xac, 0x7c, 0x6a, 0x90}}, Name: "IRVIO DNS protected resolver lab", Marker: "irvio-w05-dns-protection-v1", ServiceName: "IRVIO-W05-Owner", Weight: 49300}
}

func TestNativeDNSProtectionCleanup(t *testing.T) {
	if os.Getenv("IRVIO_DNS_PROTECTION_CLEANUP") != "1" {
		t.Skip("explicit exact lab owner cleanup only")
	}
	s, err := wfp.NewStore(dnsProtectionOwner())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNativeDNSOrdinaryResolver(t *testing.T) {
	if os.Getenv("IRVIO_DNS_PACKET_TEST") != "1" {
		t.Skip("full VM protection requires Guest Agent and lab owner service")
	}
	recoveryMode := os.Getenv("IRVIO_DNS_RECOVERY_TEST")
	if recoveryMode != "" && recoveryMode != "1" && recoveryMode != "policy" {
		t.Fatal("unknown DNS recovery fixture mode")
	}
	owner, _ := nativeOwner(t)
	s, err := wfp.NewStore(dnsProtectionOwner())
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Inspect(context.Background())
	if err != nil || before.Installed {
		t.Fatal("unexpected policy fixture", err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	app, err := wfp.ApplicationIDForPath(self)
	if err != nil {
		t.Fatal(err)
	}
	path, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	marker := func(stage string) {
		t.Helper()
		dialer := net.Dialer{Timeout: time.Second, LocalAddr: &net.UDPAddr{IP: net.IP(path.Binding.Source.AsSlice())}, Control: path.Binding.Control}
		c, err := dialer.DialContext(context.Background(), "udp4", "192.168.23.2:49495")
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Write([]byte("irvio-dns-marker-" + stage))
		c.Close()
		if err != nil {
			t.Fatal(err)
		}
		t.Log("capture marker " + stage)
	}
	t.Cleanup(func() {
		if err := s.Remove(context.Background()); err != nil {
			t.Error(err)
		}
	})
	policy := killswitch.Policy{TUNName: "IRVIO-DNS", BypassMark: 1}
	b, err := killswitch.NewWindowsBackend(killswitch.WindowsBackendConfig{Policy: policy, Owner: dnsProtectionOwner(), Network: killswitch.WindowsNetwork{TunnelLUID: owner.LUID(), Application: app, Underlay: []killswitch.WindowsBypass{{Interface: path.Binding.LUID, Source: path.Binding.Source, Remote: netip.MustParseAddrPort("192.168.23.2:49495"), Protocol: 17}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Ensure(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	marker("protection-installed")
	var routes *winroute.TunnelRoutes
	if os.Getenv("IRVIO_DNS_ROUTES_TEST") == "1" {
		routes, err = winroute.OpenTunnelRoutes(owner.LUID(), filepath.Join(t.TempDir(), "routes"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := routes.Close(); err != nil {
				t.Error(err)
			}
		})
		if err := routes.Ensure(context.Background(), true, true); err != nil {
			t.Fatal(err)
		}
		t.Log("owned IPv4/IPv6 split routes installed after WFP protection")
	}
	m, err := Open(owner.LUID(), filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	borrowed, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	name := ""
	seen := 0
	drop := false
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 65535)
		sizes := []int{0}
		for {
			if _, err := borrowed.Read([][]byte{buf}, sizes, 0); err != nil {
				return
			}
			mu.Lock()
			reply := dnsUDPReply(buf[:sizes[0]], name)
			discard := drop
			if reply != nil {
				seen++
			}
			mu.Unlock()
			if reply != nil && !discard {
				_, _ = borrowed.Write([][]byte{reply}, 0)
			}
		}
	}()
	t.Cleanup(func() { borrowed.Close(); wg.Wait() })
	servers := []string{"10.253.245.53", "fd77:245::53"}
	if routes != nil {
		servers = []string{"203.0.113.53", "2001:db8::53"}
	}
	for _, server := range servers {
		if err := m.Apply(context.Background(), []netip.Addr{netip.MustParseAddr(server)}); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		name = fmt.Sprintf("p%d.irvio.invalid.", time.Now().UnixNano())
		query := name
		seen = 0
		mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		got, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", query)
		cancel()
		mu.Lock()
		count := seen
		mu.Unlock()
		if err != nil || len(got) != 1 || got[0] != netip.MustParseAddr("203.0.113.25") || count == 0 {
			t.Fatalf("ordinary resolver via %s: %v %v packets=%d", server, got, err, count)
		}
		t.Logf("ordinary Windows resolver via TUN DNS %s PASS, packets=%d query=%s", server, count, query)
	}
	if recoveryMode != "" {
		if err := m.Apply(context.Background(), []netip.Addr{netip.MustParseAddr(servers[0])}); err != nil {
			t.Fatal(err)
		}
		t.Logf("DNS recovery fixture uses IPv4 resolver %s", servers[0])
	}
	var recoveryUpdater *killswitch.WindowsNetworkUpdater
	if recoveryMode == "policy" {
		recoveryUpdater, err = b.NetworkUpdater(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := recoveryUpdater.UpdateTunnel(context.Background(), 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	name = fmt.Sprintf("drop%d.irvio.invalid.", time.Now().UnixNano())
	query := name
	seen = 0
	drop = true
	mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
	_, err = net.DefaultResolver.LookupNetIP(ctx, "ip4", query)
	cancel()
	mu.Lock()
	count := seen
	mu.Unlock()
	if err == nil || (recoveryUpdater == nil && count == 0) || (recoveryUpdater != nil && count != 0) {
		t.Fatalf("missing DNS negative control: %v packets=%d", err, count)
	}
	t.Logf("unavailable tunnel DNS fails lookup under protection, packets=%d query=%s error=%v", count, query, err)
	marker("lookup-cancelled-policy-still-active")
	quick := os.Getenv("IRVIO_DNS_QUICK_TEARDOWN") == "1"
	if !quick {
		time.Sleep(15 * time.Second)
	}
	if state, err := s.Inspect(context.Background()); err != nil || !state.Active {
		t.Fatal("protection absent during pending resolver hold", err)
	}
	marker("post-hold-policy-still-active")
	if recoveryMode != "" {
		mu.Lock()
		drop = false
		mu.Unlock()
		if recoveryUpdater != nil {
			if err := recoveryUpdater.UpdateTunnel(context.Background(), owner.LUID(), nil); err != nil {
				t.Fatal(err)
			}
		}
		for _, kind := range []string{"same-name", "fresh-name"} {
			mu.Lock()
			if kind == "fresh-name" {
				name = fmt.Sprintf("recovered%d.irvio.invalid.", time.Now().UnixNano())
			}
			query = name
			seen = 0
			mu.Unlock()
			started := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			got, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", query)
			cancel()
			mu.Lock()
			count = seen
			mu.Unlock()
			t.Logf("DNS recovery %s duration=%s packets=%d answers=%v error=%v", kind, time.Since(started), count, got, err)
			if err != nil || len(got) != 1 || got[0] != netip.MustParseAddr("203.0.113.25") || count == 0 {
				t.Errorf("ordinary resolver did not recover for %s", kind)
			}
		}
	}
	if err := m.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	marker("dns-restored-policy-still-active")
	if routes != nil {
		if err := routes.Remove(context.Background()); err != nil {
			t.Fatal(err)
		}
		if state, err := s.Inspect(context.Background()); err != nil || !state.Active {
			t.Fatal("protection absent after route cleanup", err)
		}
		t.Log("owned routes removed after DNS restoration while WFP remains active")
	}
	if !quick {
		time.Sleep(2 * time.Second)
	}
	marker("before-protection-remove")
	if err := s.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	marker("after-protection-remove")
}

func dnsUDPReply(packet []byte, name string) []byte {
	start := 20
	if len(packet) < 28 || name == "" {
		return nil
	}
	if packet[0] == 0x45 && packet[9] == 17 {
	} else if packet[0]>>4 == 6 && len(packet) >= 48 && packet[6] == 17 {
		start = 40
	} else {
		return nil
	}
	if binary.BigEndian.Uint16(packet[start+2:start+4]) != 53 {
		return nil
	}
	var q dnsmessage.Message
	if q.Unpack(packet[start+8:]) != nil || len(q.Questions) != 1 || q.Questions[0].Name.String() != name {
		return nil
	}
	question := q.Questions[0]
	response := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, Authoritative: true, RecursionDesired: q.RecursionDesired, RecursionAvailable: true}, Questions: q.Questions}
	if question.Type == dnsmessage.TypeA {
		response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AResource{A: [4]byte{203, 0, 113, 25}}}}
	}
	data, err := response.Pack()
	if err != nil {
		return nil
	}
	p := append([]byte(nil), packet[:start+8]...)
	p = append(p, data...)
	var pseudo []byte
	if start == 20 {
		a := [4]byte{}
		copy(a[:], p[12:16])
		copy(p[12:16], p[16:20])
		copy(p[16:20], a[:])
		binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
		p[10], p[11] = 0, 0
		binary.BigEndian.PutUint16(p[10:12], dnsChecksum(p[:20]))
		pseudo = append(pseudo, p[12:20]...)
		pseudo = append(pseudo, 0, 17, byte((len(p)-20)>>8), byte(len(p)-20))
	} else {
		a := [16]byte{}
		copy(a[:], p[8:24])
		copy(p[8:24], p[24:40])
		copy(p[24:40], a[:])
		binary.BigEndian.PutUint16(p[4:6], uint16(len(p)-40))
		pseudo = append(pseudo, p[8:40]...)
		pseudo = append(pseudo, 0, 0, byte((len(p)-40)>>8), byte(len(p)-40), 0, 0, 0, 17)
	}
	u := p[start:]
	u[0], u[1], u[2], u[3] = u[2], u[3], u[0], u[1]
	binary.BigEndian.PutUint16(u[4:6], uint16(len(u)))
	u[6], u[7] = 0, 0
	sum := dnsChecksum(append(pseudo, u...))
	if sum == 0 {
		sum = 0xffff
	}
	binary.BigEndian.PutUint16(u[6:8], sum)
	return p
}

func dnsChecksum(p []byte) uint16 {
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
