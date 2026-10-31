// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestConfigAndListenAddressValidation(t *testing.T) {
	registry, err := NewRegistry(DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	muxConfig.Classifier = NewClassifier(registry)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()

	tests := []struct {
		name      string
		addresses []netip.Addr
		queue     int
	}{
		{"empty", nil, 1},
		{"zero-queue", []netip.Addr{netip.MustParseAddr("127.0.0.1")}, 0},
		{"unspecified", []netip.Addr{netip.IPv4Unspecified()}, 1},
		{"multicast", []netip.Addr{netip.MustParseAddr("224.0.0.1")}, 1},
		{"mapped", []netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1")}, 1},
		{"unavailable-family", []netip.Addr{netip.MustParseAddr("::1")}, 1},
		{"different-bind", []netip.Addr{netip.MustParseAddr("127.0.0.2")}, 1},
		{"multiple-ipv4", []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("127.0.0.2")}, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateRegistry, registryErr := NewRegistry(DefaultRegistryConfig())
			if registryErr != nil {
				t.Fatal(registryErr)
			}
			_, err := New(mux.Control(), candidateRegistry, Config{
				ListenAddresses: test.addresses, ConnectionQueueCapacity: test.queue,
			})
			if err == nil {
				t.Fatal("New() unexpectedly succeeded")
			}
		})
	}

	duplicateRegistry, err := NewRegistry(DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	address := netip.MustParseAddr("127.0.0.1")
	adapter, err := New(mux.Control(), duplicateRegistry, Config{
		ListenAddresses: []netip.Addr{address, address}, ConnectionQueueCapacity: 1,
	})
	if err != nil {
		t.Fatalf("duplicate addresses were not deduplicated: %v", err)
	}
	defer adapter.Close()
	if got := len(adapter.GetListenAddresses()); got != 1 {
		t.Fatalf("deduplicated address count = %d", got)
	}
}
