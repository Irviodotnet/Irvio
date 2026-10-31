// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"reflect"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
)

var errWindowsRouteInjected = errors.New("injected Windows session failure")

type windowsRouteTrace struct {
	events []string
	fail   string
	dns    []netip.Addr
}

func (x *windowsRouteTrace) step(s string) error {
	x.events = append(x.events, s)
	if s == x.fail {
		return errWindowsRouteInjected
	}
	return nil
}
func (x *windowsRouteTrace) UpdateTunnel(_ context.Context, luid uint64, control []killswitch.WindowsTunnelControl) error {
	s := "revoke"
	if len(control) > 0 {
		s = "control-grant"
	} else if luid != 0 {
		s = "user-grant"
	}
	return x.step(s)
}

type windowsRouteFake struct{ x *windowsRouteTrace }

func (r windowsRouteFake) EnsureControl(context.Context, []netip.Addr) error {
	return r.x.step("control-route")
}
func (r windowsRouteFake) Ensure(context.Context, bool, bool) error { return r.x.step("user-route") }
func (r windowsRouteFake) Remove(context.Context) error             { return r.x.step("remove-control-route") }
func (r windowsRouteFake) Close() error                             { return r.x.step("close-routes") }

type windowsDNSFake struct{ x *windowsRouteTrace }

func (d windowsDNSFake) Apply(_ context.Context, servers []netip.Addr) error {
	d.x.dns = append([]netip.Addr(nil), servers...)
	return d.x.step("dns")
}
func (d windowsDNSFake) Close() error { return d.x.step("close-dns") }

type windowsControlFake struct{ x *windowsRouteTrace }

func (c windowsControlFake) Close() error { return c.x.step("release") }

func windowsRoutesFixture(x *windowsRouteTrace) (*windowsClientRoutes, InstallRequest) {
	r := &windowsClientRoutes{protection: x, routes: windowsRouteFake{x}, dns: windowsDNSFake{x}}
	request := InstallRequest{
		Lease:                 tunnellease.Lease{IPv4: netip.MustParsePrefix("10.77.0.2/32")},
		TunnelControl:         []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")},
		TunnelControlEndpoint: netip.MustParseAddrPort("10.77.0.1:51822"),
		BeforeUserRoutes: func(context.Context) (io.Closer, error) {
			if err := x.step("bind"); err != nil {
				return nil, err
			}
			return windowsControlFake{x}, nil
		},
	}
	return r, request
}

func TestWindowsClientRouteActivationOrder(t *testing.T) {
	x := &windowsRouteTrace{}
	r, request := windowsRoutesFixture(x)
	servers := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2606:4700:4700::1111")}
	control, err := r.activate(context.Background(), 77, request, servers)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"control-grant", "control-route", "bind", "remove-control-route", "user-route", "dns", "user-grant"}
	if !reflect.DeepEqual(x.events, want) {
		t.Fatalf("activation order: %v", x.events)
	}
	if !reflect.DeepEqual(x.dns, servers[:1]) {
		t.Fatal("unsupported DNS family installed", x.dns)
	}
	if err := closeInstalledClient(&installedClientDataPlane{control: control, routing: r}); err != nil {
		t.Fatal(err)
	}
	want = append(want, "release", "revoke", "close-dns", "close-routes")
	if !reflect.DeepEqual(x.events, want) {
		t.Fatalf("teardown order: %v", x.events)
	}
	if err := r.Close(); err != nil || !reflect.DeepEqual(x.events, want) {
		t.Fatal("non-idempotent route cleanup", err)
	}
}

func TestWindowsClientRouteFailuresStayClosed(t *testing.T) {
	stages := []string{"control-grant", "control-route", "bind", "remove-control-route", "user-route", "dns", "user-grant"}
	for i, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			x := &windowsRouteTrace{fail: stage}
			r, request := windowsRoutesFixture(x)
			control, err := r.activate(context.Background(), 77, request, []netip.Addr{netip.MustParseAddr("1.1.1.1")})
			if !errors.Is(err, errWindowsRouteInjected) || control != nil {
				t.Fatal("failure lost", err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			want := append([]string(nil), stages[:i+1]...)
			if i > 2 {
				want = append(want, "release")
			}
			want = append(want, "revoke", "close-dns", "close-routes")
			if !reflect.DeepEqual(x.events, want) {
				t.Fatalf("failure order: %v want %v", x.events, want)
			}
		})
	}
}

func TestWindowsClientRouteCleanupTriesEveryResource(t *testing.T) {
	for _, stage := range []string{"revoke", "close-dns", "close-routes"} {
		x := &windowsRouteTrace{fail: stage}
		r, _ := windowsRoutesFixture(x)
		if err := r.Close(); !errors.Is(err, errWindowsRouteInjected) {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(x.events, []string{"revoke", "close-dns", "close-routes"}) {
			t.Fatal("cleanup short circuit", x.events)
		}
	}
}

func TestWindowsClientRouteCleanupRetriesFailedResource(t *testing.T) {
	for _, stage := range []string{"revoke", "close-dns", "close-routes"} {
		t.Run(stage, func(t *testing.T) {
			x := &windowsRouteTrace{fail: stage}
			r, _ := windowsRoutesFixture(x)
			if err := r.Close(); !errors.Is(err, errWindowsRouteInjected) {
				t.Fatal("missing initial cleanup error", err)
			}
			x.fail = ""
			if err := r.Close(); err != nil {
				t.Fatal("resolved failure was not retried", err)
			}
			want := []string{"revoke", "close-dns", "close-routes", stage}
			if !reflect.DeepEqual(x.events, want) {
				t.Fatal("retry must touch only pending resources", x.events)
			}
			if err := r.Close(); err != nil || !reflect.DeepEqual(x.events, want) {
				t.Fatal("cleanup repeated after success", err, x.events)
			}
		})
	}
}

func TestWindowsClientRequiresAuthenticatedControl(t *testing.T) {
	for _, mutate := range []func(*InstallRequest){
		func(r *InstallRequest) { r.BeforeUserRoutes = nil },
		func(r *InstallRequest) { r.TunnelControlEndpoint = netip.AddrPort{} },
		func(r *InstallRequest) { r.TunnelControlEndpoint = netip.MustParseAddrPort("10.77.0.1:0") },
		func(r *InstallRequest) { r.TunnelControlEndpoint = netip.MustParseAddrPort("10.77.0.2:51822") },
		func(r *InstallRequest) { r.TunnelControlEndpoint = netip.MustParseAddrPort("[fd77::1]:51822") },
		func(r *InstallRequest) { r.TunnelControl = nil },
	} {
		x := &windowsRouteTrace{}
		r, request := windowsRoutesFixture(x)
		mutate(&request)
		if c, err := r.activate(context.Background(), 77, request, []netip.Addr{netip.MustParseAddr("1.1.1.1")}); err == nil || c != nil {
			t.Fatal("unsafe request accepted")
		}
		if len(x.events) != 0 {
			t.Fatal("invalid inputs changed host state", x.events)
		}
	}
}
