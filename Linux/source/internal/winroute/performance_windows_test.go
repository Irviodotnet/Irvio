// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"net"
	"net/netip"
	"os"
	"testing"
)

// BenchmarkNativeBinding checks the cost of the existing packet-path fence without changing it.
func BenchmarkNativeBinding(b *testing.B) {
	value := os.Getenv("IRVIO_BENCH_SOURCE")
	if value == "" {
		b.Skip("set IRVIO_BENCH_SOURCE to a lab interface's local address")
	}
	source, err := netip.ParseAddr(value)
	if err != nil {
		b.Fatal(err)
	}
	binding, err := ForSource(source)
	if err != nil {
		b.Fatal(err)
	}
	iface, err := net.InterfaceByIndex(int(binding.Index))
	if err != nil {
		b.Fatal(err)
	}
	b.Run("ForSource", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			current, err := ForSource(source)
			if err != nil || current != binding {
				b.Fatalf("binding changed: %+v, %v", current, err)
			}
		}
	})
	b.Run("ValidateKnownBinding", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := binding.validate(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Interfaces", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := net.Interfaces(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("InterfaceAddresses", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := iface.Addrs(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
