// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestWindowsTunnelEndpoint(t *testing.T) {
	for _, test := range []struct {
		network, address string
		listen, valid    bool
		family           string
	}{
		{"tcp", "10.0.0.2:51822", false, true, "tcp4"},
		{"tcp6", "[fd00::2]:51822", false, true, "tcp6"},
		{"tcp", "10.0.0.1:0", true, true, "tcp4"},
		{"tcp", "10.0.0.1:0", false, false, ""},
		{"tcp", "example.org:51822", false, false, ""},
		{"tcp", ":51822", true, false, ""},
		{"tcp", "0.0.0.0:51822", true, false, ""},
		{"tcp", "[::]:51822", true, false, ""},
		{"tcp", "127.0.0.1:51822", false, false, ""},
		{"tcp", "[::1]:51822", false, false, ""},
		{"tcp", "169.254.1.2:51822", false, false, ""},
		{"tcp", "[fe80::2%4]:51822", false, false, ""},
		{"tcp", "[::ffff:10.0.0.2]:51822", false, false, ""},
		{"tcp", "224.0.0.1:51822", false, false, ""},
		{"tcp", "255.255.255.255:51822", false, false, ""},
		{"udp4", "10.0.0.2:51822", false, false, ""},
		{"tcp6", "10.0.0.2:51822", false, false, ""},
		{"tcp4", "[fd00::2]:51822", false, false, ""},
	} {
		t.Run(test.network+"/"+test.address, func(t *testing.T) {
			family, endpoint, err := tunnelEndpoint(test.network, test.address, test.listen)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
			if test.valid && (family != test.family || endpoint != netip.MustParseAddrPort(test.address)) {
				t.Fatalf("endpoint changed: %s %s", family, endpoint)
			}
		})
	}
}

func TestWindowsTunnelDialRefusesMissingOwner(t *testing.T) {
	for _, name := range []string{"", "IRVIO-Missing-Session-Control"} {
		dial := TunnelDialer(name)
		conn, err := dial(context.Background(), "tcp", "10.0.0.2:51822")
		if conn != nil || err == nil {
			t.Fatal("missing tunnel accepted", err)
		}
		if conn, err := dial(nil, "tcp", "10.0.0.2:51822"); conn != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("nil context accepted", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if conn, err := dial(ctx, "tcp", "10.0.0.2:51822"); conn != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation ignored", err)
		}
	}
	if _, err := ListenTunnel(nil, "tcp", "10.0.0.1:51822", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil listen context accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListenTunnel(ctx, "tcp", "10.0.0.1:51822", "x"); !errors.Is(err, context.Canceled) {
		t.Fatal("listen cancellation ignored", err)
	}
	if err := (tunnelInterface{}).validate(); err == nil {
		t.Fatal("empty interface identity accepted")
	}
}
