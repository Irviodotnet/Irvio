// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

func TestWindowsProductPathsIgnoreEnvironment(t *testing.T) {
	before, err := WindowsDefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ProgramData", `Z:\untrusted`)
	after, err := WindowsDefaultPaths()
	if err != nil || before != after || !filepath.IsAbs(after.Profile) || filepath.Base(after.GeoIP) != "country.mmdb" {
		t.Fatalf("native paths changed: %+v %+v %v", before, after, err)
	}
}

func TestWindowsSystemConfigACLAndStrictSchema(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	path := filepath.Join(root, "config.json")
	config, err := loadWindowsSystemConfig(path)
	if err != nil || config != (FileConfig{Version: 1}) {
		t.Fatalf("missing optional config: %+v %v", config, err)
	}
	d, err := winfile.OpenDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if user.User.Sid.String() != "S-1-5-18" {
		if _, err := loadWindowsSystemConfig(path); err == nil {
			t.Fatal("user-owned config directory accepted for privileged service")
		}
		return
	}
	for _, value := range []string{`{"version":1,"role":"server"}`, `{"version":1,"unknown":true}`, `{"version":2}`, `{"version":1,"role":"desktop","role":"server"}`} {
		if err := d.WriteAtomic("config.json", []byte(value), true); err != nil {
			t.Fatal(err)
		}
		config, err := loadWindowsSystemConfig(path)
		if value == `{"version":1,"role":"server"}` {
			if err != nil || config.Role != Server {
				t.Fatalf("valid system config: %+v %v", config, err)
			}
		} else if err == nil {
			t.Fatalf("invalid config accepted: %s", value)
		}
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWindowsSystemConfig(path); !errors.Is(err, winfile.ErrUnsafe) {
		t.Fatalf("world-writable config accepted or wrong error: %v", err)
	}
}

func TestWindowsBootstrapRouteFallback(t *testing.T) {
	pool := []netip.AddrPort{netip.MustParseAddrPort("[::1]:3478"), netip.MustParseAddrPort("192.0.2.1:3478"), netip.MustParseAddrPort("198.51.100.1:3478")}
	var attempts []netip.Addr
	want := winroute.Path{Target: pool[2].Addr(), Name: "kernel-selected", Binding: winroute.Binding{LUID: 123, Index: 5, Source: netip.MustParseAddr("10.0.2.15")}}
	got, err := resolveWindowsBootstrapUnderlay(pool, func(target netip.Addr) (winroute.Path, error) {
		attempts = append(attempts, target)
		if target == pool[1].Addr() {
			return winroute.Path{}, errors.New("local/unusable first path")
		}
		return want, nil
	})
	if err != nil || got != want || !reflect.DeepEqual(attempts, []netip.Addr{pool[1].Addr(), pool[2].Addr()}) {
		t.Fatalf("path=%+v attempts=%v error=%v", got, attempts, err)
	}
	if _, err := resolveWindowsBootstrapUnderlay(nil, nil); err == nil {
		t.Fatal("empty pool accepted")
	}
}

func testWindowsRoute(prefix, gateway string, luid uint64) windows.MibIpForwardRow2 {
	p := netip.MustParsePrefix(prefix)
	row := windows.MibIpForwardRow2{InterfaceLuid: luid, InterfaceIndex: uint32(luid), ValidLifetime: 100}
	row.DestinationPrefix.PrefixLength = uint8(p.Bits())
	*(*windows.RawSockaddrInet4)(unsafe.Pointer(&row.DestinationPrefix.Prefix)) = windows.RawSockaddrInet4{Family: windows.AF_INET, Addr: p.Addr().As4()}
	*(*windows.RawSockaddrInet4)(unsafe.Pointer(&row.NextHop)) = windows.RawSockaddrInet4{Family: windows.AF_INET, Addr: netip.MustParseAddr(gateway).As4()}
	return row
}

func TestWindowsConnectedRoutesNeverBypassDefaultsOrTunnel(t *testing.T) {
	rows := []windows.MibIpForwardRow2{
		testWindowsRoute("0.0.0.0/0", "0.0.0.0", 1),
		testWindowsRoute("192.168.23.0/24", "0.0.0.0", 1),
		testWindowsRoute("192.168.23.0/24", "0.0.0.0", 2),
		testWindowsRoute("10.0.0.0/8", "192.168.23.1", 1),
		testWindowsRoute("10.8.0.0/24", "0.0.0.0", 3),
		testWindowsRoute("127.0.0.0/8", "0.0.0.0", 4),
		testWindowsRoute("224.0.0.0/4", "0.0.0.0", 1),
		testWindowsRoute("172.16.0.0/16", "0.0.0.0", 1),
	}
	rows[len(rows)-1].ValidLifetime = 0
	got := windowsConnectedPrefixes(rows, []uint64{3})
	if want := []netip.Prefix{netip.MustParsePrefix("192.168.23.0/24")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bypass=%v want=%v", got, want)
	}
	rows[0].DestinationPrefix.PrefixLength = 33
	if _, ok := windowsRoutePrefix(rows[0]); ok {
		t.Fatal("invalid native prefix accepted")
	}
}

func TestWindowsNativeProductHostResolution(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_PRODUCT_HOST_TEST") != "1" {
		t.Skip("native lab route oracle required")
	}
	target := netip.MustParseAddrPort("1.1.1.1:3478")
	path, err := ResolveBootstrapUnderlay([]netip.AddrPort{target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if expected := os.Getenv("IRVIO_WINDOWS_PRODUCT_SOURCE"); path.Binding.Source.String() != expected {
		t.Fatalf("source=%s expected native oracle=%s", path.Binding.Source, expected)
	}
	if expected := os.Getenv("IRVIO_WINDOWS_PRODUCT_INTERFACE"); path.Name != expected {
		t.Fatalf("interface=%s expected native oracle=%s", path.Name, expected)
	}
	if _, err := ResolveBootstrapUnderlay([]netip.AddrPort{target}, []uint64{path.Binding.LUID}); err == nil {
		t.Fatal("excluded LUID selected as underlay")
	}
	if _, err := ResolveBootstrapUnderlay([]netip.AddrPort{netip.AddrPortFrom(path.Binding.Source, 3478)}, nil); err == nil {
		t.Fatal("own address accepted as WAN target")
	}
	prefixes, err := hostIPv4Prefixes()
	if err != nil || !overlapsAny(netip.PrefixFrom(path.Binding.Source, 32), prefixes) {
		t.Fatalf("native pool collision inventory missing own address: %v %v", prefixes, err)
	}
	bypass, err := ConnectedBypassPrefixes(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range bypass {
		if prefix.Bits() == 0 {
			t.Fatal("default bypass")
		}
	}
	t.Logf("kernel path interface=%s index=%d source=%s gateway=%s; prefixes=%d bypass=%d", path.Name, path.Binding.Index, path.Binding.Source, path.Gateway, len(prefixes), len(bypass))
}
